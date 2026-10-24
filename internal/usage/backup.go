package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/hnewcity/mantle-mask/internal/backup"
)

type BackupRequest struct {
	ID         string `json:"id"`
	StartedAt  int64  `json:"started_at"`
	UserID     string `json:"user_id"`
	UserName   string `json:"user_name"`
	KeyID      string `json:"key_id"`
	KeyName    string `json:"key_name"`
	Model      string `json:"model"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	Attempts   int    `json:"attempts"`
	DurationMS int64  `json:"duration_ms"`
	Completed  bool   `json:"completed"`
	Counters
	UsageKnown   bool   `json:"usage_known"`
	UpstreamID   string `json:"upstream_id"`
	UpstreamName string `json:"upstream_name"`
}

type Backup struct {
	Requests []BackupRequest   `json:"requests"`
	Links    map[string]string `json:"response_links"`
}

const backupColumns = `id,started_at,user_id,user_name,key_id,key_name,model,path,status,attempts,duration_ms,completed,input_tokens,output_tokens,cached_tokens,reasoning_tokens,usage_known,upstream_id,upstream_name`

func (s *Store) Export(ctx context.Context) (Backup, error) {
	return s.export(ctx, backup.MaxSize)
}

type Snapshot struct{ tx *sql.Tx }

// PinSnapshot establishes the WAL view now, not on its first later SELECT.
// The gateway briefly holds the shared checkpoint while calling this method.
func (s *Store) PrepareSnapshot(ctx context.Context) (*Snapshot, error) {
	tx, err := s.reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	return &Snapshot{tx: tx}, nil
}
func (v *Snapshot) Pin(ctx context.Context) error {
	var id string
	err := v.tx.QueryRowContext(ctx, "SELECT id FROM requests LIMIT 1").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
func (v *Snapshot) Close()                                     { _ = v.tx.Rollback() }
func (v *Snapshot) Export(ctx context.Context) (Backup, error) { return v.export(ctx, backup.MaxSize) }

func (s *Store) export(ctx context.Context, limit int) (Backup, error) {
	view, err := s.PrepareSnapshot(ctx)
	if err != nil {
		return Backup{}, err
	}
	defer view.Close()
	s.coordinator.Lock()
	err = view.Pin(ctx)
	s.coordinator.Unlock()
	if err != nil {
		return Backup{}, err
	}
	return view.export(ctx, limit)
}

func (v *Snapshot) export(ctx context.Context, limit int) (Backup, error) {
	// Bound collection before materializing an arbitrarily large usage database.
	used := len(`{"requests":[],"response_links":{}}`)
	out := Backup{Requests: []BackupRequest{}, Links: map[string]string{}}
	tx := v.tx
	rows, err := tx.QueryContext(ctx, "SELECT "+backupColumns+" FROM requests ORDER BY id")
	if err != nil {
		return out, err
	}
	for rows.Next() {
		r, err := scanBackupRequest(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		encoded, err := json.Marshal(r)
		if err != nil {
			rows.Close()
			return Backup{}, err
		}
		used += len(encoded) + 1
		if used > limit {
			rows.Close()
			return Backup{}, backup.ErrTooLarge
		}
		out.Requests = append(out.Requests, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.QueryContext(ctx, "SELECT scope,request_id FROM response_links ORDER BY scope")
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var scope, id string
		if err = rows.Scan(&scope, &id); err != nil {
			rows.Close()
			return out, err
		}
		encoded, err := json.Marshal(map[string]string{scope: id})
		if err != nil {
			rows.Close()
			return Backup{}, err
		}
		used += len(encoded)
		if used > limit {
			rows.Close()
			return Backup{}, backup.ErrTooLarge
		}
		out.Links[scope] = id
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func scanBackupRequest(rows *sql.Rows) (BackupRequest, error) {
	var r BackupRequest
	err := rows.Scan(&r.ID, &r.StartedAt, &r.UserID, &r.UserName, &r.KeyID, &r.KeyName, &r.Model, &r.Path, &r.Status, &r.Attempts, &r.DurationMS, &r.Completed, &r.InputTokens, &r.OutputTokens, &r.CachedTokens, &r.ReasoningTokens, &r.UsageKnown, &r.UpstreamID, &r.UpstreamName)
	return r, err
}

// WriteJSON streams the pinned view as the JSON encoding of a Backup without
// materializing the request history. It leaves the snapshot open, so the same
// view can be streamed again with identical content.
func (v *Snapshot) WriteJSON(ctx context.Context, w io.Writer) error {
	out := &jsonWriter{w: w}
	out.raw(`{"requests":[`)
	rows, err := v.tx.QueryContext(ctx, "SELECT "+backupColumns+" FROM requests ORDER BY id")
	if err != nil {
		return err
	}
	for i := 0; rows.Next() && out.err == nil; i++ {
		r, err := scanBackupRequest(rows)
		if err != nil {
			rows.Close()
			return err
		}
		out.item(i, r)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil || out.err != nil {
		return errors.Join(out.err, err)
	}
	out.raw(`],"response_links":{`)
	rows, err = v.tx.QueryContext(ctx, "SELECT scope,request_id FROM response_links ORDER BY scope")
	if err != nil {
		return err
	}
	for i := 0; rows.Next() && out.err == nil; i++ {
		var scope, id string
		if err := rows.Scan(&scope, &id); err != nil {
			rows.Close()
			return err
		}
		out.member(i, scope, id)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil || out.err != nil {
		return errors.Join(out.err, err)
	}
	out.raw(`}}`)
	return out.err
}

// WriteBackupJSON streams an in-memory Backup with the same encoding as
// json.Marshal, but without building the whole document first.
func WriteBackupJSON(w io.Writer, b Backup) error {
	out := &jsonWriter{w: w}
	out.raw(`{"requests":[`)
	for i, r := range b.Requests {
		out.item(i, r)
	}
	out.raw(`],"response_links":{`)
	i := 0
	for scope, id := range b.Links {
		out.member(i, scope, id)
		i++
	}
	out.raw(`}}`)
	return out.err
}

// DecodeBackupJSON reads the value written by WriteJSON one record at a time,
// so a large history is never buffered as a whole document. Field names match
// case-insensitively and unknown fields are rejected, like a json.Decoder with
// DisallowUnknownFields, which the caller must set on dec.
func DecodeBackupJSON(dec *json.Decoder) (Backup, error) {
	var b Backup
	// Most columns repeat across rows; share one copy of each distinct value.
	seen := map[string]string{}
	intern := func(s *string) {
		if v, ok := seen[*s]; ok {
			*s = v
		} else if len(seen) < 1<<16 {
			seen[*s] = *s
		}
	}
	err := backup.DecodeObject(dec, func(name string) error {
		switch {
		case strings.EqualFold(name, "requests"):
			b.Requests = []BackupRequest{}
			null, err := backup.DecodeArray(dec, func() error {
				var r BackupRequest
				if err := dec.Decode(&r); err != nil {
					return err
				}
				for _, s := range []*string{&r.UserID, &r.UserName, &r.KeyID, &r.KeyName, &r.Model, &r.Path, &r.UpstreamID, &r.UpstreamName} {
					intern(s)
				}
				b.Requests = append(b.Requests, r)
				return nil
			})
			if null {
				b.Requests = nil
			}
			return err
		case strings.EqualFold(name, "response_links"):
			b.Links = nil
			return dec.Decode(&b.Links)
		default:
			return fmt.Errorf("json: unknown field %q", name)
		}
	})
	return b, err
}

// jsonWriter keeps the first write or encoding error.
type jsonWriter struct {
	w   io.Writer
	err error
}

func (j *jsonWriter) raw(s string) {
	if j.err == nil {
		_, j.err = io.WriteString(j.w, s)
	}
}

func (j *jsonWriter) value(v any) {
	if j.err == nil {
		var b []byte
		if b, j.err = json.Marshal(v); j.err == nil {
			_, j.err = j.w.Write(b)
		}
	}
}

func (j *jsonWriter) item(i int, v any) {
	if i > 0 {
		j.raw(",")
	}
	j.value(v)
}

func (j *jsonWriter) member(i int, key string, v any) {
	j.item(i, key)
	j.raw(":")
	j.value(v)
}

func (b Backup) Validate() error {
	if b.Requests == nil || b.Links == nil {
		return errors.New("missing backup usage data")
	}
	ids := map[string]bool{}
	for _, r := range b.Requests {
		if r.ID == "" || ids[r.ID] || r.Status < 0 || r.Status > 599 || r.Attempts < 0 || r.DurationMS < 0 || r.InputTokens < 0 || r.OutputTokens < 0 || r.CachedTokens < 0 || r.ReasoningTokens < 0 {
			return errors.New("invalid or duplicate backup usage record")
		}
		ids[r.ID] = true
	}
	for scope, id := range b.Links {
		if scope == "" || !ids[id] {
			return errors.New("invalid backup response usage link")
		}
	}
	return nil
}

// Restore is a single database transaction, not a copy of a potentially stale WAL file.
func (s *Store) Restore(ctx context.Context, b Backup) error {
	if err := b.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"response_links", "requests"} {
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return err
		}
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO requests ("+backupColumns+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range b.Requests {
		if _, err = stmt.ExecContext(ctx, r.ID, r.StartedAt, r.UserID, r.UserName, r.KeyID, r.KeyName, r.Model, r.Path, r.Status, r.Attempts, r.DurationMS, r.Completed, r.InputTokens, r.OutputTokens, r.CachedTokens, r.ReasoningTokens, r.UsageKnown, r.UpstreamID, r.UpstreamName); err != nil {
			return err
		}
	}
	link, err := tx.PrepareContext(ctx, "INSERT INTO response_links(scope,request_id) VALUES (?,?)")
	if err != nil {
		return err
	}
	defer link.Close()
	for scope, id := range b.Links {
		if _, err = link.ExecContext(ctx, scope, id); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.healthy.Store(true)
	return nil
}
