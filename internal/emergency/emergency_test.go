package emergency

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/events"
)

func TestNewRecordBuildsStableIDAndFields(t *testing.T) {
	now := time.Date(2026, 4, 30, 16, 7, 1, 0, time.FixedZone("PDT", -7*60*60))
	rec, err := NewRecord(RecordOptions{
		Severity:   SeverityCritical,
		Actor:      "gascity/agent-a",
		Message:    "bd update failed",
		RefBead:    "ga-51t",
		SourcePath: "/city/rig",
		SourcePID:  1234,
		Hostname:   "host-a",
		Metadata:   map[string]string{"trigger": "dolt-down"},
		Now:        func() time.Time { return now },
		Random:     bytes.NewReader([]byte{0x7e, 0x3f, 0x9c, 0x12}),
	})
	if err != nil {
		t.Fatalf("NewRecord: %v", err)
	}

	if rec.ID != "20260430T230701Z-7e3f9c12" {
		t.Fatalf("ID = %q, want UTC compact timestamp plus random suffix", rec.ID)
	}
	if rec.CreatedAt.Location() != time.UTC || !rec.CreatedAt.Equal(now.UTC()) {
		t.Fatalf("CreatedAt = %v, want UTC %v", rec.CreatedAt, now.UTC())
	}
	if rec.Severity != SeverityCritical || rec.Actor != "gascity/agent-a" || rec.Message != "bd update failed" {
		t.Fatalf("record fields = %+v", rec)
	}
	if rec.RefBead != "ga-51t" || rec.SourcePath != "/city/rig" || rec.SourcePID != 1234 || rec.Hostname != "host-a" {
		t.Fatalf("record source fields = %+v", rec)
	}
	if rec.Metadata["trigger"] != "dolt-down" {
		t.Fatalf("metadata = %#v, want trigger=dolt-down", rec.Metadata)
	}
}

func TestNewRecordRejectsInvalidSeverityAndOversizeMessage(t *testing.T) {
	if _, err := NewRecord(RecordOptions{
		Severity: "fatal",
		Actor:    "human",
		Message:  "bad",
	}); err == nil || !strings.Contains(err.Error(), "severity") {
		t.Fatalf("invalid severity error = %v, want severity error", err)
	}

	if _, err := NewRecord(RecordOptions{
		Severity: SeverityError,
		Actor:    "human",
		Message:  strings.Repeat("x", MaxMessageBytes+1),
	}); err == nil || !strings.Contains(err.Error(), "4 KiB") {
		t.Fatalf("oversize message error = %v, want 4 KiB cap error", err)
	}
}

func TestWriteSpoolAtomicJSONPermissions(t *testing.T) {
	cityPath := t.TempDir()
	rec := Record{
		ID:        "20260430T160701Z-7e3f9c12",
		Severity:  SeverityError,
		Actor:     "human",
		Message:   "dolt down",
		CreatedAt: time.Date(2026, 4, 30, 16, 7, 1, 0, time.UTC),
	}

	path, err := WriteSpool(cityPath, rec)
	if err != nil {
		t.Fatalf("WriteSpool: %v", err)
	}
	wantPath := filepath.Join(cityPath, ".gc", "emergency", rec.ID+".json")
	if path != wantPath {
		t.Fatalf("path = %q, want %q", path, wantPath)
	}
	if fi, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("stat spool dir: %v", err)
	} else if fi.Mode().Perm() != 0o700 {
		t.Fatalf("spool dir mode = %o, want 0700", fi.Mode().Perm())
	}
	if fi, err := os.Stat(path); err != nil {
		t.Fatalf("stat spool file: %v", err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Fatalf("spool file mode = %o, want 0600", fi.Mode().Perm())
	}
	tmpMatches, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp"))
	if err != nil {
		t.Fatalf("glob tmp files: %v", err)
	}
	if len(tmpMatches) != 0 {
		t.Fatalf("tmp files left behind: %v", tmpMatches)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	var got Record
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal spool: %v", err)
	}
	if got.ID != rec.ID || got.Message != rec.Message {
		t.Fatalf("spool record = %+v, want %+v", got, rec)
	}
}

func TestWriteSpoolDoesNotOverwriteExistingRecord(t *testing.T) {
	cityPath := t.TempDir()
	rec := Record{
		ID:        "20260430T160701Z-7e3f9c12",
		Severity:  SeverityError,
		Actor:     "human",
		Message:   "original",
		CreatedAt: time.Date(2026, 4, 30, 16, 7, 1, 0, time.UTC),
	}
	path, err := WriteSpool(cityPath, rec)
	if err != nil {
		t.Fatalf("WriteSpool original: %v", err)
	}

	duplicate := rec
	duplicate.Message = "replacement"
	if _, err := WriteSpool(cityPath, duplicate); err == nil || !strings.Contains(err.Error(), "record already exists") {
		t.Fatalf("WriteSpool duplicate error = %v, want record already exists", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original spool: %v", err)
	}
	if strings.Contains(string(data), "replacement") {
		t.Fatalf("duplicate write replaced existing spool record: %s", data)
	}
}

func TestRecordSignaledMirrorsTypedPayload(t *testing.T) {
	rec := Record{
		ID:         "20260430T160701Z-7e3f9c12",
		Severity:   SeverityCritical,
		Actor:      "gascity/agent-a",
		Message:    "bd update failed",
		RefBead:    "ga-51t",
		SourcePath: "/city/rig",
		SourcePID:  1234,
		Hostname:   "host-a",
		CreatedAt:  time.Date(2026, 4, 30, 16, 7, 1, 0, time.UTC),
		Metadata:   map[string]string{"trigger": "dolt-down"},
	}
	ep := events.NewFake()

	if err := RecordSignaled(ep, rec); err != nil {
		t.Fatalf("RecordSignaled: %v", err)
	}
	evts, err := ep.List(events.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(evts) != 1 {
		t.Fatalf("events = %d, want 1", len(evts))
	}
	if evts[0].Type != events.EmergencySignaled || evts[0].Actor != rec.Actor || evts[0].Subject != rec.ID {
		t.Fatalf("event = %+v, want emergency.signaled from record actor and id", evts[0])
	}
	var payload Record
	if err := json.Unmarshal(evts[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.ID != rec.ID || payload.Metadata["trigger"] != "dolt-down" {
		t.Fatalf("payload = %+v, want record payload", payload)
	}
}

func TestNotifyDeduperAllowsFirstAndSuppressesSecond(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 30, 16, 7, 1, 0, time.UTC)
	key := NotifyDedupeKey(SeverityCritical, "dolt connection refused")

	first, err := MarkNotifyDedupe(dir, key, now, 5*time.Minute)
	if err != nil {
		t.Fatalf("first MarkNotifyDedupe: %v", err)
	}
	if !first.Fire {
		t.Fatalf("first dedupe result = %+v, want fire", first)
	}

	second, err := MarkNotifyDedupe(dir, key, now.Add(12*time.Second), 5*time.Minute)
	if err != nil {
		t.Fatalf("second MarkNotifyDedupe: %v", err)
	}
	if second.Fire {
		t.Fatalf("second dedupe result = %+v, want suppress", second)
	}
	if second.Age < 12*time.Second || second.KeyPrefix == "" {
		t.Fatalf("second dedupe details = %+v, want age and key prefix", second)
	}
}
