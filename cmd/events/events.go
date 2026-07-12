// Package events wires `everyapi events` — long-lived subscriber to
// the backend SSE stream at /api/cli/events. The SDK does the
// reconnect / backoff / heartbeat bookkeeping; this command just
// renders the event channel until ctx is cancelled.
package events

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliargs"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const usage = `everyapi events — subscribe to the live event stream

USAGE
  everyapi events [--quiet] [--types <a,b,c>]

FLAGS
  --quiet            Suppress non-event chatter (transport warnings)
  --types <list>     Only print these event types (CSV; default: all)
`

func Run(args []string) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	quiet := fs.Bool("quiet", false, "suppress transport warnings")
	typesCSV := fs.String("types", "", "filter to these event types (CSV)")
	if len(args) > 0 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		cliout.Println(usage)
		return nil
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return err
	}
	wantType := map[string]bool{}
	if *typesCSV != "" {
		for _, t := range splitCSV(*typesCSV) {
			wantType[t] = true
		}
	}
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)

	// ctx is cancelled on SIGINT/SIGTERM so a Ctrl-C closes the SSE
	// loop cleanly instead of leaving an open TCP socket to the
	// backend. The SDK's SubscribeEvents drains its goroutine off
	// ctx cancellation.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// authErr latches a 401 surfaced by the SSE stream. The SDK ends
	// the subscription on 401 rather than reconnecting forever (see
	// SubscribeEvents); we capture the error here and surface it as a
	// normal command error once the stream drains, so a revoked token
	// reads as "session expired" instead of a silent return to the
	// menu — and we skip the "reconnecting" line, which would be a
	// lie for a 401.
	var authErr error
	onErr := func(e error) {
		if api.IsUnauthorized(e) {
			authErr = e
			return
		}
		if *quiet {
			return
		}
		fmt.Fprintf(os.Stderr, i18n.T("events.transport_err")+"\n", e)
	}
	ch := client.SubscribeEvents(ctx, onErr)

	cliout.Println(i18n.T("events.connected"))
	for ev := range ch {
		if len(wantType) > 0 && !wantType[ev.Type] {
			continue
		}
		ts := time.Now().Format("15:04:05")
		cliout.Printf("[%s] %s  %s\n", ts, cliout.Sanitize(ev.Type), cliout.Sanitize(string(ev.Data)))
	}
	// onErr (SDK goroutine) writes authErr strictly before the SDK
	// closes ch on a 401; the channel close synchronises that write
	// with this read, so the access is race-free.
	if authErr != nil {
		return errors.New(i18n.T("auth.session_expired"))
	}
	return nil
}

func splitCSV(s string) []string {
	out := []string{}
	cur := ""
	for _, ch := range s {
		if ch == ',' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		if ch == ' ' || ch == '\t' {
			continue
		}
		cur += string(ch)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
