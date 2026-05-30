package admin

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/style"
	"github.com/everyapi-ai/everyapi-sdk/api"
)

// --- admin redemption ---------------------------------------------
//
// CRUD over prepaid quota vouchers (/api/redemption, AdminAuth).
// `create` MINTS quota and is the only verb that returns the plaintext
// keys, so it prints them once; the two delete verbs are destructive
// and confirm before firing.

func adminRedemption(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin redemption {list|search|show|create|update|status|delete|clear-invalid}")
	}
	switch args[0] {
	case "list":
		return adminRedemptionList(args[1:])
	case "search":
		return adminRedemptionSearch(args[1:])
	case "show":
		return adminRedemptionShow(args[1:])
	case "create":
		return adminRedemptionCreate(args[1:])
	case "update":
		return adminRedemptionUpdate(args[1:])
	case "status":
		return adminRedemptionStatus(args[1:])
	case "delete":
		return adminRedemptionDelete(args[1:])
	case "clear-invalid":
		return adminRedemptionClearInvalid(args[1:])
	default:
		return fmt.Errorf("unknown 'admin redemption' subcommand %q", args[0])
	}
}

func redemptionStatusLabel(s int) string {
	switch s {
	case api.RedemptionStatusEnabled:
		return style.Color(i18n.T("admin.redemption.status_enabled"), style.ToneGreen)
	case api.RedemptionStatusDisabled:
		return style.Color(i18n.T("admin.redemption.status_disabled"), style.ToneGray)
	case api.RedemptionStatusUsed:
		return style.Color(i18n.T("admin.redemption.status_used"), style.ToneGray)
	default:
		return fmt.Sprintf("status=%d", s)
	}
}

// printRedemptionRows renders the voucher listing as an aligned table.
func printRedemptionRows(rows []api.Redemption, perUnit float64) {
	cols := []tcol{
		{"ID", true},
		{i18n.T("admin.redemption.f_name"), false},
		{i18n.T("admin.redemption.f_status"), false},
		{i18n.T("admin.redemption.f_quota"), true},
		{i18n.T("admin.redemption.f_expires"), false},
	}
	cells := make([][]string, len(rows))
	for i, r := range rows {
		exp := i18n.T("admin.redemption.never")
		if r.ExpiredTime > 0 {
			exp = time.Unix(r.ExpiredTime, 0).Format("2006-01-02")
		}
		cells[i] = []string{fmt.Sprintf("#%d", r.ID), r.Name, redemptionStatusLabel(r.Status), fmtQuota(int64(r.Quota), perUnit), exp}
	}
	printTable(cols, cells, nil)
}

func adminRedemptionList(args []string) error {
	fs := flag.NewFlagSet("admin redemption list", flag.ContinueOnError)
	page := fs.Int("page", 0, "1-based page")
	limit := fs.Int("limit", 20, "page size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, total, err := client.AdminListRedemptions(cliout.WithCtx(), *page, *limit)
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println(i18n.T("admin.redemption.none"))
		return nil
	}
	cliout.Printf(i18n.T("admin.common.rows_total")+"\n", len(rows), total)
	printRedemptionRows(rows, quotaPerUnit(client))
	return nil
}

func adminRedemptionSearch(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin redemption search <keyword>")
	}
	keyword := args[0]
	fs := flag.NewFlagSet("admin redemption search", flag.ContinueOnError)
	page := fs.Int("page", 0, "1-based page")
	limit := fs.Int("limit", 20, "page size")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, total, err := client.AdminSearchRedemptions(cliout.WithCtx(), keyword, *page, *limit)
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println(i18n.T("admin.redemption.none"))
		return nil
	}
	cliout.Printf(i18n.T("admin.common.matches_total")+"\n", len(rows), total)
	printRedemptionRows(rows, quotaPerUnit(client))
	return nil
}

func adminRedemptionShow(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin redemption show <id>")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid id %q", args[0])
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	r, err := client.AdminGetRedemption(cliout.WithCtx(), id)
	if err != nil {
		return classifyErr(err)
	}
	expires := i18n.T("admin.redemption.never")
	if r.ExpiredTime > 0 {
		expires = time.Unix(r.ExpiredTime, 0).Format("2006-01-02 15:04:05")
	}
	var d detail
	d.add("admin.redemption.f_key", r.Key)
	d.add("admin.redemption.f_name", r.Name)
	d.add("admin.redemption.f_status", redemptionStatusLabel(r.Status))
	d.add("admin.redemption.f_quota", fmtQuota(int64(r.Quota), quotaPerUnit(client)))
	d.add("admin.redemption.f_created", time.Unix(r.CreatedTime, 0).Format("2006-01-02 15:04:05"))
	d.add("admin.redemption.f_expires", expires)
	d.add("admin.redemption.f_used_by", r.UsedUsername)
	printDetail("admin.redemption.detail_title", r.ID, d)
	return nil
}

func adminRedemptionCreate(args []string) error {
	fs := flag.NewFlagSet("admin redemption create", flag.ContinueOnError)
	name := fs.String("name", "", "code name (1–20 chars; suffixed -NNN when count>1)")
	quota := fs.Int("quota", 0, "quota granted per code")
	count := fs.Int("count", 1, "how many codes to mint (1–100)")
	expires := fs.String("expires", "", "expiry: Unix seconds, or empty/never")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return errors.New("--name is required")
	}
	if *quota <= 0 {
		return errors.New("--quota must be > 0")
	}
	var expiredTime int64
	if *expires != "" && *expires != "never" {
		v, err := strconv.ParseInt(*expires, 10, 64)
		if err != nil {
			return fmt.Errorf("--expires must be Unix seconds or 'never': %q", *expires)
		}
		expiredTime = v
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	keys, err := client.AdminCreateRedemptions(cliout.WithCtx(), api.RedemptionCreateRequest{
		Name: *name, Count: *count, Quota: *quota, ExpiredTime: expiredTime,
	})
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(okText("admin.redemption.created")+"\n", len(keys))
	for _, k := range keys {
		cliout.Printf("  %s\n", k)
	}
	return nil
}

func adminRedemptionUpdate(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin redemption update <id> [--name N] [--quota Q] [--expires E]")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid id %q", args[0])
	}
	fs := flag.NewFlagSet("admin redemption update", flag.ContinueOnError)
	name := fs.String("name", "", "new name (omit to keep)")
	quota := fs.Int("quota", -1, "new quota (omit to keep)")
	expires := fs.String("expires", "", "new expiry: Unix seconds, 'never', or omit to keep")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	// Read the current row first: the backend's update writes name /
	// quota / expired_time wholesale, so omitted flags must carry the
	// existing values forward rather than zeroing them.
	cur, err := client.AdminGetRedemption(cliout.WithCtx(), id)
	if err != nil {
		return classifyErr(err)
	}
	upd := api.Redemption{ID: id, Name: cur.Name, Quota: cur.Quota, ExpiredTime: cur.ExpiredTime}
	if *name != "" {
		upd.Name = *name
	}
	if *quota >= 0 {
		upd.Quota = *quota
	}
	if *expires != "" {
		if *expires == "never" {
			upd.ExpiredTime = 0
		} else {
			v, err := strconv.ParseInt(*expires, 10, 64)
			if err != nil {
				return fmt.Errorf("--expires must be Unix seconds or 'never': %q", *expires)
			}
			upd.ExpiredTime = v
		}
	}
	if err := client.AdminUpdateRedemption(cliout.WithCtx(), upd); err != nil {
		return classifyErr(err)
	}
	cliout.Printf(okText("admin.redemption.updated")+"\n", id)
	return nil
}

func adminRedemptionStatus(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: everyapi admin redemption status <id> <enable|disable>")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid id %q", args[0])
	}
	var status int
	switch args[1] {
	case "enable":
		status = api.RedemptionStatusEnabled
	case "disable":
		status = api.RedemptionStatusDisabled
	default:
		return fmt.Errorf("status must be 'enable' or 'disable', got %q", args[1])
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if err := client.AdminSetRedemptionStatus(cliout.WithCtx(), id, status); err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("admin.redemption.status_set")+"\n", id, redemptionStatusLabel(status))
	return nil
}

func adminRedemptionDelete(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin redemption delete <id> [-y]")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid id %q", args[0])
	}
	fs := flag.NewFlagSet("admin redemption delete", flag.ContinueOnError)
	yes := fs.Bool("y", false, "skip confirmation")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if !*yes && cliprompt.IsInteractive() {
		ok, err := cliprompt.YesNo(bufio.NewReader(os.Stdin), fmt.Sprintf(i18n.T("admin.redemption.delete_confirm"), id), false)
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
	if err := client.AdminDeleteRedemption(cliout.WithCtx(), id); err != nil {
		return classifyErr(err)
	}
	cliout.Printf(okText("admin.redemption.deleted")+"\n", id)
	return nil
}

func adminRedemptionClearInvalid(args []string) error {
	fs := flag.NewFlagSet("admin redemption clear-invalid", flag.ContinueOnError)
	yes := fs.Bool("y", false, "skip confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*yes && cliprompt.IsInteractive() {
		ok, err := cliprompt.YesNo(bufio.NewReader(os.Stdin), i18n.T("admin.redemption.clear_confirm"), false)
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
	n, err := client.AdminDeleteInvalidRedemptions(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(okText("admin.redemption.cleared")+"\n", n)
	return nil
}
