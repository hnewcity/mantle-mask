package gateway

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hnewcity/mantle-mask/internal/config"
)

func TestAffinityJournalSurvivesRestartAndTornRecord(t *testing.T) {
	dir := t.TempDir()
	s, err := openAffinity(dir)
	if err != nil {
		t.Fatal(err)
	}
	live := Binding{UpstreamID: "up", Fingerprint: config.Hash("f"), Expires: time.Now().Add(time.Hour)}
	for i := range 10 {
		if err := s.put(fmt.Sprint("scope", i), live); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.put("expired", Binding{UpstreamID: "up", Expires: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash in the middle of the next append.
	f, err := os.OpenFile(s.journalPath(), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"scope":"torn","binding":{"upstr`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	s, err = openAffinity(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	for i := range 10 {
		if b, ok := s.get(fmt.Sprint("scope", i)); !ok || b.UpstreamID != "up" {
			t.Fatalf("lost scope%d", i)
		}
	}
	if _, ok := s.get("torn"); ok {
		t.Fatal("torn record applied")
	}
	if _, exists := s.bindings["expired"]; exists {
		t.Fatal("expired binding retained after compaction")
	}
	if info, err := os.Stat(s.journalPath()); err != nil || info.Size() != 0 {
		t.Fatal("journal not compacted on open")
	}
	if err := s.put("after", live); err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	s, err = openAffinity(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if _, ok := s.get("after"); !ok || len(s.bindings) != 11 {
		t.Fatalf("append after recovery lost: %d bindings", len(s.bindings))
	}
}

func TestAffinityJournalRejectsMidFileCorruption(t *testing.T) {
	dir := t.TempDir()
	s, err := openAffinity(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.close()
	if err := os.WriteFile(s.journalPath(), []byte("garbage\n{\"scope\":\"a\",\"binding\":{}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := openAffinity(dir); err == nil {
		t.Fatal("corruption before the final record must not be skipped")
	}
}

func TestAffinityCompactsAndPrunesAtCapacity(t *testing.T) {
	s, err := openAffinity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	expired := Binding{UpstreamID: "up", Expires: time.Now().Add(-time.Minute)}
	for i := range affinityCapacity {
		s.bindings[fmt.Sprint(i)] = expired
	}
	if err := s.put("new", Binding{UpstreamID: "up", Expires: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if len(s.bindings) != 1 {
		t.Fatalf("expired bindings not pruned at capacity: %d", len(s.bindings))
	}
	for i := range 1100 {
		if err := s.put(fmt.Sprint("live", i), Binding{UpstreamID: "up", Expires: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if s.logged >= 1100 {
		t.Fatal("journal was never compacted")
	}
}

func TestAffinityCloseFoldsJournalIntoSnapshot(t *testing.T) {
	dir := t.TempDir()
	s, err := openAffinity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.put("scope", Binding{UpstreamID: "up", Expires: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	// Older releases read only affinity.json.
	data, err := os.ReadFile(s.path)
	if err != nil || !strings.Contains(string(data), `"scope"`) {
		t.Fatalf("snapshot missing journaled binding: %s %v", data, err)
	}
	if info, err := os.Stat(s.journalPath()); err != nil || info.Size() != 0 {
		t.Fatal("journal not emptied on clean close")
	}
}

func TestBoundedReadSizeHint(t *testing.T) {
	body := strings.Repeat("x", 3<<20)
	for _, hint := range []int64{-1, 0, 10, int64(len(body)), 1 << 40} {
		data, err := boundedRead(strings.NewReader(body), 4<<20, hint)
		if err != nil || string(data) != body {
			t.Fatalf("hint %d: %d bytes %v", hint, len(data), err)
		}
	}
	if _, err := boundedRead(strings.NewReader(body), 1<<20, int64(len(body))); err == nil {
		t.Fatal("limit not enforced")
	}
	// A large declared length alone must not reserve the whole limit.
	if data, err := boundedRead(strings.NewReader("{}"), 16<<20, 16<<20); err != nil || cap(data) > 1<<20+1 {
		t.Fatalf("declared length reserved %d bytes", cap(data))
	}
}
