package usage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

// mysqlMergeRequest mirrors mergeRequest. MySQL has no conditional upsert, so
// every assignment repeats the "completed or pending" guard. Assignments run
// left to right and see earlier results: model, status and upstream fields
// read the original attempts/completed, and pending_sync reads the merged
// completed value. Keep that order when editing.
const mysqlMergeRequest = `INSERT INTO requests (` + backupColumns + `,pending_sync) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) AS excluded
 ON DUPLICATE KEY UPDATE
 model=IF(` + mysqlMergeGuard + ` AND excluded.completed>=requests.completed AND excluded.attempts>=requests.attempts, excluded.model, requests.model),
 status=IF(` + mysqlMergeGuard + ` AND (excluded.completed=1 OR requests.completed=0), excluded.status, requests.status),
 upstream_id=IF(` + mysqlMergeGuard + ` AND excluded.completed>=requests.completed AND excluded.attempts>=requests.attempts, excluded.upstream_id, requests.upstream_id),
 upstream_name=IF(` + mysqlMergeGuard + ` AND excluded.completed>=requests.completed AND excluded.attempts>=requests.attempts, excluded.upstream_name, requests.upstream_name),
 attempts=IF(` + mysqlMergeGuard + `, GREATEST(requests.attempts,excluded.attempts), requests.attempts),
 duration_ms=IF(` + mysqlMergeGuard + `, GREATEST(requests.duration_ms,excluded.duration_ms), requests.duration_ms),
 input_tokens=IF(` + mysqlMergeGuard + `, GREATEST(requests.input_tokens,excluded.input_tokens), requests.input_tokens),
 output_tokens=IF(` + mysqlMergeGuard + `, GREATEST(requests.output_tokens,excluded.output_tokens), requests.output_tokens),
 cached_tokens=IF(` + mysqlMergeGuard + `, GREATEST(requests.cached_tokens,excluded.cached_tokens), requests.cached_tokens),
 reasoning_tokens=IF(` + mysqlMergeGuard + `, GREATEST(requests.reasoning_tokens,excluded.reasoning_tokens), requests.reasoning_tokens),
 usage_known=IF(` + mysqlMergeGuard + `, GREATEST(requests.usage_known,excluded.usage_known), requests.usage_known),
 completed=IF(` + mysqlMergeGuard + `, GREATEST(requests.completed,excluded.completed), requests.completed),
 pending_sync=IF(` + mysqlMergeGuard + `, IF(requests.completed=0,1,0), requests.pending_sync)`

const mysqlMergeGuard = "(requests.completed=1 OR requests.pending_sync=1)"

var mysqlDialect = dialect{
	mysql: true,
	// A no-op update keeps first-writer-wins without INSERT IGNORE hiding
	// foreign-key or truncation errors.
	bindLink: "INSERT INTO response_links(scope,request_id) VALUES(?,?) ON DUPLICATE KEY UPDATE scope=scope",
	tokenUpdate: `UPDATE requests SET
	input_tokens=GREATEST(input_tokens,?), output_tokens=GREATEST(output_tokens,?),
	cached_tokens=GREATEST(cached_tokens,?), reasoning_tokens=GREATEST(reasoning_tokens,?), usage_known=1 `,
	merge: mysqlMergeRequest,
}

// Binary columns keep SQLite's exact, case- and trailing-space-sensitive
// comparisons for IDs, model names and filters on any server collation.
var mysqlSchema = []string{
	`CREATE TABLE IF NOT EXISTS requests (
		id VARBINARY(255) NOT NULL PRIMARY KEY, started_at BIGINT NOT NULL,
		user_id VARBINARY(255) NOT NULL, user_name BLOB NOT NULL,
		key_id VARBINARY(255) NOT NULL, key_name BLOB NOT NULL,
		model MEDIUMBLOB NOT NULL, path BLOB NOT NULL,
		status INT NOT NULL DEFAULT 0, attempts INT NOT NULL DEFAULT 0,
		duration_ms BIGINT NOT NULL DEFAULT 0, completed TINYINT NOT NULL DEFAULT 0,
		input_tokens BIGINT NOT NULL DEFAULT 0, output_tokens BIGINT NOT NULL DEFAULT 0,
		cached_tokens BIGINT NOT NULL DEFAULT 0, reasoning_tokens BIGINT NOT NULL DEFAULT 0,
		usage_known TINYINT NOT NULL DEFAULT 0,
		upstream_id VARBINARY(255) NOT NULL DEFAULT '', upstream_name BLOB NOT NULL DEFAULT (''),
		pending_sync TINYINT NOT NULL DEFAULT 0,
		INDEX requests_date (started_at),
		INDEX requests_user_date (user_id, started_at),
		INDEX requests_key_date (key_id, started_at),
		INDEX requests_upstream_date (upstream_id, started_at)
	) ENGINE=InnoDB`,
	`CREATE TABLE IF NOT EXISTS response_links (
		scope VARBINARY(255) NOT NULL PRIMARY KEY,
		request_id VARBINARY(255) NOT NULL,
		FOREIGN KEY (request_id) REFERENCES requests(id)
	) ENGINE=InnoDB`,
	"CREATE TABLE IF NOT EXISTS schema_version (version INT NOT NULL) ENGINE=InnoDB",
}

// Options selects the usage backend. Without MySQLDSN, usage stays in
// dir/usage.db and ImportSQLite is ignored.
type Options struct {
	MySQLDSN string
	// ImportSQLite replaces all MySQL usage with dir/usage.db on this start.
	ImportSQLite bool
}

func OpenStore(dir string, opts Options) (*Store, error) {
	if opts.MySQLDSN == "" {
		return Open(dir)
	}
	s, err := OpenMySQL(opts.MySQLDSN)
	if err != nil {
		return nil, err
	}
	if opts.ImportSQLite {
		if err = s.importSQLite(dir); err != nil {
			s.Close()
			return nil, fmt.Errorf("import SQLite usage into MySQL: %w", err)
		}
	}
	return s, nil
}

// OpenMySQL connects to a go-sql-driver DSN, for example
// user:password@tcp(host:3306)/mantle_mask. A missing database is created.
// MySQL 8.0.19 or later is required for upsert row aliases.
func OpenMySQL(dsn string) (*Store, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse MySQL DSN: %w", err)
	}
	if cfg.DBName == "" {
		return nil, errors.New("MySQL DSN must name a database")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	// Never let a non-strict server silently truncate accounting data, and keep
	// backup snapshots consistent when the server default is READ COMMITTED.
	cfg.Params["sql_mode"] = "CONCAT_WS(',',NULLIF(@@sql_mode,''),'STRICT_ALL_TABLES')"
	cfg.Params["transaction_isolation"] = "'REPEATABLE-READ'"
	// One round trip per accounting write; utf8mb4 escaping is injection-safe.
	cfg.InterpolateParams = true
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	// Only create a missing database, so an account limited to an existing
	// schema needs no global CREATE privilege.
	var unknown *mysql.MySQLError
	if err = db.Ping(); errors.As(err, &unknown) && unknown.Number == 1049 {
		err = createMySQLDatabase(cfg)
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to MySQL: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	db.SetConnMaxIdleTime(5 * time.Minute)
	s := &Store{db: db, coordinator: &sync.RWMutex{}, sql: mysqlDialect}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if s.lock, err = acquireMySQLLock(ctx, db, cfg.DBName); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.initializeMySQL(ctx); err != nil {
		s.lock.release()
		db.Close()
		return nil, fmt.Errorf("initialize usage schema: %w", err)
	}
	// A separate pool keeps long reports from starving accounting writes.
	s.reader = sql.OpenDB(connector)
	s.reader.SetMaxOpenConns(4)
	s.reader.SetMaxIdleConns(4)
	s.reader.SetConnMaxIdleTime(5 * time.Minute)
	if err = s.reader.PingContext(ctx); err != nil {
		s.reader.Close()
		s.lock.release()
		db.Close()
		return nil, err
	}
	s.healthy.Store(true)
	return s, nil
}

func createMySQLDatabase(cfg *mysql.Config) error {
	server := cfg.Clone()
	server.DBName = ""
	connector, err := mysql.NewConnector(server)
	if err != nil {
		return err
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	name := strings.ReplaceAll(cfg.DBName, "`", "``")
	if _, err = db.Exec("CREATE DATABASE IF NOT EXISTS `" + name + "` CHARACTER SET utf8mb4"); err != nil {
		return fmt.Errorf("create MySQL database: %w", err)
	}
	return nil
}

func (s *Store) initializeMySQL(ctx context.Context) error {
	for _, stmt := range mysqlSchema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	var version int
	err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.ExecContext(ctx, "INSERT INTO schema_version (version) VALUES (3)")
		version = 3
	}
	if err != nil {
		return err
	}
	if version != 3 {
		return errors.New("unsupported usage database version")
	}
	// The instance lock guarantees these in-flight rows belong to a previous run.
	_, err = s.db.ExecContext(ctx, "UPDATE requests SET status=499, completed=1 WHERE completed=0 AND pending_sync=0")
	return err
}

// mysqlLock keeps a second gateway from sharing the database: startup marks
// all in-flight rows interrupted, and routes and config are still local files.
type mysqlLock struct {
	conn *sql.Conn
	name string
}

func acquireMySQLLock(ctx context.Context, db *sql.DB, database string) (*mysqlLock, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	// The held connection is otherwise idle; keep the server from expiring it.
	if _, err = conn.ExecContext(ctx, "SET SESSION wait_timeout=31536000"); err != nil {
		conn.Close()
		return nil, err
	}
	sum := sha256.Sum256([]byte(database))
	lock := &mysqlLock{conn: conn, name: "mantle-mask:" + hex.EncodeToString(sum[:16])}
	var got sql.NullInt64
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", lock.name).Scan(&got); err != nil {
		conn.Close()
		return nil, err
	}
	if got.Int64 != 1 {
		conn.Close()
		return nil, fmt.Errorf("MySQL database %q is already used by another Mantle Mask instance", database)
	}
	return lock, nil
}

func (l *mysqlLock) release() error {
	_, err := l.conn.ExecContext(context.Background(), "DO RELEASE_LOCK(?)", l.name)
	return errors.Join(err, l.conn.Close())
}

// importSQLite overwrites MySQL in one transaction; on failure MySQL keeps
// its previous rows. The SQLite file is only read.
func (s *Store) importSQLite(dir string) error {
	path := filepath.Join(dir, "usage.db")
	// Open would create an empty file and the restore would then empty MySQL.
	if _, err := os.Stat(path); err != nil {
		return err
	}
	legacy, err := Open(dir)
	if err != nil {
		return err
	}
	defer legacy.Close()
	ctx := context.Background()
	history, err := legacy.Export(ctx)
	if err != nil {
		return err
	}
	if err = s.Restore(ctx, history); err != nil {
		return err
	}
	slog.Warn("replaced MySQL usage with SQLite history; unset MANTLE_MYSQL_IMPORT_SQLITE to keep new usage on the next start",
		"path", path, "requests", len(history.Requests), "response_links", len(history.Links))
	return nil
}
