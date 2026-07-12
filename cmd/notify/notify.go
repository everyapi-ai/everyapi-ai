// Package notify wires `everyapi inbox notify …` — in-app notifications:
// list, unread count, mark one read, mark all read.
package notify

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
	if len(args) == 0 {
		return runList(nil)
	}
	if len(args) > 1 && (args[1] == "help" || args[1] == "--help" || args[1] == "-h") {
		cliout.Println(i18n.T("notify.usage"))
		return nil
	}
	switch args[0] {
	case "help", "--help", "-h":
		cliout.Println(i18n.T("notify.usage"))
		return nil
	case "list":
		return runList(args[1:])
	case "count":
		return runCount(args[1:])
	case "read":
		return runRead(args[1:])
	case "readall":
		return runReadAll(args[1:])
	default:
		cliout.Println(i18n.T("notify.usage"))
		return fmt.Errorf("unknown 'notify' subcommand %q", args[0])
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

func runList(args []string) error {
	fs := flag.NewFlagSet("notify list", flag.ContinueOnError)
	unread := fs.Bool("unread", false, "only unread rows")
	page := fs.Int("page", 0, "1-based page")
	limit := fs.Int("limit", 20, "page size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, total, err := client.ListNotifications(cliout.WithCtx(), *unread, *page, *limit)
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		if *unread {
			cliout.Println(i18n.T("notify.no_unread"))
		} else {
			cliout.Println(i18n.T("notify.no_rows"))
		}
		return nil
	}
	cliout.Printf(i18n.T("common.rows_of_total")+"\n", len(rows), total)
	for _, n := range rows {
		when := time.Unix(n.CreatedAt, 0).Format("01-02 15:04")
		marker := " "
		if n.ReadAt == 0 {
			marker = "•"
		}
		title := cliout.Sanitize(n.Title)
		if title == "" {
			title = cliout.Sanitize(n.Type)
		}
		cliout.Printf("  %s [#%d] %s  %s\n", marker, n.ID, when, title)
		if n.Body != "" {
			// Sanitize before truncating so the rune slice can't split a
			// multi-byte escape sequence and re-arm a partial control byte.
			body := cliout.Sanitize(n.Body)
			if r := []rune(body); len(r) > 120 {
				body = string(r[:120]) + "…"
			}
			cliout.Printf("        %s\n", body)
		}
	}
	return nil
}

func runCount(args []string) error {
	fs := flag.NewFlagSet("notify count", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	n, err := client.NotificationUnreadCount(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("notify.unread_count")+"\n", n)
	return nil
}

func runRead(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi inbox notify read <id>")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid id %q", args[0])
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if err := client.MarkNotificationRead(cliout.WithCtx(), id); err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("notify.marked_read")+"\n", id)
	return nil
}

func runReadAll(args []string) error {
	fs := flag.NewFlagSet("notify readall", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	n, err := client.MarkAllNotificationsRead(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("notify.marked_all")+"\n", n)
	return nil
}
