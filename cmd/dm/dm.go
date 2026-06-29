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
	return api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID), nil
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
	cliout.Printf("%d thread(s) of %d total:\n", len(rows), total)
	for _, t := range rows {
		when := time.Unix(t.LastMessageAt, 0).Format("01-02 15:04")
		marker := " "
		if t.UnreadCount > 0 {
			marker = fmt.Sprintf("(%d)", t.UnreadCount)
		}
		preview := cliout.Sanitize(t.LastMessagePreview)
		if r := []rune(preview); len(r) > 80 {
			preview = string(r[:80]) + "…"
		}
		cliout.Printf("  %s [#%d] %s with uid=%d (%s)\n",
			marker, t.ID, when, t.OtherUserID, cliout.Sanitize(t.OtherUsername))
		if preview != "" {
			cliout.Printf("        %s\n", preview)
		}
	}
	return nil
}

func runContacts(args []string) error {
	fs := flag.NewFlagSet("dm contacts", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
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
	cliout.Printf("Thread #%d  with uid=%d (%s)\n", t.ID, t.OtherUserID, cliout.Sanitize(t.OtherUsername))
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
	for _, m := range rows {
		when := time.Unix(m.CreatedAt, 0).Format("01-02 15:04")
		marker := " "
		if m.ReadAt == 0 {
			marker = "•"
		}
		cliout.Printf("  %s [#%d] %s  uid=%d: %s\n", marker, m.ID, when, m.SenderID, cliout.Sanitize(m.Body))
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
