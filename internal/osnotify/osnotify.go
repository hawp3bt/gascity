// Package osnotify sends best-effort desktop notifications.
package osnotify

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	appName               = "Gas City"
	notificationBodyLimit = 240
)

// Notification is the canonical emergency notification content.
type Notification struct {
	Severity string
	Actor    string
	Message  string
	RefBead  string
}

// RenderedNotification is the platform-neutral notification text.
type RenderedNotification struct {
	Title  string
	Body   string
	Footer string
}

// Result describes a best-effort notification attempt.
type Result struct {
	Backend string
	Fired   bool
	Err     error
}

// Dependencies supplies platform and process hooks for Notify.
type Dependencies struct {
	GOOS     string
	LookPath func(string) (string, error)
	Run      func(context.Context, string, ...string) error
}

// Render formats a notification consistently across platform backends.
func Render(n Notification) RenderedNotification {
	severity := strings.TrimSpace(n.Severity)
	if severity == "" {
		severity = "error"
	}
	actor := strings.TrimSpace(n.Actor)
	if actor == "" {
		actor = "human"
	}
	body := collapseNotificationWhitespace(n.Message)
	body = truncateRunes(body, notificationBodyLimit)
	rendered := RenderedNotification{
		Title: "[" + severity + "] " + actor,
		Body:  body,
	}
	if ref := strings.TrimSpace(n.RefBead); ref != "" {
		rendered.Footer = "bead: " + ref
	}
	return rendered
}

// Notify fires a desktop notification with a one-second deadline. A missing
// platform backend is a non-error skip.
func Notify(ctx context.Context, n Notification, deps Dependencies) Result {
	deps = normalizeDependencies(deps)
	rendered := Render(n)
	switch deps.GOOS {
	case "darwin":
		return notifyDarwin(ctx, rendered, deps)
	case "windows":
		return notifyWindows(ctx, rendered, deps)
	default:
		return notifyLinux(ctx, n.Severity, rendered, deps)
	}
}

func normalizeDependencies(deps Dependencies) Dependencies {
	if deps.GOOS == "" {
		deps.GOOS = runtime.GOOS
	}
	if deps.LookPath == nil {
		deps.LookPath = exec.LookPath
	}
	if deps.Run == nil {
		deps.Run = func(ctx context.Context, name string, args ...string) error {
			return exec.CommandContext(ctx, name, args...).Run()
		}
	}
	return deps
}

func notifyLinux(ctx context.Context, severity string, rendered RenderedNotification, deps Dependencies) Result {
	const backend = "notify-send"
	path, err := deps.LookPath(backend)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Result{Backend: backend}
		}
		return Result{Backend: backend, Err: err}
	}
	args := []string{
		"-u", linuxUrgency(severity),
		"-a", appName,
		"-i", linuxIcon(severity),
		"-t", linuxTimeout(severity),
		rendered.Title,
		bodyWithFooter(rendered),
	}
	return runWithDeadline(ctx, backend, path, deps, args...)
}

func notifyDarwin(ctx context.Context, rendered RenderedNotification, deps Dependencies) Result {
	const backend = "osascript"
	path, err := deps.LookPath(backend)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Result{Backend: backend}
		}
		return Result{Backend: backend, Err: err}
	}
	args := []string{
		"-e", `on run argv`,
		"-e", `display notification (item 1 of argv) with title (item 2 of argv) subtitle (item 3 of argv)`,
		"-e", `end run`,
		bodyWithFooter(rendered),
		rendered.Title,
		rendered.Footer,
	}
	return runWithDeadline(ctx, backend, path, deps, args...)
}

func notifyWindows(ctx context.Context, rendered RenderedNotification, deps Dependencies) Result {
	const backend = "pwsh"
	path, err := deps.LookPath(backend)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Result{Backend: backend}
		}
		return Result{Backend: backend, Err: err}
	}
	args := []string{
		"-NoProfile",
		"-Command",
		"New-BurntToastNotification -Text @($args[0], $args[1], $args[2])",
		rendered.Title,
		rendered.Body,
		rendered.Footer,
	}
	return runWithDeadline(ctx, backend, path, deps, args...)
}

func runWithDeadline(ctx context.Context, backend, path string, deps Dependencies, args ...string) Result {
	if ctx == nil {
		ctx = context.Background()
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := deps.Run(deadlineCtx, path, args...); err != nil {
		return Result{Backend: backend, Err: err}
	}
	return Result{Backend: backend, Fired: true}
}

func bodyWithFooter(rendered RenderedNotification) string {
	if rendered.Footer == "" {
		return rendered.Body
	}
	if rendered.Body == "" {
		return rendered.Footer
	}
	return rendered.Body + "\n\n" + rendered.Footer
}

func collapseNotificationWhitespace(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}

func linuxUrgency(severity string) string {
	switch strings.TrimSpace(severity) {
	case "info":
		return "low"
	case "warn":
		return "normal"
	default:
		return "critical"
	}
}

func linuxIcon(severity string) string {
	switch strings.TrimSpace(severity) {
	case "info":
		return "dialog-information"
	case "warn":
		return "dialog-warning"
	default:
		return "dialog-error"
	}
}

func linuxTimeout(severity string) string {
	switch strings.TrimSpace(severity) {
	case "info":
		return "5000"
	case "warn":
		return "10000"
	default:
		return "0"
	}
}
