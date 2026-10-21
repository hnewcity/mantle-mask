package usage

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func requireMySQL(t *testing.T) {
	t.Helper()
	if testMySQLDSN == "" {
		t.Skip("set MANTLE_TEST_MYSQL_DSN to run MySQL tests")
	}
}

func TestMySQLImportReplacesUsageOnlyWhenRequested(t *testing.T) {
	requireMySQL(t)
	ctx := context.Background()
	dir := t.TempDir()
	dsn := testDSN(t, dir)
	open := func(importSQLite bool) *Store {
		t.Helper()
		s, err := OpenStore(dir, Options{MySQLDSN: dsn, ImportSQLite: importSQLite})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	// Without a usage.db, an import must fail instead of emptying MySQL.
	s := open(false)
	stale := Request{ID: "mysql-only", StartedAt: time.Now(), Status: 200}
	for _, err := range []error{s.Begin(stale), s.Bind("mysql-scope", stale.ID), s.Finish(stale)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenStore(dir, Options{MySQLDSN: dsn, ImportSQLite: true}); err == nil {
		s.Close()
		t.Fatal("import without usage.db accepted")
	}

	legacy, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := Request{ID: "legacy", StartedAt: time.Now(), UserID: "alice", KeyID: "k1", Model: "Model ", Path: "/v1/responses", UpstreamID: "up", UpstreamName: "上游", Attempts: 1, Status: 200}
	for _, err := range []error{legacy.Begin(r), legacy.Attempt(r), legacy.Tokens(r.ID, Counters{InputTokens: 10, OutputTokens: 5}), legacy.Bind("scope", r.ID), legacy.Finish(r)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	want, err := legacy.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	// Not requested: MySQL keeps its own rows even though usage.db exists.
	s = open(false)
	kept, err := s.Export(ctx)
	if err != nil || len(kept.Requests) != 1 || kept.Requests[0].ID != "mysql-only" {
		t.Fatalf("usage imported without the setting: %+v %v", kept, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Requested: MySQL becomes an exact copy of SQLite; its own rows are gone.
	for range 2 {
		s = open(true)
		got, err := s.Export(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("imported history differs:\ngot  %+v\nwant %+v", got, want)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMySQLRejectsSecondInstanceAndKeepsExactIDs(t *testing.T) {
	requireMySQL(t)
	dir := t.TempDir()
	s, err := openForTest(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := openForTest(t, dir); err == nil {
		second.Close()
		s.Close()
		t.Fatal("second instance shared the database")
	}
	// Binary columns must not fold case or trailing spaces like _ci collations.
	for _, id := range []string{"r", "R", "r "} {
		r := Request{ID: id, UserID: id, StartedAt: time.Now(), Model: id, Attempts: 1, Status: 200}
		if err := s.Begin(r); err != nil {
			t.Fatal(err)
		}
		if err := s.Finish(r); err != nil {
			t.Fatal(err)
		}
	}
	report, err := s.Report(context.Background(), Filter{All: true, UserID: "r"})
	if err != nil || report.Totals.Requests != 1 {
		t.Fatalf("exact filter: %+v %v", report.Totals, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Closing releases the lock, so a restart can reopen the same database.
	s, err = openForTest(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
