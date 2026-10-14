package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hnewcity/mantle-mask/internal/backup"
	"github.com/hnewcity/mantle-mask/internal/gateway"
)

func (s *Server) beginBackup(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	if !s.backupMu.TryLock() {
		abortError(c, http.StatusConflict, "backup_busy", "另一个备份或恢复任务正在执行，请稍后重试。")
		return false
	}
	if s.gateway.RestorePending() {
		s.backupMu.Unlock()
		abortError(c, http.StatusConflict, "restore_pending", "之前的恢复尚未完成。现有请求继续处理；请检查磁盘空间与权限并重启以继续恢复。")
		return false
	}
	return true
}

func (s *Server) exportBackup(c *gin.Context) {
	if !s.beginBackup(c) {
		return
	}
	defer s.backupMu.Unlock()
	var input struct {
		Password string `json:"password"`
	}
	if !decode(c, &input) {
		return
	}
	if err := backup.ValidatePassword(input.Password); err != nil {
		abortError(c, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}
	snapshot, err := s.gateway.ExportBackup(c.Request.Context())
	if err != nil {
		if errors.Is(err, backup.ErrTooLarge) {
			abortError(c, http.StatusRequestEntityTooLarge, "backup_too_large", err.Error())
			return
		}
		slog.Error("backup snapshot failed", "error", err)
		abortError(c, http.StatusInternalServerError, "backup_failed", "读取完整备份失败，请检查数据存储。未生成不完整备份。")
		return
	}
	plain, err := json.Marshal(snapshot)
	if err != nil {
		abortError(c, http.StatusInternalServerError, "backup_failed", "备份序列化失败。")
		return
	}
	defer clear(plain)
	data, err := backup.Encrypt(plain, input.Password)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, backup.ErrTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		abortError(c, status, "backup_failed", err.Error())
		return
	}
	c.Header("Content-Disposition", `attachment; filename="mantle-mask-`+time.Now().UTC().Format("20060102-150405")+`.mmbak"`)
	c.Data(http.StatusOK, "application/octet-stream", data)
}

func (s *Server) restoreBackup(c *gin.Context) {
	if !s.beginBackup(c) {
		return
	}
	defer s.backupMu.Unlock()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, backup.MaxSize+(1<<20))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		status := http.StatusBadRequest
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		abortError(c, status, "invalid_backup_upload", "上传失败：请提供不超过 256 MiB 的备份文件。")
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	if c.PostForm("confirm") != "RESTORE" {
		abortError(c, http.StatusBadRequest, "confirmation_required", "请确认恢复配置并合并用量，或仅补齐用量。")
		return
	}
	password := c.PostForm("password")
	if err := backup.ValidatePassword(password); err != nil {
		abortError(c, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}
	files := c.Request.MultipartForm.File["file"]
	if len(files) != 1 || files[0].Size > backup.MaxSize {
		abortError(c, http.StatusBadRequest, "invalid_backup_file", "请选择一个不超过 256 MiB 的备份文件。")
		return
	}
	file, err := files[0].Open()
	if err != nil {
		abortError(c, http.StatusBadRequest, "invalid_backup_file", "无法读取备份文件。")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, backup.MaxSize+1))
	if err != nil {
		abortError(c, http.StatusBadRequest, "invalid_backup_file", "无法读取备份文件。")
		return
	}
	plain, err := backup.Decrypt(data, password)
	if err != nil {
		abortError(c, http.StatusBadRequest, "invalid_backup", err.Error())
		return
	}
	defer clear(plain)
	snapshot, err := gateway.DecodeBackup(plain)
	if err != nil {
		abortError(c, http.StatusBadRequest, "invalid_backup", err.Error())
		return
	}
	mode := c.PostForm("mode")
	if mode != "" && mode != "full" && mode != "usage" {
		abortError(c, http.StatusBadRequest, "invalid_restore_mode", "不支持的恢复模式。")
		return
	}
	usageOnly := mode == "usage"
	prepared, err := s.gateway.PrepareBackup(snapshot, usageOnly)
	if err != nil {
		abortError(c, http.StatusBadRequest, "invalid_backup", "备份数据校验失败："+err.Error())
		return
	}
	if err := s.gateway.RestoreBackup(prepared); err != nil {
		slog.Error("backup restore failed", "error", err)
		text := "无法写入恢复数据，当前数据未修改。请检查磁盘空间与权限。"
		if s.gateway.RestorePending() {
			text = "恢复未完成，已合并的用量会保留，现有请求不会因恢复任务被停用。请检查磁盘空间与权限并重启服务，任务将幂等继续。"
		}
		abortError(c, http.StatusInternalServerError, "restore_failed", text)
		return
	}
	c.JSON(http.StatusOK, gin.H{"restored": true, "usage_only": usageOnly, "snapshot_at": snapshot.CreatedAt, "users": len(snapshot.Config.State.Users), "api_keys": len(snapshot.Config.State.APIKeys), "upstreams": len(snapshot.Config.State.Upstreams), "usage_requests": len(snapshot.Usage.Requests)})
}

// Never disable inference for a migration job. Only configuration mutations are
// held off while a redo journal is pending, so recovery cannot discard new edits.
func (s *Server) guardPendingRestore(c *gin.Context) {
	if !s.gateway.RestorePending() || c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
		return
	}
	path := strings.TrimPrefix(c.Request.URL.Path, "/api/admin/")
	for _, prefix := range []string{"users", "keys", "upstreams", "settings"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			abortError(c, http.StatusConflict, "restore_pending", "恢复任务尚未完成，暂不能修改配置；API 请求和查询不受此限制。")
			return
		}
	}
}
