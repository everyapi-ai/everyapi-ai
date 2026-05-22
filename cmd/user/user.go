// Package user wires `everyapi user …` — profile / 2FA / passkey
// status / OAuth bindings / affiliate code. Operations that need a
// browser (passkey register, 2FA setup QR scan, email verification)
// are intentionally out of scope; the CLI surfaces what it can do
// over a bearer-token session and points at the dashboard for the
// rest.
package user

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
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(i18n.T("user.usage"))
		if len(args) == 0 {
			return fmt.Errorf(i18n.T("common.missing_subcommand"), "everyapi user")
		}
		return nil
	}
	switch args[0] {
	case "info":
		return runInfo(args[1:])
	case "2fa":
		return runTwoFA(args[1:])
	case "passkey":
		return runPasskey(args[1:])
	case "oauth":
		return runOAuth(args[1:])
	case "aff":
		return runAff(args[1:])
	default:
		cliout.Println(i18n.T("user.usage"))
		return fmt.Errorf(i18n.T("common.unknown_subcommand"), "user", args[0])
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

// --- info -----------------------------------------------------

func runInfo(args []string) error {
	fs := flag.NewFlagSet("user info", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	ctx := cliout.WithCtx()
	self, err := client.GetSelf(ctx)
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("user.account_header")+"\n", self.ID, self.Username, self.Email)
	cliout.Printf("  role:           %d\n", self.Role)
	cliout.Printf("  quota:          remain=%d  used=%d  requests=%d\n", self.Quota, self.UsedQuota, self.RequestCount)
	if self.SellerQuota > 0 {
		cliout.Printf("  seller earnings (pending): %d\n", self.SellerQuota)
	}

	// 2FA / passkey / bindings are best-effort — each may 401 on a
	// pure bearer-token session if backend gates that surface on the
	// dashboard cookie. Render the failures as "(unknown — N/A on
	// CLI session)" rather than killing the whole info command.
	if st, err := client.Get2FAStatus(ctx); err == nil {
		extra := ""
		if st.Enabled {
			extra = fmt.Sprintf(", backup codes remaining: %d", st.BackupCodesRemaining)
			if st.Locked {
				extra = " (LOCKED)" + extra
			}
		}
		cliout.Printf("  2fa:            enabled=%v%s\n", st.Enabled, extra)
	}
	if ps, err := client.GetPasskeyStatus(ctx); err == nil {
		if ps.Enabled {
			cliout.Printf("  passkey:        registered (last used %s)\n", formatUnixOrNever(ps.LastUsedAt))
		} else {
			cliout.Println("  passkey:        not registered")
		}
	}
	if bs, err := client.ListOAuthBindings(ctx); err == nil {
		if len(bs) == 0 {
			cliout.Println("  oauth bindings: (none)")
		} else {
			cliout.Printf("  oauth bindings: %d\n", len(bs))
			for _, b := range bs {
				cliout.Printf("    - [#%d] %s (%s)\n", b.ProviderID, b.ProviderName, b.ProviderSlug)
			}
		}
	}
	if aff, err := client.GetAffCode(ctx); err == nil && aff != "" {
		cliout.Printf("  aff code:       %s\n", aff)
	}
	return nil
}

func formatUnixOrNever(ts int64) string {
	if ts <= 0 {
		return "never"
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

// --- 2fa ------------------------------------------------------

func runTwoFA(args []string) error {
	if len(args) == 0 {
		return runTwoFAStatus(nil)
	}
	switch args[0] {
	case "disable":
		return runTwoFADisable(args[1:])
	case "backup":
		return runTwoFABackup(args[1:])
	case "help", "--help", "-h":
		cliout.Println("everyapi user 2fa [disable|backup] — see 'everyapi user help'")
		return nil
	default:
		// Bare `user 2fa` (with anything else) → status
		return runTwoFAStatus(args)
	}
}

func runTwoFAStatus(args []string) error {
	fs := flag.NewFlagSet("user 2fa", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	st, err := client.Get2FAStatus(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("user.2fa_status")+"\n", st.Enabled, st.Locked)
	if st.Enabled {
		cliout.Printf(i18n.T("user.2fa_backup_remaining")+"\n", st.BackupCodesRemaining)
		if st.BackupCodesRemaining <= 1 {
			cliout.Println(i18n.T("user.2fa_low_hint"))
		}
	} else {
		cliout.Println(i18n.T("user.2fa_disabled_msg"))
	}
	return nil
}

func runTwoFADisable(args []string) error {
	fs := flag.NewFlagSet("user 2fa disable", flag.ContinueOnError)
	code := fs.String("code", "", "6-digit TOTP or backup code")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *code == "" {
		return errors.New("--code is required")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if err := client.Disable2FA(cliout.WithCtx(), *code); err != nil {
		return classifyErr(err)
	}
	cliout.Println(i18n.T("user.2fa_disabled"))
	return nil
}

func runTwoFABackup(args []string) error {
	fs := flag.NewFlagSet("user 2fa backup", flag.ContinueOnError)
	code := fs.String("code", "", "6-digit TOTP (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *code == "" {
		return errors.New("--code is required")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	codes, err := client.RegenerateBackupCodes(cliout.WithCtx(), *code)
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("user.backup_regenerated")+"\n", len(codes))
	for _, c := range codes {
		cliout.Printf("  %s\n", c)
	}
	return nil
}

// --- passkey --------------------------------------------------

func runPasskey(args []string) error {
	fs := flag.NewFlagSet("user passkey", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	ps, err := client.GetPasskeyStatus(cliout.WithCtx())
	if err != nil {
		if api.IsUnauthorized(err) {
			return errors.New(i18n.T("user.passkey_dashboard_gated"))
		}
		return classifyErr(err)
	}
	if ps.Enabled {
		cliout.Printf(i18n.T("user.passkey_registered_last_used")+"\n", formatUnixOrNever(ps.LastUsedAt))
		cliout.Println(i18n.T("user.passkey_use_dashboard"))
	} else {
		cliout.Println(i18n.T("user.passkey_none"))
	}
	return nil
}

// --- oauth ----------------------------------------------------

func runOAuth(args []string) error {
	if len(args) == 0 {
		cliout.Println(i18n.T("user.oauth_usage"))
		return fmt.Errorf(i18n.T("common.missing_subcommand"), "everyapi user oauth")
	}
	switch args[0] {
	case "list":
		return runOAuthList(args[1:])
	case "unbind":
		return runOAuthUnbind(args[1:])
	default:
		return fmt.Errorf(i18n.T("common.unknown_subcommand"), "user oauth", args[0])
	}
}

func runOAuthList(args []string) error {
	fs := flag.NewFlagSet("user oauth list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	bs, err := client.ListOAuthBindings(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	if len(bs) == 0 {
		cliout.Println(i18n.T("user.no_bindings"))
		return nil
	}
	cliout.Printf(i18n.T("user.bindings_count")+"\n", len(bs))
	for _, b := range bs {
		cliout.Printf("  [#%d] %s (%s) — provider user id: %s\n", b.ProviderID, b.ProviderName, b.ProviderSlug, b.ProviderUserID)
	}
	return nil
}

func runOAuthUnbind(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi user oauth unbind <provider_id>")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid provider id %q", args[0])
	}
	fs := flag.NewFlagSet("user oauth unbind", flag.ContinueOnError)
	yes := fs.Bool("y", false, "skip confirmation")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if !*yes && cliprompt.IsInteractive() {
		ok, err := cliprompt.YesNo(
			bufio.NewReader(os.Stdin),
			fmt.Sprintf(i18n.T("user.unbind_confirm"), id),
			false,
		)
		if err != nil {
			return err
		}
		if !ok {
			cliout.Println(i18n.T("common.canceled"))
			return nil
		}
	}
	if err := client.UnbindOAuth(cliout.WithCtx(), id); err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("user.unbound")+"\n", id)
	return nil
}

// --- aff ------------------------------------------------------

func runAff(args []string) error {
	client, err := newClient()
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "reset" {
		newCode, err := client.ResetAffCode(cliout.WithCtx())
		if err != nil {
			return classifyErr(err)
		}
		cliout.Printf(i18n.T("user.aff_new")+"\n", newCode)
		cliout.Println(i18n.T("user.aff_rotated_warning"))
		return nil
	}
	code, err := client.GetAffCode(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("user.aff_code")+"\n", code)
	return nil
}
