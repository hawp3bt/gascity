package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	gcapi "github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/session"
)

// repairConfiguredNamedSessionMetadata scans open session beads and backfills
// the configured_named_* metadata family on beads that match configured named
// ownership but are missing configured_named_session=true. The repair is
// idempotent and best-effort: write failures are logged and the scan
// continues, with the first write error returned for traceability.
func (cr *CityRuntime) repairConfiguredNamedSessionMetadata(ctx context.Context, sessionBeads *sessionBeadSnapshot) (int, error) {
	if cr == nil || cr.cfg == nil || len(cr.cfg.NamedSessions) == 0 || sessionBeads == nil {
		return 0, nil
	}
	store := cr.cityBeadStore()
	if store == nil {
		return 0, nil
	}
	stderr := cr.stderr
	if stderr == nil {
		stderr = io.Discard
	}

	repaired := 0
	var firstErr error
	for _, b := range sessionBeads.Open() {
		if err := ctx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return repaired, firstErr
		}
		if session.IsNamedSessionBead(b) {
			continue
		}
		alias := strings.TrimSpace(b.Metadata["alias"])
		if alias == "" {
			continue
		}
		spec, ok := session.FindNamedSessionSpec(cr.cfg, cr.cityName, alias)
		if !ok {
			continue
		}
		if !session.NamedSessionContinuityEligible(b) {
			continue
		}
		if !session.BeadIsLikelyConfiguredNamedOwner(b, spec) {
			continue
		}

		kvs := map[string]string{
			session.NamedSessionMetadataKey:      "true",
			session.NamedSessionIdentityMetadata: spec.Identity,
			session.NamedSessionModeMetadata:     spec.Mode,
		}
		if err := store.SetMetadataBatch(b.ID, kvs); err != nil {
			fmt.Fprintf(stderr, "repairConfiguredNamedSessionMetadata: %s: %v\n", b.ID, err) //nolint:errcheck
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if cr.rec != nil {
			payload, _ := json.Marshal(gcapi.SessionMetadataRepairedPayload{
				BeadID:                  b.ID,
				ConfiguredNamedIdentity: spec.Identity,
				ConfiguredNamedMode:     spec.Mode,
				Reason:                  gcapi.SessionMetadataRepairReasonMissingMetadata,
			})
			cr.rec.Record(events.Event{
				Type:    events.SessionMetadataRepaired,
				Actor:   "gc",
				Subject: b.ID,
				Message: fmt.Sprintf("backfilled configured_named_* metadata for %s", spec.Identity),
				Payload: payload,
			})
		}
		repaired++
	}
	return repaired, firstErr
}
