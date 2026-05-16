package osnotify

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestRenderSeverityFirstAndTruncatesBody(t *testing.T) {
	rendered := Render(Notification{
		Severity: "critical",
		Actor:    "gascity/agent-a",
		Message:  strings.Repeat("a", 260),
		RefBead:  "ga-51t",
	})

	if rendered.Title != "[critical] gascity/agent-a" {
		t.Fatalf("title = %q, want severity first", rendered.Title)
	}
	if len(rendered.Body) > 243 || !strings.HasSuffix(rendered.Body, "...") {
		t.Fatalf("body = %q, want 240-char truncation plus ellipsis", rendered.Body)
	}
	if rendered.Footer != "bead: ga-51t" {
		t.Fatalf("footer = %q, want bead ref", rendered.Footer)
	}
}

func TestNotifyLinuxUsesNotifySendWithUrgency(t *testing.T) {
	var gotName string
	var gotArgs []string
	result := Notify(context.Background(), Notification{
		Severity: "critical",
		Actor:    "gascity/agent-a",
		Message:  "dolt down",
		RefBead:  "ga-51t",
	}, Dependencies{
		GOOS: "linux",
		LookPath: func(name string) (string, error) {
			if name != "notify-send" {
				t.Fatalf("LookPath(%q), want notify-send", name)
			}
			return "/usr/bin/notify-send", nil
		},
		Run: func(_ context.Context, name string, args ...string) error {
			gotName = name
			gotArgs = append([]string(nil), args...)
			return nil
		},
	})

	if result.Err != nil || !result.Fired || result.Backend != "notify-send" {
		t.Fatalf("Notify result = %+v, want fired notify-send without error", result)
	}
	if gotName != "/usr/bin/notify-send" {
		t.Fatalf("run name = %q, want resolved notify-send path", gotName)
	}
	joined := strings.Join(gotArgs, "\x00")
	for _, want := range []string{"-u\x00critical", "-a\x00Gas City", "[critical] gascity/agent-a", "dolt down", "bead: ga-51t"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("notify-send args = %#v, missing %q", gotArgs, want)
		}
	}
}

func TestNotifyMissingBinaryIsNonError(t *testing.T) {
	result := Notify(context.Background(), Notification{
		Severity: "critical",
		Actor:    "human",
		Message:  "dolt down",
	}, Dependencies{
		GOOS: "linux",
		LookPath: func(string) (string, error) {
			return "", exec.ErrNotFound
		},
		Run: func(context.Context, string, ...string) error {
			t.Fatal("Run should not be called when backend is missing")
			return nil
		},
	})

	if result.Err != nil || result.Fired {
		t.Fatalf("Notify result = %+v, want missing binary as non-error skip", result)
	}
	if result.Backend != "notify-send" {
		t.Fatalf("backend = %q, want notify-send", result.Backend)
	}
}

func TestNotifyPropagatesRunError(t *testing.T) {
	runErr := errors.New("display failed")
	result := Notify(context.Background(), Notification{
		Severity: "warn",
		Actor:    "human",
		Message:  "degraded",
	}, Dependencies{
		GOOS:     "linux",
		LookPath: func(string) (string, error) { return "/usr/bin/notify-send", nil },
		Run:      func(context.Context, string, ...string) error { return runErr },
	})

	if !errors.Is(result.Err, runErr) || result.Fired {
		t.Fatalf("Notify result = %+v, want run error and not fired", result)
	}
}
