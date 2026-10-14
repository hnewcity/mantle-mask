package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
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
	return true
}

func (s *Server) lockMigration(c *gin.Context) bool {
	if !s.migrationMu.TryLock() {
		abortError(c, http.StatusConflict, "gateway_busy", "仍有请求正在执行，请暂停 API 流量并等待流式请求结束后重试。")
		return false
	}
	// A previous failed restore may have become pending during file upload.
	if s.gateway.RestorePending() {
		s.migrationMu.Unlock()
		abortError(c, http.StatusServiceUnavailable, "restore_pending", "恢复尚未完成，请检查磁盘空间与权限并重启服务。")
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
	if !s.lockMigration(c) {
		return
	}
	snapshot, err := func() (gateway.Backup, error) {
		defer s.migrationMu.Unlock()
		return s.gateway.ExportBackup(c.Request.Context())
	}()
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
		abortError(c, http.StatusBadRequest, "confirmation_required", "请确认覆盖当前实例的全部配置和用量。")
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
	prepared, err := s.gateway.PrepareBackup(snapshot)
	if err != nil {
		abortError(c, http.StatusBadRequest, "invalid_backup", "备份数据校验失败："+err.Error())
		return
	}
	if !s.lockMigration(c) {
		return
	}
	defer s.migrationMu.Unlock()
	if err := s.gateway.RestoreBackup(prepared); err != nil {
		slog.Error("backup restore failed", "error", err)
		text := "无法写入恢复数据，当前数据未修改。请检查磁盘空间与权限。"
		if s.gateway.RestorePending() {
			text = "恢复未完成，服务已暂停接收请求。请检查磁盘空间与权限并重启服务，恢复任务会自动继续。"
		}
		abortError(c, http.StatusInternalServerError, "restore_failed", text)
		return
	}
	c.JSON(http.StatusOK, gin.H{"restored": true, "users": len(snapshot.Config.State.Users), "api_keys": len(snapshot.Config.State.APIKeys), "upstreams": len(snapshot.Config.State.Upstreams), "usage_requests": len(snapshot.Usage.Requests)})
}
