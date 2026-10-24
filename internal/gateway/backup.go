package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hnewcity/mantle-mask/internal/backup"
	"github.com/hnewcity/mantle-mask/internal/config"
	"github.com/hnewcity/mantle-mask/internal/usage"
)

// Backup includes all persistent application state, not ephemeral health/cache/logs.
// Snapshot pinning coordinates only short state mutations, not request lifetimes.
type Backup struct {
	Format    string               `json:"format"`
	Version   int                  `json:"version"`
	CreatedAt time.Time            `json:"created_at"`
	Config    config.PortableState `json:"config"`
	Usage     usage.Backup         `json:"usage"`
	Affinity  map[string]Binding   `json:"affinity"`
}

type restoreJournal struct {
	UsageOnly bool               `json:"usage_only,omitempty"`
	Version   int                `json:"version"`
	Config    config.State       `json:"config"`
	Usage     usage.Backup       `json:"usage"`
	Affinity  map[string]Binding `json:"affinity"`
}

func (g *Gateway) ExportBackup(ctx context.Context) (Backup, error) {
	out, view, err := g.pinBackup(ctx)
	if err != nil {
		return out, err
	}
	defer view.Close()
	// WAL retains the pinned database view while new requests keep writing.
	out.Usage, err = view.Export(ctx)
	return out, err
}

// ExportBackupArchive encrypts the same document as json.Marshal(ExportBackup)
// without materializing the usage history as Go values or a separate
// plaintext buffer; the archive is the only history-sized allocation.
func (g *Gateway) ExportBackupArchive(ctx context.Context, password string) ([]byte, error) {
	out, view, err := g.pinBackup(ctx)
	if err != nil {
		return nil, err
	}
	defer view.Close()
	head := struct {
		Format    string               `json:"format"`
		Version   int                  `json:"version"`
		CreatedAt time.Time            `json:"created_at"`
		Config    config.PortableState `json:"config"`
	}{out.Format, out.Version, out.CreatedAt, out.Config}
	return backup.EncryptStream(func(w io.Writer) error {
		return writeWithUsage(w, head, out.Affinity, func(w io.Writer) error { return view.WriteJSON(ctx, w) })
	}, password)
}

// writeWithUsage writes head's members followed by "usage" and "affinity",
// matching the field order and encoding of json.Marshal on Backup and
// restoreJournal. head must encode as a non-empty object.
func writeWithUsage(w io.Writer, headValue any, affinityValue map[string]Binding, usageJSON func(io.Writer) error) error {
	head, err := json.Marshal(headValue)
	if err != nil {
		return err
	}
	if _, err := w.Write(head[:len(head)-1]); err != nil {
		return err
	}
	if _, err := io.WriteString(w, `,"usage":`); err != nil {
		return err
	}
	if err := usageJSON(w); err != nil {
		return err
	}
	affinity, err := json.Marshal(affinityValue)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, `,"affinity":`); err != nil {
		return err
	}
	if _, err := w.Write(affinity); err != nil {
		return err
	}
	_, err = io.WriteString(w, "}")
	return err
}

// pinBackup captures configuration and routing and pins the usage view at one
// checkpoint. The caller closes the returned snapshot.
func (g *Gateway) pinBackup(ctx context.Context) (Backup, *usage.Snapshot, error) {
	out := Backup{Format: "mantle-mask", Version: 2}
	// Reserve a reader outside the checkpoint; a busy reporting pool must not
	// hold up active accounting while waiting for a connection.
	view, err := g.usage.PrepareSnapshot(ctx)
	if err != nil {
		return out, nil, err
	}
	err = func() error {
		gate := g.store.Coordinator()
		gate.Lock()
		defer gate.Unlock()
		if err := view.Pin(ctx); err != nil {
			return err
		}
		out.CreatedAt = time.Now().UTC()
		var err error
		if out.Config, err = g.store.Export(); err != nil {
			return err
		}
		g.affinity.mu.Lock()
		defer g.affinity.mu.Unlock()
		out.Affinity = make(map[string]Binding, len(g.affinity.bindings))
		for k, v := range g.affinity.bindings {
			out.Affinity[k] = v
		}
		return nil
	}()
	if err != nil {
		view.Close()
		return out, nil, err
	}
	return out, view, nil
}

func DecodeBackup(data []byte) (Backup, error) {
	var out Backup
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	// Walk the document instead of Decode(&out): a Decoder buffers each value
	// it decodes in full, which for the usage history is the whole archive.
	err := backup.DecodeObject(decoder, func(name string) error {
		switch {
		case strings.EqualFold(name, "format"):
			return decoder.Decode(&out.Format)
		case strings.EqualFold(name, "version"):
			return decoder.Decode(&out.Version)
		case strings.EqualFold(name, "created_at"):
			return decoder.Decode(&out.CreatedAt)
		case strings.EqualFold(name, "config"):
			return decoder.Decode(&out.Config)
		case strings.EqualFold(name, "usage"):
			var err error
			out.Usage, err = usage.DecodeBackupJSON(decoder)
			return err
		case strings.EqualFold(name, "affinity"):
			out.Affinity = nil
			return decoder.Decode(&out.Affinity)
		}
		return errors.New("unknown field")
	})
	if err != nil {
		return out, errors.New("备份内容无效或包含不受支持的字段")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return out, errors.New("备份只能包含一个完整对象")
	}
	return out, nil
}

// decodeJournal is DecodeBackup for the restore journal, read from disk.
func decodeJournal(r io.Reader) (restoreJournal, error) {
	var out restoreJournal
	decoder := json.NewDecoder(r)
	err := backup.DecodeObject(decoder, func(name string) error {
		switch {
		case strings.EqualFold(name, "usage_only"):
			return decoder.Decode(&out.UsageOnly)
		case strings.EqualFold(name, "version"):
			return decoder.Decode(&out.Version)
		case strings.EqualFold(name, "config"):
			return decoder.Decode(&out.Config)
		case strings.EqualFold(name, "usage"):
			var err error
			out.Usage, err = usage.DecodeBackupJSON(decoder)
			return err
		case strings.EqualFold(name, "affinity"):
			out.Affinity = nil
			return decoder.Decode(&out.Affinity)
		}
		// Journals written by this program have no other fields.
		return decoder.Decode(new(json.RawMessage))
	})
	return out, err
}

func (g *Gateway) PrepareBackup(in Backup, usageOnly ...bool) (restoreJournal, error) {
	var out restoreJournal
	if in.Format != "mantle-mask" || (in.Version != 1 && in.Version != 2) || in.CreatedAt.IsZero() || in.Affinity == nil || len(in.Affinity) > 100000 {
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
	if err := g.usage.ValidateMerge(context.Background(), in.Usage); err != nil {
		return out, err
	}
	g.affinity.mu.Lock()
	count := len(g.affinity.bindings)
	for scope := range in.Affinity {
		if _, ok := g.affinity.bindings[scope]; !ok {
			count++
		}
	}
	g.affinity.mu.Unlock()
	if count > 100000 {
		return out, errors.New("合并后的 Responses 路由超过容量限制")
	}
	return restoreJournal{Version: 2, Config: st, Usage: in.Usage, Affinity: in.Affinity, UsageOnly: len(usageOnly) > 0 && usageOnly[0]}, nil
}

func (g *Gateway) restorePath() string {
	return filepath.Join(filepath.Dir(g.affinity.path), "restore-pending.json")
}
func (g *Gateway) RestorePending() bool { return g.restorePending.Load() }

// RestoreBackup journals the already-validated target state before changing any
// live data. A failed/interrupted application is replayed idempotently on startup;
// failed jobs remain resumable, without disabling the live request path.
func (g *Gateway) RestoreBackup(in restoreJournal) error {
	// Streamed: json.Encoder would first build the whole history in memory.
	if err := config.AtomicWrite(g.restorePath(), func(w io.Writer) error {
		head := struct {
			UsageOnly bool         `json:"usage_only,omitempty"`
			Version   int          `json:"version"`
			Config    config.State `json:"config"`
		}{in.UsageOnly, in.Version, in.Config}
		return writeWithUsage(w, head, in.Affinity, func(w io.Writer) error { return usage.WriteBackupJSON(w, in.Usage) })
	}); err != nil {
		return err
	}
	g.restorePending.Store(true)
	if err := g.syncRestoreDirectory(); err != nil {
		return err
	}
	return g.applyRestore(in)
}

func (g *Gateway) recoverRestore() error {
	f, err := os.Open(g.restorePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	in, err := decodeJournal(bufio.NewReaderSize(f, 64<<10))
	f.Close()
	if err != nil {
		return err
	}
	if (in.Version != 1 && in.Version != 2) || in.Config.Version != 2 || in.Affinity == nil {
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
	if _, err := g.store.PrepareImport(portable); err != nil {
		return err
	}
	g.restorePending.Store(true)
	return g.applyRestore(in)
}

func (g *Gateway) applyRestore(in restoreJournal) error {
	// Once journaled, a disconnected browser must not abandon a partially applied job.
	ctx := context.Background()
	if in.Version == 1 {
		// Resume backups written by the old, stop-the-world implementation at startup.
		if err := g.usage.Restore(ctx, in.Usage); err != nil {
			return err
		}
		if err := g.store.RestoreWith(in.Config, func() error {
			g.affinity.mu.Lock()
			defer g.affinity.mu.Unlock()
			return g.affinity.replaceLocked(in.Affinity)
		}); err != nil {
			return err
		}
	} else {
		if err := g.usage.Merge(ctx, in.Usage); err != nil {
			return err
		}
		if in.UsageOnly {
			gate := g.store.Coordinator()
			gate.Lock()
			err := g.mergeAffinity(in.Affinity)
			gate.Unlock()
			if err != nil {
				return err
			}
		} else if err := g.store.RestoreWith(in.Config, func() error { return g.mergeAffinity(in.Affinity) }); err != nil {
			return err
		}
	}
	if err := g.syncRestoreDirectory(); err != nil {
		return err
	}
	if err := os.Remove(g.restorePath()); err != nil {
		return err
	}
	if err := g.syncRestoreDirectory(); err != nil {
		return err
	}
	// Keep runtime/provider objects alive: active streams still use them. Caches
	// already include endpoint/credential fingerprints, so new config is isolated.
	g.restorePending.Store(false)
	return nil
}

// Caller holds the checkpoint. Existing bindings win conflicts, just like usage
// links, so importing history cannot redirect a response created on this target.
func (g *Gateway) mergeAffinity(in map[string]Binding) error {
	g.affinity.mu.Lock()
	defer g.affinity.mu.Unlock()
	merged := make(map[string]Binding, len(g.affinity.bindings)+len(in))
	for k, v := range in {
		merged[k] = v
	}
	for k, v := range g.affinity.bindings {
		merged[k] = v
	}
	if len(merged) > 100000 {
		return errors.New("合并后的 Responses 路由超过容量限制")
	}
	return g.affinity.replaceLocked(merged)
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
