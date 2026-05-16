package api

import (
	"testing"

	"github.com/gastownhall/gascity/internal/events"
)

func TestEmergencyEventPayloadRegistered(t *testing.T) {
	for _, eventType := range []string{events.EmergencySignaled, events.EmergencyAcked} {
		payload, ok := events.LookupPayload(eventType)
		if !ok {
			t.Fatalf("%s payload not registered", eventType)
		}
		if _, ok := payload.(EmergencyEventPayload); !ok {
			t.Fatalf("%s payload = %T, want EmergencyEventPayload", eventType, payload)
		}
	}
}
