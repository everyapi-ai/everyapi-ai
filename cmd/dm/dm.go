// Package dm wires `everyapi inbox dm …` — direct messages between
// users in the marketplace (compensation discussions, support
// threads, etc.). Read / open / send / messages / read.
package dm

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliargs"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(i18n.T("dm.usage"))
		if len(args) == 0 {
			return errors.New("missing subcommand")
		}
		return nil
	}
	if len(args) > 1 && (args[1] == "help" || args[1] == "--help" || args[1] == "-h") {
		cliout.Println(i18n.T("dm.usage"))
		return nil
	}
	switch args[0] {
	case "threads":
		return runThreads(args[1:])
	case "contacts":
		return runContacts(args[1:])
	case "count":
		return runCount(args[1:])
	case "open":
		return runOpen(args[1:])
	case "messages":
		return runMessages(args[1:])
	case "send":
		return runSend(args[1:])
	case "read":
		return runRead(args[1:])
	default:
		cliout.Println(i18n.T("dm.usage"))
		return fmt.Errorf("unknown 'dm' subcommand %q", args[0])
	}
}

func newClient() (*api.Client, error) {
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return nil, errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return nil, err
	}
	return api.ForCredentials(creds), nil
}

// myUserID loads the caller's own user id from saved credentials. It
// returns 0 when the id isn't known (no credentials, or an OAuth2
// login that didn't persist a user id) — threadCounterpart treats 0
// as "viewer unknown" and shows both participants rather than guessing
// which side is "us".
func myUserID() int {
	creds, err := config.Load()
	if err != nil {
		return 0
	}
	return creds.UserID
}

// threadCounterpart renders the *other* participant of a thread
// relative to the viewer `me`. The backend stores the pair unordered
// as UserA / UserB and does not pre-compute an "other" side, so we
// pick it here. We show both participants when we can't pin "us" to one
// side: `me` is 0 (unknown viewer — e.g. an OAuth2 login that didn't
// persist a user id) OR `me` matches neither participant (stale or
// hand-edited credentials, an account/token swap). The old code's final
// fall-through returned UserA unconditionally, which on a neither-match
// mislabeled an arbitrary side as "the counterpart".
func threadCounterpart(t api.DMThread, me int) string {
	both := func() string {
		return fmt.Sprintf("uid=%d (%s) ↔ uid=%d (%s)",
			t.UserAID, cliout.Sanitize(t.UserAUsername),
			t.UserBID, cliout.Sanitize(t.UserBUsername))
	}
	if me <= 0 {
		return both()
	}
	switch me {
	case t.UserAID:
		return fmt.Sprintf("uid=%d (%s)", t.UserBID, cliout.Sanitize(t.UserBUsername))
	case t.UserBID:
		return fmt.Sprintf("uid=%d (%s)", t.UserAID, cliout.Sanitize(t.UserAUsername))
	default:
		return both()
	}
}

func classifyErr(err error) error {
	if err == nil {
		return nil
	}
	if api.IsUnauthorized(err) {
		return errors.New(i18n.T("auth.session_expired"))
	}
	return err
}

func parseInt(args []string, idx int, name string) (int, error) {
	if len(args) <= idx {
		return 0, fmt.Errorf("missing <%s>", name)
	}
	v, err := strconv.Atoi(args[idx])
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("invalid %s %q", name, args[idx])
	}
	return v, nil
}

func runThreads(args []string) error {
	fs := flag.NewFlagSet("dm threads", flag.ContinueOnError)
	page := fs.Int("page", 0, "1-based page")
	limit := fs.Int("limit", 20, "page size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, total, err := client.ListDMThreads(cliout.WithCtx(), *page, *limit)
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println(i18n.T("dm.no_threads"))
		return nil
	}
	cliout.Printf(i18n.T("dm.threads_of_total")+"\n", len(rows), total)
	me := myUserID()
	for _, t := range rows {
		when := time.Unix(t.LastMessageAt, 0).Format("01-02 15:04")
		marker := " "
		if t.UnreadForViewer > 0 {
			marker = fmt.Sprintf("(%d)", t.UnreadForViewer)
		}
		cliout.Printf("  %s [#%d] %s with %s\n",
			marker, t.ID, when, threadCounterpart(t, me))
		if t.Subject != "" {
			cliout.Printf("        %s\n", cliout.Sanitize(t.Subject))
		}
	}
	return nil
}

func runContacts(args []string) error {
	fs := flag.NewFlagSet("dm contacts", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, err := client.ListDMContacts(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println(i18n.T("dm.no_contacts"))
		return nil
	}
	cliout.Printf("%d contact(s):\n", len(rows))
	for _, r := range rows {
		cliout.Printf("  uid=%d  %s\n", r.UserID, cliout.Sanitize(r.Username))
	}
	return nil
}

func runCount(args []string) error {
	if err := cliargs.RequireExact(args, 0); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	n, err := client.DMUnreadCount(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("notify.unread_count")+"\n", n)
	return nil
}

func runOpen(args []string) error {
	if err := cliargs.RequireExact(args, 1); err != nil {
		return err
	}
	uid, err := parseInt(args, 0, "other_user_id")
	if err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	t, err := client.OpenDMThread(cliout.WithCtx(), uid)
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf("Thread #%d  with %s\n", t.ID, threadCounterpart(*t, myUserID()))
	return nil
}

func runMessages(args []string) error {
	tid, err := parseInt(args, 0, "thread_id")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("dm messages", flag.ContinueOnError)
	after := fs.Int("after", 0, "message id (exclusive)")
	limit := fs.Int("limit", 50, "page size")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, err := client.ListDMMessages(cliout.WithCtx(), tid, *after, *limit)
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println(i18n.T("dm.no_messages"))
		return nil
	}
	// The backend tracks read state per-thread (a per-user read pointer),
	// not per-message — there is no read_at on a message — so a "read/unread"
	// dot here would be meaningless (always the same). Mark direction instead:
	// → sent by you, ← received. When the viewer id is unknown (OAuth2 login
	// that didn't persist one) we can't attribute a side, so leave it blank.
	me := myUserID()
	for _, m := range rows {
		when := time.Unix(m.CreatedAt, 0).Format("01-02 15:04")
		dir := " "
		switch {
		case me <= 0:
		case m.SenderID == me:
			dir = "→"
		default:
			dir = "←"
		}
		cliout.Printf("  %s [#%d] %s  uid=%d: %s\n", dir, m.ID, when, m.SenderID, cliout.Sanitize(m.Body))
	}
	return nil
}

func runSend(args []string) error {
	tid, err := parseInt(args, 0, "thread_id")
	if err != nil {
		return err
	}
	if len(args) < 2 {
		return errors.New("usage: everyapi inbox dm send <thread_id> <body>")
	}
	body := args[1]
	for _, extra := range args[2:] {
		body += " " + extra
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	m, err := client.SendDMMessage(cliout.WithCtx(), tid, body)
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("dm.sent_at")+"\n", m.ID, time.Unix(m.CreatedAt, 0).Format("2006-01-02 15:04:05"))
	return nil
}

func runRead(args []string) error {
	if err := cliargs.RequireExact(args, 1); err != nil {
		return err
	}
	tid, err := parseInt(args, 0, "thread_id")
	if err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if err := client.MarkDMRead(cliout.WithCtx(), tid); err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("dm.marked_read")+"\n", tid)
	return nil
}
