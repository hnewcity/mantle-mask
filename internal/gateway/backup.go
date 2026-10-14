package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/hnewcity/mantle-mask/internal/config"
	"github.com/hnewcity/mantle-mask/internal/usage"
)

// Backup includes all persistent application state, not ephemeral health/cache/logs.
// The HTTP layer must hold its exclusive migration lock while taking/applying it.
type Backup struct {
	Format    string               `json:"format"`
	Version   int                  `json:"version"`
	CreatedAt time.Time            `json:"created_at"`
	Config    config.PortableState `json:"config"`
	Usage     usage.Backup         `json:"usage"`
	Affinity  map[string]Binding   `json:"affinity"`
}

type restoreJournal struct {
	Version  int                `json:"version"`
	Config   config.State       `json:"config"`
	Usage    usage.Backup       `json:"usage"`
	Affinity map[string]Binding `json:"affinity"`
}

func (g *Gateway) ExportBackup(ctx context.Context) (Backup, error) {
	out := Backup{Format: "mantle-mask", Version: 1, CreatedAt: time.Now().UTC()}
	var err error
	if out.Config, err = g.store.Export(); err != nil {
		return out, err
	}
	if out.Usage, err = g.usage.Export(ctx); err != nil {
		return out, err
	}
	g.affinity.mu.Lock()
	defer g.affinity.mu.Unlock()
	out.Affinity = make(map[string]Binding, len(g.affinity.bindings))
	for k, v := range g.affinity.bindings {
		out.Affinity[k] = v
	}
	return out, nil
}

func DecodeBackup(data []byte) (Backup, error) {
	var out Backup
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return out, errors.New("备份内容无效或包含不受支持的字段")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return out, errors.New("备份只能包含一个完整对象")
	}
	return out, nil
}

func (g *Gateway) PrepareBackup(in Backup) (restoreJournal, error) {
	var out restoreJournal
	if in.Format != "mantle-mask" || in.Version != 1 || in.CreatedAt.IsZero() || in.Affinity == nil || len(in.Affinity) > 100000 {
		return out, errors.New("备份版本不受支持或缺少必要数据")
	}
	if err := in.Usage.Validate(); err != nil {
		return out, err
	}
	for scope, b := range in.Affinity {
		// Bindings to removed upstreams are valid historical records and remain inert.
		if len(scope) != 64 || len(b.Fingerprint) != 64 || b.UpstreamID == "" || b.Expires.IsZero() {
			return out, errors.New("备份包含无效的 Responses 路由绑定")
		}
	}
	st, err := g.store.PrepareImport(in.Config)
	if err != nil {
		return out, err
	}
	return restoreJournal{Version: 1, Config: st, Usage: in.Usage, Affinity: in.Affinity}, nil
}

func (g *Gateway) restorePath() string {
	return filepath.Join(filepath.Dir(g.affinity.path), "restore-pending.json")
}
func (g *Gateway) RestorePending() bool { return g.restorePending.Load() }

// RestoreBackup journals the already-validated target state before changing any
// live data. A failed/interrupted application is replayed idempotently on startup;
// until then the HTTP server refuses all traffic rather than serve mixed state.
func (g *Gateway) RestoreBackup(in restoreJournal) error {
	if err := config.AtomicJSON(g.restorePath(), in); err != nil {
		return err
	}
	g.restorePending.Store(true)
	if err := g.syncRestoreDirectory(); err != nil {
		return err
	}
	return g.applyRestore(in)
}

func (g *Gateway) recoverRestore() error {
	data, err := os.ReadFile(g.restorePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var in restoreJournal
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	if in.Version != 1 || in.Config.Version != 2 || in.Affinity == nil {
		return errors.New("invalid pending restore journal")
	}
	// Verify local journal credentials before writing any additional state.
	portable := config.PortableState{State: in.Config, Credentials: map[string]config.Credentials{}}
	portable.State.Upstreams = append([]config.Upstream{}, in.Config.Upstreams...)
	for i, u := range portable.State.Upstreams {
		c, err := g.store.Credentials(u)
		if err != nil {
			return err
		}
		portable.Credentials[u.ID] = c
		portable.State.Upstreams[i].Secret = ""
	}
	if _, err := g.PrepareBackup(Backup{Format: "mantle-mask", Version: 1, CreatedAt: time.Now().UTC(), Config: portable, Usage: in.Usage, Affinity: in.Affinity}); err != nil {
		return err
	}
	g.restorePending.Store(true)
	return g.applyRestore(in)
}

func (g *Gateway) applyRestore(in restoreJournal) error {
	// Do not inherit HTTP cancellation once the durable journal is committed.
	if err := g.usage.Restore(context.Background(), in.Usage); err != nil {
		return err
	}
	if err := g.store.Restore(in.Config); err != nil {
		return err
	}
	g.affinity.mu.Lock()
	err := config.AtomicJSON(g.affinity.path, in.Affinity)
	if err == nil {
		g.affinity.bindings = in.Affinity
	}
	g.affinity.mu.Unlock()
	if err != nil {
		return err
	}
	// Persist both JSON renames before removing the redo journal.
	if err := g.syncRestoreDirectory(); err != nil {
		return err
	}
	if err := os.Remove(g.restorePath()); err != nil {
		return err
	}
	if err := g.syncRestoreDirectory(); err != nil {
		return err
	}
	g.runtime = newRuntime()
	g.credentialMu.Lock()
	g.providers = map[string]aws.CredentialsProvider{}
	g.credentialMu.Unlock()
	g.client.CloseIdleConnections()
	g.restorePending.Store(false)
	return nil
}

// Linux directory fsync makes journal publication/removal durable across power
// loss as well as process crashes. Windows does not expose directory fsync via
// os.File; the synced-file/rename journal still covers process interruptions.
func (g *Gateway) syncRestoreDirectory() error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(filepath.Dir(g.restorePath()))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
