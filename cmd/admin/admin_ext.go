package admin

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/style"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

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

func classifyErr(err error) error {
	if err == nil {
		return nil
	}
	if api.IsUnauthorized(err) {
		return errors.New(i18n.T("auth.session_expired"))
	}
	return err
}

// --- admin user ----------------------------------------------------

func adminUser(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin user {list|search|show|manage|delete}")
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println("usage: everyapi admin user {list|search|show|manage|delete}")
		return nil
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
	if err := rejectPositionals(fs); err != nil {
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
		cliout.Println(i18n.T("admin.user.no_matches"))
		return nil
	}
	cliout.Printf(i18n.T("admin.common.rows_total")+"\n", len(rows), total)
	printUserRows(rows, quotaPerUnit(client), true)
	return nil
}

func adminUserSearch(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin user search <keyword>")
	}
	if err := requireExactPositionals(args, 1); err != nil {
		return err
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
		cliout.Println(i18n.T("admin.user.no_matches"))
		return nil
	}
	cliout.Printf(i18n.T("admin.common.matches")+"\n", len(rows))
	printUserRows(rows, 0, false)
	return nil
}

func adminUserShow(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin user show <id>")
	}
	if err := requireExactPositionals(args, 1); err != nil {
		return err
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
	printUserDetail(u, quotaPerUnit(client))
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
	yes := fs.Bool("y", false, "skip confirmation for destructive actions")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if err := rejectPositionals(fs); err != nil {
		return err
	}
	if *action == "" {
		return errors.New("--action is required")
	}
	// `delete` here routes to the same irreversible deletion as `admin user
	// delete`, so gate it behind the identical interactive confirmation.
	if *action == "delete" && !*yes {
		if !cliprompt.IsInteractive() {
			// Destructive + no TTY to confirm on: fail closed rather than
			// silently deleting. Require explicit -y for non-interactive use.
			return errors.New(i18n.T("token.revoke_needs_confirm"))
		}
		ok, err := cliprompt.YesNo(
			bufio.NewReader(os.Stdin),
			fmt.Sprintf(i18n.T("admin.user.delete_confirm"), id),
			false,
		)
		if err != nil {
			return err
		}
		if !ok {
			cliout.Println(mutedText("common.canceled"))
			return nil
		}
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
	cliout.Printf(okText("admin.user.action_applied")+"\n", *action, id)
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
	if err := rejectPositionals(fs); err != nil {
		return err
	}
	if !*yes {
		if !cliprompt.IsInteractive() {
			// Destructive + no TTY to confirm on: fail closed rather than
			// silently deleting. Require explicit -y for non-interactive use.
			return errors.New(i18n.T("token.revoke_needs_confirm"))
		}
		ok, err := cliprompt.YesNo(
			bufio.NewReader(os.Stdin),
			fmt.Sprintf(i18n.T("admin.user.delete_confirm"), id),
			false,
		)
		if err != nil {
			return err
		}
		if !ok {
			cliout.Println(mutedText("common.canceled"))
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
	cliout.Printf(okText("admin.user.deleted")+"\n", id)
	return nil
}

// --- admin channel ------------------------------------------------

func adminChannel(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin channel {test|tag|add-oauth}")
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println("usage: everyapi admin channel {test|tag|add-oauth}")
		return nil
	}
	switch args[0] {
	case "test":
		return adminChannelTest(args[1:])
	case "tag":
		return adminChannelTag(args[1:])
	case "add-oauth":
		return adminChannelAddOAuth(args[1:])
	default:
		return fmt.Errorf("unknown 'admin channel' subcommand %q", args[0])
	}
}

func adminChannelTest(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin channel test <id>")
	}
	if err := requireExactPositionals(args, 1); err != nil {
		return err
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
	cliout.Printf("%s\n", style.Bold(fmt.Sprintf(i18n.T("admin.channel.test_title"), id)))
	if len(res) == 0 {
		cliout.Println("  " + style.Dim(i18n.T("admin.channel.empty_result")))
		return nil
	}
	// Backend test result is a free-form map; sort the keys for a stable
	// order and align them into a dim-labelled column.
	keys := make([]string, 0, len(res))
	w := 0
	for k := range res {
		keys = append(keys, k)
		if x := style.Width(k); x > w {
			w = x
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		cliout.Printf("  %s  %s\n", style.Dim(padName(cliout.Sanitize(k)+":", w+1)), cliout.Sanitize(fmt.Sprintf("%v", res[k])))
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
	yes := fs.Bool("y", false, "skip confirmation")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if err := rejectPositionals(fs); err != nil {
		return err
	}
	if *enable == *disable {
		return errors.New("pass exactly one of --enable / --disable")
	}
	// This mutates EVERY channel carrying the tag in one shot — confirm
	// before firing (same gate as `admin user delete`). Non-interactive
	// callers must pass -y so a scripted bulk change is explicit, not
	// accidental.
	if !*yes {
		confirmKey := "admin.channel.tag_confirm_disable"
		if *enable {
			confirmKey = "admin.channel.tag_confirm_enable"
		}
		if !cliprompt.IsInteractive() {
			return errors.New(i18n.T("token.revoke_needs_confirm"))
		}
		ok, err := cliprompt.YesNo(
			bufio.NewReader(os.Stdin),
			fmt.Sprintf(i18n.T(confirmKey), tag),
			false,
		)
		if err != nil {
			return err
		}
		if !ok {
			cliout.Println(mutedText("common.canceled"))
			return nil
		}
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if err := client.AdminTagChannels(cliout.WithCtx(), tag, *enable); err != nil {
		return classifyErr(err)
	}
	key := "admin.channel.tag_disabled"
	if *enable {
		key = "admin.channel.tag_enabled"
	}
	cliout.Printf(okText(key)+"\n", tag)
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
	if err := rejectPositionals(fs); err != nil {
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
		cliout.Println(i18n.T("log.no_rows"))
		return nil
	}
	cliout.Printf(i18n.T("admin.common.rows_total")+"\n", len(rows), total)
	for _, r := range rows {
		ts := time.Unix(r.CreatedAt, 0).Format("01-02 15:04:05")
		// Per-request log lines keep the compact key=value shape (grep-
		// friendly); quota stays in raw units — rounding tiny per-call
		// amounts to USD cents would collapse them all to $0.00 — but
		// gets thousands separators so it's not a wall of digits.
		cliout.Printf("  %s  uid=%s  model=%s  quota=%s  tokens=%d/%d  ch=#%d\n",
			style.Dim(ts), sanitize(r.Username), sanitize(r.ModelName), commaInt(int64(r.Quota)), r.PromptTokens, r.CompletionTokens, r.ChannelID)
		if r.Content != "" {
			cliout.Printf("    %s\n", sanitize(r.Content))
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
			if n < 0 || n > 36500 {
				return 0, fmt.Errorf("--since: %q day count out of range (0-36500)", s)
			}
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
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println("usage: everyapi admin abuse {list|show|update}")
		return nil
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
	if err := rejectPositionals(fs); err != nil {
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
		cliout.Println(i18n.T("admin.abuse.no_reports"))
		return nil
	}
	cliout.Printf(i18n.T("admin.common.rows_total")+"\n", len(rows), total)
	cols := []tcol{
		{"ID", true},
		{i18n.T("admin.abuse.f_filed"), false},
		{i18n.T("admin.abuse.f_category"), false},
		{i18n.T("admin.abuse.f_target_type"), false},
		{i18n.T("admin.abuse.f_status"), false},
		{i18n.T("admin.abuse.f_reporter"), false},
	}
	cells := make([][]string, len(rows))
	for i, r := range rows {
		by := sanitize(r.ReporterEmail)
		if ip := sanitize(r.ReporterIP); ip != "" {
			if by != "" {
				by += " "
			}
			by += "(" + ip + ")"
		}
		cells[i] = []string{
			fmt.Sprintf("#%d", r.ID),
			time.Unix(r.CreatedAt, 0).Format("01-02 15:04"),
			sanitize(r.Category), sanitize(r.TargetType), sanitize(r.Status), by,
		}
	}
	printTable(cols, cells, nil)
	return nil
}

func adminAbuseShow(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin abuse show <id>")
	}
	if err := requireExactPositionals(args, 1); err != nil {
		return err
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
	var d detail
	d.add("admin.abuse.f_status", sanitize(r.Status))
	d.add("admin.abuse.f_filed", time.Unix(r.CreatedAt, 0).Format("2006-01-02 15:04:05"))
	d.add("admin.abuse.f_reporter", fmt.Sprintf("%s (%s) uid=%d", sanitize(r.ReporterEmail), sanitize(r.ReporterIP), r.ReporterUID))
	d.add("admin.abuse.f_category", sanitize(r.Category))
	d.add("admin.abuse.f_target_type", sanitize(r.TargetType))
	d.add("admin.abuse.f_target_id", sanitize(r.TargetID))
	d.add("admin.abuse.f_evidence", sanitize(r.EvidenceURL))
	d.add("admin.abuse.f_description", sanitize(r.Description))
	d.add("admin.abuse.f_note", sanitize(r.AdminNote))
	printDetail("admin.abuse.detail_title", r.ID, d)
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
	if err := rejectPositionals(fs); err != nil {
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
	cliout.Printf(okText("admin.abuse.updated")+"\n", id)
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
	if err := rejectPositionals(fs); err != nil {
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
		cliout.Println(i18n.T("admin.audit.no_rows"))
		return nil
	}
	cliout.Printf(i18n.T("admin.common.rows_total")+"\n", len(rows), total)
	for _, r := range rows {
		when := time.Unix(r.CreatedAt, 0).Format("01-02 15:04:05")
		// Label-free "·"-joined form (no English field tags): time ·
		// action · actor (#id) · target. Values are backend strings.
		cliout.Printf("  %s · %s · %s (#%d) · %s/%s\n",
			style.Dim(when), sanitize(r.Action), sanitize(r.ActorName), r.ActorID, sanitize(r.TargetType), sanitize(r.TargetID))
		if r.Payload != "" {
			line := sanitize(r.Payload)
			if r := []rune(line); len(r) > 200 {
				line = string(r[:200]) + "…"
			}
			cliout.Printf("    %s\n", style.Dim(line))
		}
	}
	return nil
}
