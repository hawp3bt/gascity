package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/emergency"
	"github.com/gastownhall/gascity/internal/osnotify"
	"github.com/spf13/cobra"
)

const emergencyControllerCommandPrefix = "emergency:"

type emergencySendOptions struct {
	severity string
	ref      string
	actor    string
	metadata []string
	notify   bool
	quiet    bool
	message  string
	bodyFile string
}

func newEmergencyCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "emergency",
		Short: "Send dolt-independent emergency signals",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprintln(stderr, "gc emergency: missing subcommand (send)") //nolint:errcheck // best-effort stderr
			} else {
				fmt.Fprintf(stderr, "gc emergency: unknown subcommand %q\n", args[0]) //nolint:errcheck // best-effort stderr
			}
			return errExit
		},
	}
	cmd.AddCommand(newEmergencySendCmd(stdout, stderr))
	return cmd
}

func newEmergencySendCmd(stdout, stderr io.Writer) *cobra.Command {
	opts := emergencySendOptions{severity: emergency.SeverityError}
	cmd := &cobra.Command{
		Use:   "send [flags] [<message>]",
		Short: "Send a dolt-independent emergency signal",
		Long: `Send a dolt-independent emergency signal.

Use this when normal reporting paths such as bd update or gc mail send
cannot be trusted. The signal is written to a filesystem spool and then
best-effort forwarded to events.jsonl, the controller socket, and the host
notification system.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitForCode(cmdEmergencySend(cmd, args, opts, stdout, stderr))
		},
	}
	cmd.Flags().StringVarP(&opts.severity, "severity", "s", emergency.SeverityError, "info|warn|error|critical")
	cmd.Flags().StringVar(&opts.ref, "ref", "", "related bead id")
	cmd.Flags().StringVar(&opts.actor, "actor", "", "actor name (default: $GC_ALIAS, $GC_AGENT, $GC_SESSION_ID, $BEADS_ACTOR, human)")
	cmd.Flags().StringArrayVar(&opts.metadata, "metadata", nil, "metadata key=value (repeatable)")
	cmd.Flags().BoolVar(&opts.notify, "notify", false, "force OS notification regardless of severity")
	cmd.Flags().BoolVar(&opts.quiet, "quiet", false, "suppress OS notification regardless of severity")
	cmd.Flags().StringVar(&opts.message, "message", "", "message body (alternative to positional)")
	cmd.Flags().StringVar(&opts.bodyFile, "body-file", "", "read message from file (\"-\" = stdin)")
	return cmd
}

func cmdEmergencySend(cmd *cobra.Command, args []string, opts emergencySendOptions, stdout, stderr io.Writer) int {
	if opts.notify && opts.quiet {
		fmt.Fprintln(stderr, "gc emergency send: --notify and --quiet are mutually exclusive") //nolint:errcheck // best-effort stderr
		return 2
	}
	message, err := resolveEmergencyMessage(args, opts.message, opts.bodyFile, cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(stderr, "gc emergency send: %v\n", err) //nolint:errcheck // best-effort stderr
		return 2
	}
	metadata, err := parseEmergencyMetadata(opts.metadata)
	if err != nil {
		fmt.Fprintf(stderr, "gc emergency send: %v\n", err) //nolint:errcheck // best-effort stderr
		return 2
	}
	ref := strings.TrimSpace(opts.ref)
	if ref != "" && !isBeadIDCandidate(ref) {
		fmt.Fprintf(stderr, "gc emergency send: --ref %q malformed; spooled without ref\n", ref) //nolint:errcheck // best-effort stderr
		ref = ""
	}
	cityPath, err := resolveCity()
	if err != nil {
		fmt.Fprintf(stderr, "gc emergency send: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	actor := strings.TrimSpace(opts.actor)
	if actor == "" {
		actor = eventActor()
	}
	cwd, _ := os.Getwd()
	hostname, _ := os.Hostname()
	rec, err := emergency.NewRecord(emergency.RecordOptions{
		Severity:   opts.severity,
		Actor:      actor,
		Message:    message,
		RefBead:    ref,
		SourcePath: cwd,
		SourcePID:  os.Getpid(),
		Hostname:   hostname,
		Metadata:   metadata,
	})
	if err != nil {
		fmt.Fprintf(stderr, "gc emergency send: %v\n", err) //nolint:errcheck // best-effort stderr
		if emergencyInputError(err) {
			return 2
		}
		return 1
	}
	if _, err := emergency.WriteSpool(cityPath, rec); err != nil {
		fmt.Fprintf(stderr, "gc emergency send: spool write failed: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	fmt.Fprintf(stderr, "gc emergency send: spooled (%s)\n", rec.ID) //nolint:errcheck // best-effort stderr
	if err := emergency.RecordSignaledToCityLog(cityPath, rec, stderr); err != nil {
		fmt.Fprintf(stderr, "gc emergency send: events recorder error: %v\n", err) //nolint:errcheck // best-effort stderr
	} else {
		fmt.Fprintln(stderr, "gc emergency send: events.jsonl recorded") //nolint:errcheck // best-effort stderr
	}
	if err := sendEmergencyToController(cityPath, rec, time.Second); err != nil {
		fmt.Fprintln(stderr, "gc emergency send: controller unreachable, spool only") //nolint:errcheck // best-effort stderr
	} else {
		fmt.Fprintln(stderr, "gc emergency send: controller socket ok") //nolint:errcheck // best-effort stderr
	}
	maybeNotifyEmergency(cityPath, rec, opts, stderr)
	fmt.Fprintln(stdout, rec.ID) //nolint:errcheck // best-effort stdout
	return 0
}

func resolveEmergencyMessage(args []string, messageFlag, bodyFile string, stdin io.Reader) (string, error) {
	forms := 0
	if len(args) > 0 {
		forms++
	}
	if strings.TrimSpace(messageFlag) != "" {
		forms++
	}
	if strings.TrimSpace(bodyFile) != "" {
		forms++
	}
	if forms > 1 {
		return "", fmt.Errorf("--message, --body-file, and a positional message are mutually exclusive")
	}
	switch {
	case len(args) > 0:
		return strings.TrimSpace(strings.Join(args, " ")), nil
	case strings.TrimSpace(messageFlag) != "":
		return strings.TrimSpace(messageFlag), nil
	case strings.TrimSpace(bodyFile) == "-":
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("reading --body-file -: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	case strings.TrimSpace(bodyFile) != "":
		data, err := os.ReadFile(filepath.Clean(bodyFile))
		if err != nil {
			return "", fmt.Errorf("reading --body-file %q: %w", bodyFile, err)
		}
		return strings.TrimSpace(string(data)), nil
	default:
		return "", fmt.Errorf("message is required")
	}
}

func parseEmergencyMetadata(entries []string) (map[string]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(entries))
	for _, raw := range entries {
		key, value, ok := strings.Cut(raw, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("--metadata must be key=value")
		}
		if emergencyReservedMetadataKey(key) {
			return nil, fmt.Errorf("--metadata key %q is reserved", key)
		}
		out[key] = strings.TrimSpace(value)
	}
	return out, nil
}

func emergencyReservedMetadataKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "id", "severity", "actor", "message", "ref_bead", "source_path", "source_pid", "hostname", "created_at", "metadata":
		return true
	default:
		return false
	}
}

func emergencyInputError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "severity") ||
		strings.Contains(msg, "message is required") ||
		strings.Contains(msg, "4 KiB")
}

func sendEmergencyToController(cityPath string, rec emergency.Record, timeout time.Duration) error {
	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encoding controller emergency request: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", controllerSocketPath(cityPath))
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck // best-effort cleanup
	deadline := time.Now().Add(timeout)
	_ = conn.SetDeadline(deadline)
	if _, err := conn.Write(append([]byte(emergencyControllerCommandPrefix), append(payload, '\n')...)); err != nil {
		return err
	}
	buf := make([]byte, 16)
	n, err := conn.Read(buf)
	if err != nil {
		return err
	}
	if string(buf[:n]) != "ok\n" {
		return fmt.Errorf("unexpected controller response %q", string(buf[:n]))
	}
	return nil
}

func maybeNotifyEmergency(cityPath string, rec emergency.Record, opts emergencySendOptions, stderr io.Writer) {
	if opts.quiet {
		return
	}
	if !opts.notify && rec.Severity != emergency.SeverityCritical {
		return
	}
	dedupe, err := emergency.MarkNotifyDedupe(
		cityPath,
		emergency.NotifyDedupeKey(rec.Severity, rec.Message),
		time.Now(),
		5*time.Minute,
	)
	if err != nil {
		fmt.Fprintf(stderr, "gc emergency send: notify dedupe error: %v, spool only\n", err) //nolint:errcheck // best-effort stderr
		return
	}
	if !dedupe.Fire {
		fmt.Fprintf(stderr, "gc emergency send: notify dedupe (recent same-severity message, key %s, fired %s ago)\n", dedupe.KeyPrefix, dedupe.Age.Round(time.Second)) //nolint:errcheck // best-effort stderr
		return
	}
	result := osnotify.Notify(context.Background(), osnotify.Notification{
		Severity: rec.Severity,
		Actor:    rec.Actor,
		Message:  rec.Message,
		RefBead:  rec.RefBead,
	}, osnotify.Dependencies{})
	if result.Err != nil {
		fmt.Fprintf(stderr, "gc emergency send: %s error: %v, spool only\n", result.Backend, result.Err) //nolint:errcheck // best-effort stderr
		return
	}
	if !result.Fired {
		fmt.Fprintf(stderr, "gc emergency send: %s not on PATH, spool only\n", result.Backend) //nolint:errcheck // best-effort stderr
		return
	}
	fmt.Fprintf(stderr, "gc emergency send: %s fired\n", result.Backend) //nolint:errcheck // best-effort stderr
}
