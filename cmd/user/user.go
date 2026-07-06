// Package user wires `everyapi account user …` — profile / 2FA / passkey
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
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mdp/qrterminal/v3"
	"golang.org/x/term"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(i18n.T("user.usage"))
		if len(args) == 0 {
			return fmt.Errorf(i18n.T("common.missing_subcommand"), "everyapi account user")
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
	case "update":
		return runUpdate(args[1:])
	case "passwd":
		return runPasswd(args[1:])
	case "setting":
		return runSetting(args[1:])
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
	cliout.Printf(i18n.T("user.account_header")+"\n", self.ID, cliout.Sanitize(self.Username), cliout.Sanitize(self.Email))
	cliout.Printf("  role:           %d\n", self.Role)
	cliout.Printf("  quota:          remain=%d  used=%d  requests=%d\n", self.Quota, self.UsedQuota, self.RequestCount)
	if self.SellerQuota > 0 {
		cliout.Printf("  seller earnings (pending): %d\n", self.SellerQuota)
	}

	// 2FA / passkey / bindings are best-effort — each may 401 on a
	// pure bearer-token session if backend gates that surface on the
	// dashboard cookie. Render the failures as "(unknown — N/A on
	// CLI session)" rather than killing the whole info command OR
	// silently dropping the line (which makes a real backend outage
	// look identical to the feature being unset).
	const unknown = "(unknown — N/A on CLI session)"
	if st, err := client.Get2FAStatus(ctx); err == nil {
		extra := ""
		if st.Enabled {
			extra = fmt.Sprintf(", backup codes remaining: %d", st.BackupCodesRemaining)
			if st.Locked {
				extra = " (LOCKED)" + extra
			}
		}
		cliout.Printf("  2fa:            enabled=%v%s\n", st.Enabled, extra)
	} else {
		cliout.Printf("  2fa:            %s\n", unknown)
	}
	if ps, err := client.GetPasskeyStatus(ctx); err == nil {
		if ps.Enabled {
			cliout.Printf("  passkey:        registered (last used %s)\n", formatUnixOrNever(ps.LastUsedAt))
		} else {
			cliout.Println("  passkey:        not registered")
		}
	} else {
		cliout.Printf("  passkey:        %s\n", unknown)
	}
	if bs, err := client.ListOAuthBindings(ctx); err == nil {
		if len(bs) == 0 {
			cliout.Println("  oauth bindings: (none)")
		} else {
			cliout.Printf("  oauth bindings: %d\n", len(bs))
			for _, b := range bs {
				cliout.Printf("    - [#%d] %s (%s)\n", b.ProviderID, cliout.Sanitize(b.ProviderName), cliout.Sanitize(b.ProviderSlug))
			}
		}
	} else {
		cliout.Printf("  oauth bindings: %s\n", unknown)
	}
	// aff code: only shown when present; an error is non-actionable
	// noise here (affiliate code is optional), so keep omitting on
	// failure — but distinguish it from the gated surfaces above,
	// which the user might otherwise think are simply unset.
	if aff, err := client.GetAffCode(ctx); err == nil && aff != "" {
		cliout.Printf("  aff code:       %s\n", cliout.Sanitize(aff))
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
	case "enable":
		return runTwoFAEnable(args[1:])
	case "disable":
		return runTwoFADisable(args[1:])
	case "backup":
		return runTwoFABackup(args[1:])
	case "help", "--help", "-h":
		cliout.Println("everyapi account user 2fa [enable|disable|backup] — see 'everyapi account user help'")
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

// runTwoFAEnable runs the two-step enrollment: Setup2FA mints a secret
// + otpauth URI + backup codes (persisting a DISABLED row), we render
// the QR / secret / backup codes, then Enable2FA flips it on once the
// user types the 6-digit code from their authenticator. The secret and
// backup codes are only ever written to the terminal, never logged.
func runTwoFAEnable(args []string) error {
	fs := flag.NewFlagSet("user 2fa enable", flag.ContinueOnError)
	noQR := fs.Bool("no-qr", false, "skip the terminal QR; show the secret to type instead")
	codeFlag := fs.String("code", "", "TOTP code (non-interactive: skip the prompt)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	ctx := cliout.WithCtx()

	setup, err := client.Setup2FA(ctx)
	if err != nil {
		return classifyErr(err)
	}

	cliout.Println(i18n.T("user.2fa_enroll_intro"))
	cliout.Println("")
	if !*noQR && cliprompt.IsInteractive() {
		cliout.Println(i18n.T("user.2fa_scan_hint"))
		cliout.Println("")
		qrterminal.GenerateHalfBlock(setup.QRCodeData, qrterminal.L, cliout.Out)
		cliout.Println("")
	}
	cliout.Printf(i18n.T("user.2fa_secret")+"\n", setup.Secret)
	cliout.Println("")
	cliout.Println(i18n.T("user.2fa_backup_intro"))
	for _, bc := range setup.BackupCodes {
		cliout.Printf("  %s\n", bc)
	}
	cliout.Println("")

	code := strings.TrimSpace(*codeFlag)
	if code == "" {
		if !cliprompt.IsInteractive() {
			return errors.New(i18n.T("user.2fa_enable_need_code"))
		}
		entered, err := cliprompt.Line(bufio.NewReader(os.Stdin), i18n.T("user.2fa_enter_code"), "")
		if err != nil {
			return err
		}
		code = strings.TrimSpace(entered)
	}
	if code == "" {
		return errors.New(i18n.T("user.2fa_enable_need_code"))
	}
	if err := client.Enable2FA(ctx, code); err != nil {
		return classifyErr(err)
	}
	cliout.Println(i18n.T("user.2fa_enabled_ok"))
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
		return fmt.Errorf(i18n.T("common.missing_subcommand"), "everyapi account user oauth")
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
		cliout.Printf("  [#%d] %s (%s) — provider user id: %s\n", b.ProviderID, cliout.Sanitize(b.ProviderName), cliout.Sanitize(b.ProviderSlug), cliout.Sanitize(b.ProviderUserID))
	}
	return nil
}

func runOAuthUnbind(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi account user oauth unbind <provider_id>")
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
	if !*yes {
		if !cliprompt.IsInteractive() {
			// Destructive + no TTY to confirm on: fail closed rather than
			// silently unbinding. Require explicit -y for non-interactive use.
			return errors.New("refusing to unbind without confirmation; pass -y to unbind non-interactively")
		}
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
	if len(args) > 0 && args[0] == "transfer" {
		return runAffTransfer(client, args[1:])
	}
	code, err := client.GetAffCode(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("user.aff_code")+"\n", code)
	return nil
}

// runAffTransfer moves affiliate-reward quota into the main balance —
// the affiliate-side mirror of `seller withdraw`. Amount is the first
// positional arg (gateway quota units); -y skips the confirm.
func runAffTransfer(client *api.Client, args []string) error {
	// Accept the amount and the confirm-skip flag in any order — the only
	// flag here is -y (with --y / --yes aliases, matching `seller`), so
	// splitting it from the positional amount by hand keeps both
	// `aff transfer -y 1000` and `aff transfer 1000 -y` working (stdlib
	// flag would mis-read the amount-first form's trailing flag).
	yes := false
	var positional []string
	for _, a := range args {
		if a == "-y" || a == "--y" || a == "--yes" {
			yes = true
			continue
		}
		positional = append(positional, a)
	}
	if len(positional) == 0 {
		return errors.New(i18n.T("user.aff_transfer_usage"))
	}
	amount, err := strconv.Atoi(positional[0])
	if err != nil || amount <= 0 {
		return fmt.Errorf(i18n.T("user.aff_transfer_bad_amount"), positional[0])
	}
	// Render the amount in USD (like `seller withdraw` / `status`) so the
	// confirm + result speak the same units as the rest of the CLI — a
	// user typing too small an amount sees "$0.00" and backs out instead
	// of bouncing off the backend's $1 minimum. perUnit is a free
	// /api/status round-trip; fall back to raw DB units on the rare
	// failure rather than blocking the transfer.
	perUnit := 1.0
	if status, sErr := client.GetStatus(cliout.WithCtx()); sErr == nil && status.QuotaPerUnit > 0 {
		perUnit = status.QuotaPerUnit
	}
	usd := float64(amount) / perUnit
	if !yes {
		if !cliprompt.IsInteractive() {
			// Financial + no TTY to confirm on: fail closed rather than
			// silently transferring. Require explicit -y for non-interactive use.
			return errors.New("refusing to transfer without confirmation; pass -y to transfer non-interactively")
		}
		ok, err := cliprompt.YesNo(bufio.NewReader(os.Stdin), fmt.Sprintf(i18n.T("user.aff_transfer_confirm"), usd, amount), false)
		if err != nil {
			return err
		}
		if !ok {
			cliout.Println(i18n.T("common.canceled"))
			return nil
		}
	}
	if err := client.TransferAffQuota(cliout.WithCtx(), amount); err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("user.aff_transfer_ok")+"\n", usd)
	return nil
}

// --- update / passwd ------------------------------------------

// runUpdate edits the profile fields the generic PUT /api/user/self
// branch honors: username and display name. Password lives in its own
// `passwd` verb so the original-password prompt stays out of argv.
func runUpdate(args []string) error {
	fs := flag.NewFlagSet("user update", flag.ContinueOnError)
	username := fs.String("username", "", "new username (max 20 chars)")
	displayName := fs.String("display-name", "", "new display name (max 20 chars)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	u := strings.TrimSpace(*username)
	d := strings.TrimSpace(*displayName)
	if u == "" && d == "" {
		return errors.New(i18n.T("user.update_no_fields"))
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if err := client.UpdateProfile(cliout.WithCtx(), api.UpdateProfileRequest{
		Username:    u,
		DisplayName: d,
	}); err != nil {
		return classifyErr(err)
	}
	cliout.Println(i18n.T("user.update_ok"))
	return nil
}

// runPasswd changes the account password. Both the current and the new
// password are read with echo off (golang.org/x/term) so they never
// land in the scrollback or a shell history. The backend verifies the
// current password before applying the change.
func runPasswd(args []string) error {
	fs := flag.NewFlagSet("user passwd", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !cliprompt.IsInteractive() {
		return errors.New(i18n.T("user.passwd_interactive_only"))
	}
	oldPw, err := readSecret(i18n.T("user.passwd_old"))
	if err != nil {
		return err
	}
	newPw, err := readSecret(i18n.T("user.passwd_new"))
	if err != nil {
		return err
	}
	confirm, err := readSecret(i18n.T("user.passwd_confirm"))
	if err != nil {
		return err
	}
	if newPw == "" {
		return errors.New(i18n.T("user.passwd_empty"))
	}
	if newPw != confirm {
		return errors.New(i18n.T("user.passwd_mismatch"))
	}
	if n := utf8.RuneCountInString(newPw); n < 8 || n > 20 {
		return errors.New(i18n.T("user.passwd_length"))
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if err := client.UpdateProfile(cliout.WithCtx(), api.UpdateProfileRequest{
		Password:         newPw,
		OriginalPassword: oldPw,
	}); err != nil {
		return classifyErr(err)
	}
	cliout.Println(i18n.T("user.passwd_ok"))
	return nil
}

// readSecret prompts and reads one line with terminal echo disabled.
func readSecret(prompt string) (string, error) {
	fmt.Fprint(cliout.Out, prompt+" ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	cliout.Println("")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// --- setting (quota-warning notifications) --------------------

// runSetting shows or updates the quota-warning notification channel.
// Bare `user setting` prints the current config; `user setting test`
// fires a test message; `user setting --type <ch> ...` rewrites it.
//
// The backend rebuilds the whole setting blob on each write, so this
// relies on the server-side fix that preserves the non-notify fields
// (sidebar / language / seller-mode / marketplace opt-in).
func runSetting(args []string) error {
	if len(args) > 0 && args[0] == "test" {
		return runSettingTest(args[1:])
	}
	fs := flag.NewFlagSet("user setting", flag.ContinueOnError)
	notifyType := fs.String("type", "", "channel: email | webhook | bark | gotify")
	threshold := fs.Float64("threshold", 0, "quota-warning threshold (must be > 0)")
	email := fs.String("email", "", "notification email (type=email)")
	webhookURL := fs.String("webhook-url", "", "webhook URL (type=webhook)")
	webhookSecret := fs.String("webhook-secret", "", "webhook secret (type=webhook, optional)")
	barkURL := fs.String("bark-url", "", "Bark URL (type=bark)")
	gotifyURL := fs.String("gotify-url", "", "Gotify URL (type=gotify)")
	gotifyToken := fs.String("gotify-token", "", "Gotify token (type=gotify)")
	gotifyPriority := fs.Int("gotify-priority", 5, "Gotify priority 0-10 (type=gotify)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	// No --type → show the current config.
	if *notifyType == "" {
		return showSetting(client)
	}
	switch *notifyType {
	case "email", "webhook", "bark", "gotify":
	default:
		return fmt.Errorf(i18n.T("user.setting_bad_type"), *notifyType)
	}
	if *threshold <= 0 {
		return errors.New(i18n.T("user.setting_threshold_required"))
	}
	if err := client.UpdateNotifySetting(cliout.WithCtx(), api.NotifySettingRequest{
		NotifyType:            *notifyType,
		QuotaWarningThreshold: *threshold,
		NotificationEmail:     strings.TrimSpace(*email),
		WebhookURL:            strings.TrimSpace(*webhookURL),
		WebhookSecret:         *webhookSecret,
		BarkURL:               strings.TrimSpace(*barkURL),
		GotifyURL:             strings.TrimSpace(*gotifyURL),
		GotifyToken:           strings.TrimSpace(*gotifyToken),
		GotifyPriority:        *gotifyPriority,
	}); err != nil {
		return classifyErr(err)
	}
	cliout.Println(i18n.T("user.setting_saved"))
	return nil
}

func showSetting(client *api.Client) error {
	v, err := client.GetNotifySetting(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	if v.NotifyType == "" {
		cliout.Println(i18n.T("user.setting_none"))
		return nil
	}
	cliout.Printf(i18n.T("user.setting_header")+"\n", v.NotifyType)
	cliout.Printf("  %-12s %g\n", i18n.T("user.setting_threshold"), v.QuotaWarningThreshold)
	switch v.NotifyType {
	case "email":
		cliout.Printf("  %-12s %s\n", "email", v.NotificationEmail)
	case "webhook":
		cliout.Printf("  %-12s %s\n", "webhook", v.WebhookURL)
	case "bark":
		cliout.Printf("  %-12s %s\n", "bark", v.BarkURL)
	case "gotify":
		cliout.Printf("  %-12s %s\n", "gotify", v.GotifyURL)
	}
	return nil
}

func runSettingTest(args []string) error {
	fs := flag.NewFlagSet("user setting test", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if err := client.TestNotification(cliout.WithCtx()); err != nil {
		return classifyErr(err)
	}
	cliout.Println(i18n.T("user.setting_test_sent"))
	return nil
}
