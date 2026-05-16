package main

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/emergency"
)

func TestHandleControllerConnEmergencyFanout(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close() //nolint:errcheck

	convergenceReqCh := make(chan convergenceRequest, 1)
	pokeCh := make(chan struct{}, 1)
	controlDispatcherCh := make(chan struct{}, 1)
	emergencyCh := make(chan emergency.Record, 1)
	cityPath := t.TempDir()

	done := make(chan struct{})
	go func() {
		handleControllerConn(server, cityPath, func() {}, nil, nil, nil, convergenceReqCh, pokeCh, controlDispatcherCh, emergencyCh)
		close(done)
	}()

	rec := emergency.Record{ID: "20260430T160701Z-7e3f9c12", Severity: emergency.SeverityCritical, Actor: "human", Message: "dolt down"}
	payload, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	if _, err := client.Write(append([]byte("emergency:"), append(payload, '\n')...)); err != nil {
		t.Fatalf("write command: %v", err)
	}
	buf := make([]byte, 16)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if got := string(buf[:n]); got != "ok\n" {
		t.Fatalf("ack = %q, want ok", got)
	}

	select {
	case got := <-emergencyCh:
		if got.ID != rec.ID || got.Message != rec.Message {
			t.Fatalf("emergency fanout = %+v, want %+v", got, rec)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("emergency channel was not signaled")
	}

	select {
	case <-pokeCh:
		t.Fatal("generic poke channel should remain untouched")
	default:
	}

	client.Close() //nolint:errcheck
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleControllerConn did not exit")
	}
}
