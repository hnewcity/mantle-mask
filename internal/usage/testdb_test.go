package usage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// MANTLE_TEST_MYSQL_DSN (for example root:secret@tcp(127.0.0.1:3306)/) runs
// the usage suite against MySQL. Each test directory maps to its own
// throwaway mantle_mask_test_* database, so reopening a directory simulates a
// restart against the same database.
var testMySQLDSN = os.Getenv("MANTLE_TEST_MYSQL_DSN")

func openForTest(tb testing.TB, dir string) (*Store, error) {
	tb.Helper()
	if testMySQLDSN == "" {
		return Open(dir)
	}
	return OpenStore(dir, Options{MySQLDSN: testDSN(tb, dir)})
}

func testDSN(tb testing.TB, dir string) string {
	tb.Helper()
	cfg, err := mysql.ParseDSN(testMySQLDSN)
	if err != nil {
		tb.Fatal(err)
	}
	sum := sha256.Sum256([]byte(dir))
	name := "mantle_mask_test_" + hex.EncodeToString(sum[:8])
	if cfg.DBName == "" {
		tb.Cleanup(func() { dropTestDatabase(tb, cfg, name) })
	}
	if cfg.DBName == "" || cfg.DBName == name {
		cfg.DBName = name
	}
	return cfg.FormatDSN()
}

func dropTestDatabase(tb testing.TB, cfg *mysql.Config, name string) {
	server := cfg.Clone()
	server.DBName = ""
	db, err := sql.Open("mysql", server.FormatDSN())
	if err != nil {
		tb.Error(err)
		return
	}
	defer db.Close()
	if _, err = db.Exec("DROP DATABASE IF EXISTS `" + name + "`"); err != nil {
		tb.Error(err)
	}
}
