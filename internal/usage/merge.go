package usage

import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

var ErrIdentityConflict = errors.New("用量记录 ID 与目标实例中不同用户、Key 或请求的记录冲突")

func sameIdentity(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, r BackupRequest) error {
	var user, key, path string
	var started int64
	err := query.QueryRowContext(ctx, "SELECT user_id,key_id,path,started_at FROM requests WHERE id=?", r.ID).Scan(&user, &key, &path, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if user != r.UserID || key != r.KeyID || path != r.Path || started != r.StartedAt {
		return ErrIdentityConflict
	}
	return nil
}

// ValidateMerge detects conflicting identities before publishing a redo journal
// or adding any history. WAL readers leave active inference accounting writable.
func (s *Store) ValidateMerge(ctx context.Context, b Backup) error {
	if err := b.Validate(); err != nil {
		return err
	}
	tx, err := s.reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range b.Requests {
		if err := sameIdentity(ctx, tx, r); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const mergeRequest = `INSERT INTO requests (` + backupColumns + `,pending_sync) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET
 model=CASE WHEN excluded.completed>=requests.completed AND excluded.attempts>=requests.attempts THEN excluded.model ELSE requests.model END,
 status=CASE WHEN excluded.completed=1 OR requests.completed=0 THEN excluded.status ELSE requests.status END,
 upstream_id=CASE WHEN excluded.completed>=requests.completed AND excluded.attempts>=requests.attempts THEN excluded.upstream_id ELSE requests.upstream_id END,
 upstream_name=CASE WHEN excluded.completed>=requests.completed AND excluded.attempts>=requests.attempts THEN excluded.upstream_name ELSE requests.upstream_name END,
 attempts=MAX(requests.attempts,excluded.attempts),duration_ms=MAX(requests.duration_ms,excluded.duration_ms),
 input_tokens=MAX(requests.input_tokens,excluded.input_tokens),output_tokens=MAX(requests.output_tokens,excluded.output_tokens),
 cached_tokens=MAX(requests.cached_tokens,excluded.cached_tokens),reasoning_tokens=MAX(requests.reasoning_tokens,excluded.reasoning_tokens),
 usage_known=MAX(requests.usage_known,excluded.usage_known),completed=MAX(requests.completed,excluded.completed),
 pending_sync=CASE WHEN MAX(requests.completed,excluded.completed)=0 THEN 1 ELSE 0 END
 WHERE requests.completed=1 OR requests.pending_sync=1`

// Merge adds history in bounded transactions. It never deletes target records,
// never overwrites a locally active request, and never sums duplicate counters.
// A durable gateway journal makes interruption/replay of these batches safe.
func (s *Store) Merge(ctx context.Context, b Backup) error {
	if err := b.Validate(); err != nil {
		return err
	}
	const batchSize = 64
	for start := 0; start < len(b.Requests); start += batchSize {
		batch := b.Requests[start:min(start+batchSize, len(b.Requests))]
		if err := s.mergeBatch(ctx, func(tx *sql.Tx) error {
			stmt, err := tx.PrepareContext(ctx, mergeRequest)
			if err != nil {
				return err
			}
			defer stmt.Close()
			for _, r := range batch {
				if err := sameIdentity(ctx, tx, r); err != nil {
					return err
				}
				if _, err := stmt.ExecContext(ctx, r.ID, r.StartedAt, r.UserID, r.UserName, r.KeyID, r.KeyName, r.Model, r.Path, r.Status, r.Attempts, r.DurationMS, r.Completed, r.InputTokens, r.OutputTokens, r.CachedTokens, r.ReasoningTokens, r.UsageKnown, r.UpstreamID, r.UpstreamName, !r.Completed); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	scopes := make([]string, 0, len(b.Links))
	for scope := range b.Links {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	for start := 0; start < len(scopes); start += batchSize {
		batch := scopes[start:min(start+batchSize, len(scopes))]
		if err := s.mergeBatch(ctx, func(tx *sql.Tx) error {
			for _, scope := range batch {
				if _, err := tx.ExecContext(ctx, "INSERT INTO response_links(scope,request_id) VALUES(?,?) ON CONFLICT(scope) DO NOTHING", scope, b.Links[scope]); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) mergeBatch(ctx context.Context, apply func(*sql.Tx) error) error {
	s.coordinator.RLock()
	defer s.coordinator.RUnlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := apply(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}
