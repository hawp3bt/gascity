package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/emergency"
	"github.com/gastownhall/gascity/internal/events"
)

func TestResolveEmergencyMessageForms(t *testing.T) {
	msg, err := resolveEmergencyMessage([]string{"positional"}, "", "", strings.NewReader(""))
	if err != nil {
		t.Fatalf("resolve positional: %v", err)
	}
	if msg != "positional" {
		t.Fatalf("message = %q, want positional", msg)
	}

	msg, err = resolveEmergencyMessage(nil, "", "-", strings.NewReader("from stdin"))
	if err != nil {
		t.Fatalf("resolve stdin body-file: %v", err)
	}
	if msg != "from stdin" {
		t.Fatalf("stdin message = %q, want body", msg)
	}

	_, err = resolveEmergencyMessage([]string{"positional"}, "flag", "", strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("mixed message forms error = %v, want mutually exclusive", err)
	}
}

func TestParseEmergencyMetadataRejectsReservedAndMalformed(t *testing.T) {
	meta, err := parseEmergencyMetadata([]string{"trigger=dolt-down", "agent=gascity/builder"})
	if err != nil {
		t.Fatalf("parse metadata: %v", err)
	}
	if meta["trigger"] != "dolt-down" || meta["agent"] != "gascity/builder" {
		t.Fatalf("metadata = %#v", meta)
	}

	for _, raw := range []string{"bad", "message=override"} {
		if _, err := parseEmergencyMetadata([]string{raw}); err == nil {
			t.Fatalf("parse metadata %q succeeded, want error", raw)
		}
	}
}

func TestEmergencySendViaCLIWritesSpoolAndEvents(t *testing.T) {
	clearGCEnv(t)
	clearInheritedCityRoutingEnv(t)
	configureIsolatedRuntimeEnv(t)
	t.Setenv("GC_ALIAS", "gascity/agent-a")

	cityDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cityDir, "city.toml"), []byte("[city]\nname = \"test\"\n"), 0o644); err != nil {
		t.Fatalf("write city.toml: %v", err)
	}
	if err := os.Mkdir(filepath.Join(cityDir, ".gc"), 0o700); err != nil {
		t.Fatalf("mkdir .gc: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--city", cityDir,
		"emergency", "send",
		"--quiet",
		"--severity", "critical",
		"--ref", "bad ref",
		"--metadata", "trigger=dolt-down",
		"dolt wedged",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("gc emergency send = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	id := strings.TrimSpace(stdout.String())
	if !emergency.ValidRecordID(id) {
		t.Fatalf("stdout id = %q, want emergency record id", id)
	}
	if !strings.Contains(stderr.String(), "gc emergency send: spooled ("+id+")") {
		t.Fatalf("stderr missing spool diagnostic: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "--ref \"bad ref\" malformed; spooled without ref") {
		t.Fatalf("stderr missing malformed ref warning: %q", stderr.String())
	}

	spoolPath := filepath.Join(cityDir, ".gc", "emergency", id+".json")
	data, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	var rec emergency.Record
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("unmarshal spool: %v", err)
	}
	if rec.Actor != "gascity/agent-a" || rec.Severity != "critical" || rec.Message != "dolt wedged" {
		t.Fatalf("spool record = %+v", rec)
	}
	if rec.RefBead != "" {
		t.Fatalf("RefBead = %q, want empty after malformed --ref", rec.RefBead)
	}
	if rec.Metadata["trigger"] != "dolt-down" {
		t.Fatalf("metadata = %#v", rec.Metadata)
	}

	eventData, err := os.ReadFile(filepath.Join(cityDir, ".gc", "events.jsonl"))
	if err != nil {
		t.Fatalf("read events.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(eventData)), "\n")
	if len(lines) != 1 {
		t.Fatalf("events lines = %d, want 1; data=%s", len(lines), string(eventData))
	}
	var evt events.Event
	if err := json.Unmarshal([]byte(lines[0]), &evt); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if evt.Type != events.EmergencySignaled || evt.Actor != rec.Actor || evt.Subject != rec.ID {
		t.Fatalf("event = %+v, want emergency.signaled", evt)
	}
}

func TestEmergencySendInvalidInvocationExitsTwo(t *testing.T) {
	clearGCEnv(t)
	clearInheritedCityRoutingEnv(t)
	configureIsolatedRuntimeEnv(t)

	cityDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cityDir, "city.toml"), []byte("[city]\nname = \"test\"\n"), 0o644); err != nil {
		t.Fatalf("write city.toml: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--city", cityDir,
		"emergency", "send",
		"--message", "flag",
		"positional",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Fatalf("stderr = %q, want mutually exclusive error", stderr.String())
	}
}
