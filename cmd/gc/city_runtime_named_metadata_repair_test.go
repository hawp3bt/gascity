package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"strings"
	"sync"
	"testing"

	gcapi "github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

func TestMailSendThroughLiberalResolver(t *testing.T) {
	cfg := namedMetadataRepairTestConfig()
	store := beads.NewMemStore()
	broken := createBrokenNamedSessionBead(t, store, "named", "runner")

	address, err := resolveMailRecipientIdentity(t.TempDir(), cfg, store, "named")
	if err != nil {
		t.Fatalf("mail send returned: %v; want nil - liberal resolver branch did not match", err)
	}
	if address != broken.Metadata["alias"] {
		t.Fatalf("mail recipient address = %q, want %q", address, broken.Metadata["alias"])
	}
}

func TestSessionReconciler_BackfillsMissingConfiguredNamedMetadata(t *testing.T) {
	cfg := namedMetadataRepairTestConfig()
	store := &metadataBatchCountingStore{MemStore: beads.NewMemStore()}
	broken := createBrokenNamedSessionBead(t, store, "named", "runner")
	rec := &namedMetadataRepairRecorder{}
	var stderr bytes.Buffer
	cr := &CityRuntime{
		cfg:                 cfg,
		cityName:            "test-city",
		sp:                  runtime.NewFake(),
		rec:                 rec,
		sessionDrains:       newDrainTracker(),
		stdout:              io.Discard,
		stderr:              &stderr,
		standaloneCityStore: store,
	}

	cr.beadReconcileTick(context.Background(), namedMetadataRepairDesiredState(broken), nil, nil)

	got, err := store.Get(broken.ID)
	if err != nil {
		t.Fatalf("Get(%s): %v", broken.ID, err)
	}
	if got.Metadata[session.NamedSessionMetadataKey] != "true" {
		t.Fatalf("after reconcile: configured_named_session=%q, want \"true\"", got.Metadata[session.NamedSessionMetadataKey])
	}
	if got.Metadata[session.NamedSessionIdentityMetadata] != "named" {
		t.Fatalf("after reconcile: configured_named_identity=%q, want \"named\"", got.Metadata[session.NamedSessionIdentityMetadata])
	}
	if got.Metadata[session.NamedSessionModeMetadata] != "on_demand" {
		t.Fatalf("after reconcile: configured_named_mode=%q, want %q", got.Metadata[session.NamedSessionModeMetadata], "on_demand")
	}
	if got.Status == "closed" {
		t.Fatalf("after reconcile: repaired named session bead was closed")
	}
	eventsOfType := rec.metadataRepairedEvents()
	if len(eventsOfType) != 1 {
		t.Fatalf("event spy: got %d session.metadata_repaired events, want 1; payload diff:\n%v", len(eventsOfType), eventsOfType)
	}
	if eventsOfType[0].Actor != "gc" {
		t.Fatalf("event actor = %q, want gc", eventsOfType[0].Actor)
	}
	if eventsOfType[0].Subject != broken.ID {
		t.Fatalf("event subject = %q, want %q", eventsOfType[0].Subject, broken.ID)
	}
	var payload gcapi.SessionMetadataRepairedPayload
	if err := json.Unmarshal(eventsOfType[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal session.metadata_repaired payload: %v", err)
	}
	wantPayload := gcapi.SessionMetadataRepairedPayload{
		BeadID:                  broken.ID,
		ConfiguredNamedIdentity: "named",
		ConfiguredNamedMode:     "on_demand",
		Reason:                  gcapi.SessionMetadataRepairReasonMissingMetadata,
	}
	if payload != wantPayload {
		t.Fatalf("event spy: got 1 session.metadata_repaired events, want 1; payload diff:\ngot  %+v\nwant %+v", payload, wantPayload)
	}

	writesAfterFirst := store.successfulWrites()
	eventsAfterFirst := len(rec.metadataRepairedEvents())
	cr.beadReconcileTick(context.Background(), namedMetadataRepairDesiredState(broken), nil, nil)
	if got := len(rec.metadataRepairedEvents()); got != eventsAfterFirst {
		t.Fatalf("second cycle was not idempotent: got %d new events, want 0", got-eventsAfterFirst)
	}
	if got := store.successfulWrites(); got != writesAfterFirst {
		t.Fatalf("second cycle wrote metadata again: writes=%d, want %d", got, writesAfterFirst)
	}
}

func TestSessionReconciler_BackfillIsBestEffort(t *testing.T) {
	cfg := namedMetadataRepairTestConfig()
	store := &metadataBatchCountingStore{
		MemStore: beads.NewMemStore(),
		err:      errors.New("synthetic"),
	}
	broken := createBrokenNamedSessionBead(t, store, "named", "runner")
	snapshot, err := loadSessionBeadSnapshot(store)
	if err != nil {
		t.Fatalf("loadSessionBeadSnapshot: %v", err)
	}
	rec := &namedMetadataRepairRecorder{}
	var stderr bytes.Buffer
	cr := &CityRuntime{
		cfg:                 cfg,
		cityName:            "test-city",
		rec:                 rec,
		stderr:              &stderr,
		standaloneCityStore: store,
	}

	repaired, err := cr.repairConfiguredNamedSessionMetadata(context.Background(), snapshot)
	if err == nil {
		t.Fatal("repairConfiguredNamedSessionMetadata error = nil, want synthetic error")
	}
	if repaired != 0 {
		t.Fatalf("repairConfiguredNamedSessionMetadata repaired = %d, want 0", repaired)
	}
	if log := stderr.String(); !strings.Contains(log, broken.ID) || !strings.Contains(log, "synthetic") {
		t.Fatalf("stderr missing repair failure log: %q", log)
	}
	if got := len(rec.metadataRepairedEvents()); got != 0 {
		t.Fatalf("event spy: got %d session.metadata_repaired events on store failure, want 0", got)
	}
	got, err := store.Get(broken.ID)
	if err != nil {
		t.Fatalf("Get(%s): %v", broken.ID, err)
	}
	if got.Metadata[session.NamedSessionMetadataKey] != "" {
		t.Fatalf("metadata changed on failure: configured_named_session=%q", got.Metadata[session.NamedSessionMetadataKey])
	}
}

func TestSessionReconciler_DoesNotBackfillAdHocBeads(t *testing.T) {
	cfg := namedMetadataRepairTestConfig()
	store := &metadataBatchCountingStore{MemStore: beads.NewMemStore()}
	adhoc := createBrokenNamedSessionBead(t, store, "adhoc-thing", "adhoc-thing")
	before := maps.Clone(adhoc.Metadata)
	snapshot, err := loadSessionBeadSnapshot(store)
	if err != nil {
		t.Fatalf("loadSessionBeadSnapshot: %v", err)
	}
	rec := &namedMetadataRepairRecorder{}
	cr := &CityRuntime{
		cfg:                 cfg,
		cityName:            "test-city",
		rec:                 rec,
		stderr:              &bytes.Buffer{},
		standaloneCityStore: store,
	}

	repaired, err := cr.repairConfiguredNamedSessionMetadata(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("repairConfiguredNamedSessionMetadata: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("repairConfiguredNamedSessionMetadata repaired = %d, want 0", repaired)
	}
	got, err := store.Get(adhoc.ID)
	if err != nil {
		t.Fatalf("Get(%s): %v", adhoc.ID, err)
	}
	if !maps.Equal(got.Metadata, before) {
		t.Fatalf("adhoc bead %s was modified by reconciler: %v", adhoc.ID, got.Metadata)
	}
	if got := len(rec.metadataRepairedEvents()); got != 0 {
		t.Fatalf("event spy: got %d session.metadata_repaired events, want 0", got)
	}
}

func namedMetadataRepairTestConfig() *config.City {
	return &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{{
			Name:         "runner",
			StartCommand: "true",
		}},
		NamedSessions: []config.NamedSession{{
			Name:     "named",
			Template: "runner",
		}},
	}
}

func createBrokenNamedSessionBead(t *testing.T, store beads.Store, alias, backing string) beads.Bead {
	t.Helper()
	bead, err := store.Create(beads.Bead{
		Type:   session.BeadType,
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"agent_name":   backing,
			"alias":        alias,
			"session_name": "session-" + alias,
			"template":     backing,
		},
	})
	if err != nil {
		t.Fatalf("Create(broken session): %v", err)
	}
	return bead
}

func namedMetadataRepairDesiredState(bead beads.Bead) DesiredStateResult {
	sessionName := bead.Metadata["session_name"]
	return DesiredStateResult{
		State: map[string]TemplateParams{
			sessionName: {
				SessionName:             sessionName,
				Alias:                   bead.Metadata["alias"],
				TemplateName:            bead.Metadata["template"],
				ConfiguredNamedIdentity: "named",
				ConfiguredNamedMode:     "on_demand",
				ResolvedProvider:        &config.ResolvedProvider{Name: "fake", BuiltinAncestor: "fake"},
			},
		},
	}
}

type metadataBatchCountingStore struct {
	*beads.MemStore
	mu     sync.Mutex
	err    error
	writes int
}

func (s *metadataBatchCountingStore) SetMetadataBatch(id string, kvs map[string]string) error {
	if s.err != nil {
		return s.err
	}
	if err := s.MemStore.SetMetadataBatch(id, kvs); err != nil {
		return err
	}
	s.mu.Lock()
	s.writes++
	s.mu.Unlock()
	return nil
}

func (s *metadataBatchCountingStore) successfulWrites() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

type namedMetadataRepairRecorder struct {
	mu     sync.Mutex
	events []events.Event
}

func (r *namedMetadataRepairRecorder) Record(e events.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *namedMetadataRepairRecorder) metadataRepairedEvents() []events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []events.Event
	for _, event := range r.events {
		if event.Type == events.SessionMetadataRepaired {
			out = append(out, event)
		}
	}
	return out
}
