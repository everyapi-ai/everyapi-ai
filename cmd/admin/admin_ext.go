package admin

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func newClient() (*api.Client, error) {
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return nil, errors.New("not logged in — run 'everyapi login' first")
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
		return errors.New("your session expired — run 'everyapi login' again")
	}
	return err
}

// --- admin user ----------------------------------------------------

func adminUser(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin user {list|search|show|manage|delete}")
	}
	switch args[0] {
	case "list":
		return adminUserList(args[1:])
	case "search":
		return adminUserSearch(args[1:])
	case "show":
		return adminUserShow(args[1:])
	case "manage":
		return adminUserManage(args[1:])
	case "delete":
		return adminUserDelete(args[1:])
	default:
		return fmt.Errorf("unknown 'admin user' subcommand %q", args[0])
	}
}

func adminUserList(args []string) error {
	fs := flag.NewFlagSet("admin user list", flag.ContinueOnError)
	page := fs.Int("page", 0, "1-based page")
	limit := fs.Int("limit", 20, "page size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, total, err := client.AdminListUsers(cliout.WithCtx(), *page, *limit)
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println("No users.")
		return nil
	}
	cliout.Printf("%d row(s) of %d total:\n", len(rows), total)
	for _, u := range rows {
		cliout.Printf("  [#%d] %s (%s) — role=%d status=%d quota=%d used=%d group=%s\n",
			u.ID, u.Username, u.Email, u.Role, u.Status, u.Quota, u.UsedQuota, u.Group)
	}
	return nil
}

func adminUserSearch(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin user search <keyword>")
	}
	keyword := args[0]
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, err := client.AdminSearchUsers(cliout.WithCtx(), keyword)
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println("No matches.")
		return nil
	}
	cliout.Printf("%d match(es):\n", len(rows))
	for _, u := range rows {
		cliout.Printf("  [#%d] %s (%s) — role=%d status=%d\n", u.ID, u.Username, u.Email, u.Role, u.Status)
	}
	return nil
}

func adminUserShow(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin user show <id>")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid id %q", args[0])
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	u, err := client.AdminGetUser(cliout.WithCtx(), id)
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf("User #%d\n", u.ID)
	cliout.Printf("  username:    %s\n", u.Username)
	cliout.Printf("  email:       %s\n", u.Email)
	cliout.Printf("  role:        %d\n", u.Role)
	cliout.Printf("  status:      %d\n", u.Status)
	cliout.Printf("  group:       %s\n", u.Group)
	cliout.Printf("  quota:       %d (used %d)\n", u.Quota, u.UsedQuota)
	if u.DisplayName != "" {
		cliout.Printf("  display:     %s\n", u.DisplayName)
	}
	return nil
}

func adminUserManage(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin user manage <id> --action <verb> [--value N]")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid id %q", args[0])
	}
	fs := flag.NewFlagSet("admin user manage", flag.ContinueOnError)
	action := fs.String("action", "", "enable / disable / delete / promote_admin / demote_admin")
	value := fs.Int("value", 0, "action-specific value")
	mode := fs.String("mode", "", "action-specific mode string")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *action == "" {
		return errors.New("--action is required")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if err := client.AdminManageUser(cliout.WithCtx(), api.AdminManageRequest{
		ID: id, Action: *action, Value: *value, Mode: *mode,
	}); err != nil {
		return classifyErr(err)
	}
	cliout.Printf("admin user manage: action=%q applied to user #%d\n", *action, id)
	return nil
}

func adminUserDelete(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin user delete <id> [-y]")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid id %q", args[0])
	}
	fs := flag.NewFlagSet("admin user delete", flag.ContinueOnError)
	yes := fs.Bool("y", false, "skip confirmation")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if !*yes && cliprompt.IsInteractive() {
		ok, err := cliprompt.YesNo(
			bufio.NewReader(os.Stdin),
			fmt.Sprintf("Delete user #%d? This is destructive and irreversible.", id),
			false,
		)
		if err != nil {
			return err
		}
		if !ok {
			cliout.Println("Canceled.")
			return nil
		}
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if err := client.AdminDeleteUser(cliout.WithCtx(), id); err != nil {
		return classifyErr(err)
	}
	cliout.Printf("User #%d deleted.\n", id)
	return nil
}

// --- admin channel ------------------------------------------------

func adminChannel(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin channel {test|tag}")
	}
	switch args[0] {
	case "test":
		return adminChannelTest(args[1:])
	case "tag":
		return adminChannelTag(args[1:])
	default:
		return fmt.Errorf("unknown 'admin channel' subcommand %q", args[0])
	}
}

func adminChannelTest(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin channel test <id>")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid channel id %q", args[0])
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	res, err := client.AdminTestChannel(cliout.WithCtx(), id)
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf("Channel #%d test:\n", id)
	if len(res) == 0 {
		cliout.Println("  (empty result)")
		return nil
	}
	for k, v := range res {
		cliout.Printf("  %s: %v\n", k, v)
	}
	return nil
}

func adminChannelTag(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin channel tag <name> --enable|--disable")
	}
	tag := args[0]
	fs := flag.NewFlagSet("admin channel tag", flag.ContinueOnError)
	enable := fs.Bool("enable", false, "enable every channel carrying the tag")
	disable := fs.Bool("disable", false, "disable every channel carrying the tag")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *enable == *disable {
		return errors.New("pass exactly one of --enable / --disable")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if err := client.AdminTagChannels(cliout.WithCtx(), tag, *enable); err != nil {
		return classifyErr(err)
	}
	verb := "disabled"
	if *enable {
		verb = "enabled"
	}
	cliout.Printf("Tag %q %s.\n", tag, verb)
	return nil
}

// --- admin log ----------------------------------------------------

func adminLog(args []string) error {
	if len(args) == 0 || args[0] != "tail" {
		// `admin log` bare = same as `admin log tail`.
		return adminLogTail(args)
	}
	return adminLogTail(args[1:])
}

func adminLogTail(args []string) error {
	fs := flag.NewFlagSet("admin log tail", flag.ContinueOnError)
	user := fs.String("user", "", "filter by username")
	model := fs.String("model", "", "filter by model name")
	ch := fs.Int("channel", 0, "filter by channel id")
	group := fs.String("group", "", "filter by routing group")
	sinceStr := fs.String("since", "1h", "window start (e.g. 1h, 24h, 7d) or Unix seconds")
	page := fs.Int("page", 0, "1-based page")
	limit := fs.Int("limit", 50, "page size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	now := time.Now()
	start, err := parseWindow(*sinceStr, now)
	if err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, total, err := client.AdminListLogs(cliout.WithCtx(), api.AdminLogFilter{
		Username: *user, ModelName: *model, Channel: *ch, Group: *group,
		Start: start,
		Page:  *page, PageSize: *limit,
	})
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println("No log rows match.")
		return nil
	}
	cliout.Printf("%d row(s) of %d total:\n", len(rows), total)
	for _, r := range rows {
		ts := time.Unix(r.CreatedAt, 0).Format("01-02 15:04:05")
		cliout.Printf("  %s  uid=%s  model=%s  quota=%d  tokens=%d/%d  ch=#%d\n",
			ts, r.Username, r.ModelName, r.Quota, r.PromptTokens, r.CompletionTokens, r.ChannelID)
		if r.Content != "" {
			cliout.Printf("    %s\n", r.Content)
		}
	}
	return nil
}

func parseWindow(s string, now time.Time) (int64, error) {
	if s == "" {
		return 0, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	// Support a "d" (days) suffix on top of Go's h/m/s. ParseDuration
	// rejects "d" so we strip + multiply manually.
	if len(s) > 1 && s[len(s)-1] == 'd' {
		if n, err := strconv.Atoi(s[:len(s)-1]); err == nil {
			return now.Add(-time.Duration(n) * 24 * time.Hour).Unix(), nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("--since: %q must be Unix seconds or a duration (e.g. 1h, 24h, 7d)", s)
	}
	return now.Add(-d).Unix(), nil
}

// --- admin abuse --------------------------------------------------

func adminAbuse(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin abuse {list|show|update}")
	}
	switch args[0] {
	case "list":
		return adminAbuseList(args[1:])
	case "show":
		return adminAbuseShow(args[1:])
	case "update":
		return adminAbuseUpdate(args[1:])
	default:
		return fmt.Errorf("unknown 'admin abuse' subcommand %q", args[0])
	}
}

func adminAbuseList(args []string) error {
	fs := flag.NewFlagSet("admin abuse list", flag.ContinueOnError)
	status := fs.String("status", "", "filter by status")
	page := fs.Int("page", 0, "1-based page")
	limit := fs.Int("limit", 20, "page size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, total, err := client.AdminListAbuseReports(cliout.WithCtx(), *status, *page, *limit)
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println("No reports.")
		return nil
	}
	cliout.Printf("%d row(s) of %d total:\n", len(rows), total)
	for _, r := range rows {
		when := time.Unix(r.CreatedAt, 0).Format("01-02 15:04")
		cliout.Printf("  [#%d] %s  %s/%s  status=%s  by=%s (%s)\n",
			r.ID, when, r.Category, r.TargetType, r.Status, r.ReporterEmail, r.ReporterIP)
	}
	return nil
}

func adminAbuseShow(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin abuse show <id>")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid id %q", args[0])
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	r, err := client.AdminGetAbuseReport(cliout.WithCtx(), id)
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf("Report #%d  status=%s\n", r.ID, r.Status)
	cliout.Printf("  filed:        %s\n", time.Unix(r.CreatedAt, 0).Format("2006-01-02 15:04:05"))
	cliout.Printf("  reporter:     %s (%s)  uid=%d\n", r.ReporterEmail, r.ReporterIP, r.ReporterUID)
	cliout.Printf("  category:     %s\n", r.Category)
	cliout.Printf("  target type:  %s\n", r.TargetType)
	if r.TargetID != "" {
		cliout.Printf("  target id:    %s\n", r.TargetID)
	}
	if r.EvidenceURL != "" {
		cliout.Printf("  evidence:     %s\n", r.EvidenceURL)
	}
	cliout.Printf("  description:\n    %s\n", r.Description)
	if r.AdminNote != "" {
		cliout.Printf("  admin note:\n    %s\n", r.AdminNote)
	}
	return nil
}

func adminAbuseUpdate(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin abuse update <id> --status X [--note N]")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid id %q", args[0])
	}
	fs := flag.NewFlagSet("admin abuse update", flag.ContinueOnError)
	status := fs.String("status", "", "new status (backend enumerates)")
	note := fs.String("note", "", "admin triage note")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *status == "" && *note == "" {
		return errors.New("pass at least one of --status / --note")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if err := client.AdminUpdateAbuseReport(cliout.WithCtx(), id, *status, *note); err != nil {
		return classifyErr(err)
	}
	cliout.Printf("Report #%d updated.\n", id)
	return nil
}

// --- admin audit --------------------------------------------------

func adminAudit(args []string) error {
	fs := flag.NewFlagSet("admin audit", flag.ContinueOnError)
	page := fs.Int("page", 0, "1-based page")
	limit := fs.Int("limit", 20, "page size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, total, err := client.AdminListAuditLog(cliout.WithCtx(), *page, *limit)
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println("No audit rows.")
		return nil
	}
	cliout.Printf("%d row(s) of %d total:\n", len(rows), total)
	for _, r := range rows {
		when := time.Unix(r.CreatedAt, 0).Format("01-02 15:04:05")
		cliout.Printf("  %s  [%s] uid=%d (%s)  target=%s/%s\n",
			when, r.Action, r.ActorID, r.ActorName, r.TargetType, r.TargetID)
		if r.Payload != "" {
			line := r.Payload
			if len(line) > 200 {
				line = line[:200] + "…"
			}
			cliout.Printf("    %s\n", line)
		}
	}
	return nil
}
