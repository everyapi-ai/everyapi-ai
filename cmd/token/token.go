// Package token wires `everyapi token …` — CRUD over the relay API
// tokens a buyer issues to their own apps. The dispatcher mirrors
// cmd/seller: one verb per subcommand, each a thin handler around an
// api.Client method. The full key (sk-everyapi-…) is only printed
// from `token key <id>` so the audit trail in the backend lines up
// 1:1 with explicit user intent.
package token

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// Run is the dispatcher registered in main.go. Behaves like
// cmd/seller's Run: bare invocation prints help + errors so the
// launcher's sub-picker isn't masked by a successful no-op.
func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(i18n.T("token.usage"))
		if len(args) == 0 {
			return fmt.Errorf(i18n.T("common.missing_subcommand"), "everyapi token")
		}
		return nil
	}
	switch args[0] {
	case "list":
		return runList(args[1:])
	case "show":
		return runShow(args[1:])
	case "key":
		return runKey(args[1:])
	case "create":
		return runCreate(args[1:])
	case "update":
		return runUpdate(args[1:])
	case "enable":
		return runSetStatus(args[1:], api.TokenStatusEnabled)
	case "disable":
		return runSetStatus(args[1:], api.TokenStatusDisabled)
	case "revoke":
		return runRevoke(args[1:])
	default:
		cliout.Println(i18n.T("token.usage"))
		return fmt.Errorf(i18n.T("common.unknown_subcommand"), "token", args[0])
	}
}

// --- shared helpers ---------------------------------------------------

func newClient() (*api.Client, *config.Credentials, error) {
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return nil, nil, errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return nil, nil, err
	}
	return api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID), creds, nil
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

func parseID(args []string, verb string) (int, []string, error) {
	if len(args) == 0 {
		return 0, nil, fmt.Errorf(i18n.T("token.usage_arg_id"), verb)
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return 0, nil, fmt.Errorf(i18n.T("token.invalid_id"), args[0])
	}
	return id, args[1:], nil
}

func statusLabel(s int) string {
	switch s {
	case api.TokenStatusEnabled:
		return i18n.T("token.status_enabled")
	case api.TokenStatusDisabled:
		return i18n.T("token.status_disabled")
	case api.TokenStatusExpired:
		return i18n.T("token.status_expired")
	case api.TokenStatusExhausted:
		return i18n.T("token.status_exhausted")
	default:
		return fmt.Sprintf("status=%d", s)
	}
}

func expiresLabel(t int64) string {
	if t == api.TokenExpiresNever {
		return i18n.T("token.label.expires_never")
	}
	return time.Unix(t, 0).Format("2006-01-02 15:04:05")
}

// --- list -------------------------------------------------------------

func runList(args []string) error {
	fs := flag.NewFlagSet("token list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, _, err := newClient()
	if err != nil {
		return err
	}
	toks, err := client.ListTokens(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	if len(toks) == 0 {
		cliout.Println(i18n.T("token.no_tokens"))
		return nil
	}
	sort.Slice(toks, func(i, j int) bool { return toks[i].ID < toks[j].ID })
	cliout.Printf(i18n.T("token.count")+"\n", len(toks))
	for _, t := range toks {
		group := t.Group
		if group == "" {
			group = "(auto)"
		}
		cliout.Printf("  [#%d] %s — %s, group=%s\n",
			t.ID, t.Name, statusLabel(t.Status), group)
	}
	cliout.Println(i18n.T("token.full_keys_hint"))
	return nil
}

// --- show -------------------------------------------------------------

func runShow(args []string) error {
	id, _, err := parseID(args, "show")
	if err != nil {
		return err
	}
	client, _, err := newClient()
	if err != nil {
		return err
	}
	t, err := client.GetToken(cliout.WithCtx(), id)
	if err != nil {
		return classifyErr(err)
	}
	allowIPs := ""
	if t.AllowIPs != nil {
		allowIPs = *t.AllowIPs
	}
	cliout.Printf(i18n.T("token.label.id")+"\n", t.ID)
	cliout.Printf("  %-12s %s\n", i18n.T("token.label.name"), t.Name)
	cliout.Printf("  %-12s "+i18n.T("token.label.key_masked")+"\n", i18n.T("token.label.key"), t.Key)
	cliout.Printf("  %-12s %s\n", i18n.T("token.label.status"), statusLabel(t.Status))
	cliout.Printf("  %-12s %s\n", i18n.T("token.label.group"), emptyAs(t.Group, i18n.T("token.label.auto")))
	cliout.Printf("  %-12s %s\n", i18n.T("token.label.created"), time.Unix(t.CreatedTime, 0).Format("2006-01-02 15:04:05"))
	cliout.Printf("  %-12s %s\n", i18n.T("token.label.last_used"), time.Unix(t.AccessedTime, 0).Format("2006-01-02 15:04:05"))
	cliout.Printf("  %-12s %s\n", i18n.T("token.label.expires"), expiresLabel(t.ExpiredTime))
	if t.UnlimitedQuota {
		cliout.Printf("  %-12s "+i18n.T("token.label.unlimited")+"\n", i18n.T("token.label.quota"), t.UsedQuota)
	} else {
		cliout.Printf("  %-12s "+i18n.T("token.label.quota_split")+"\n", i18n.T("token.label.quota"), t.RemainQuota, t.UsedQuota)
	}
	if t.ModelLimitsEnabled {
		cliout.Printf("  %-12s %s\n", i18n.T("token.label.models"), t.ModelLimits)
	} else {
		cliout.Printf("  %-12s %s\n", i18n.T("token.label.models"), i18n.T("token.label.all"))
	}
	cliout.Printf("  %-12s %s\n", i18n.T("token.label.allow_ips"), emptyAs(allowIPs, i18n.T("token.label.any")))
	cliout.Printf("  %-12s %v\n", i18n.T("token.label.cross_group"), t.CrossGroupRetry)
	if t.SpecificChannelID != nil {
		cliout.Printf("  %-12s #%d\n", i18n.T("token.label.pinned_ch"), *t.SpecificChannelID)
	}
	return nil
}

func emptyAs(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// --- key --------------------------------------------------------------

func runKey(args []string) error {
	id, _, err := parseID(args, "key")
	if err != nil {
		return err
	}
	client, _, err := newClient()
	if err != nil {
		return err
	}
	key, err := client.TokenKey(cliout.WithCtx(), id)
	if err != nil {
		return classifyErr(err)
	}
	// Single bare line so `everyapi token key 42 | pbcopy` is the
	// obvious one-shot for piping the key into another tool.
	cliout.Println(key)
	return nil
}

// --- create / update --------------------------------------------------

// tokenFlags binds the flags shared by create / update onto a flag.
// FlagSet and reports which were explicitly set by the caller. For
// update we need that distinction so an omitted flag preserves the
// stored value instead of zeroing it.
type tokenFlags struct {
	name        string
	group       string
	unlimited   bool
	quota       int
	expires     string
	models      string
	ip          string
	crossGroup  bool
	seen        map[string]bool
}

func (tf *tokenFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&tf.name, "name", "", "display name (required for create; max 50 chars)")
	fs.StringVar(&tf.group, "group", "", "routing group ('' = auto)")
	fs.BoolVar(&tf.unlimited, "unlimited", false, "no quota cap")
	fs.IntVar(&tf.quota, "quota", 0, "remaining quota in gateway units")
	fs.StringVar(&tf.expires, "expires", "", `"never" or absolute Unix seconds`)
	fs.StringVar(&tf.models, "models", "", "CSV of model ids ('' = all)")
	fs.StringVar(&tf.ip, "ip", "", "IP allowlist CSV ('' = no restriction)")
	fs.BoolVar(&tf.crossGroup, "cross-group", false, "allow cross-group retry (auto group only)")
}

func (tf *tokenFlags) parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	tf.seen = map[string]bool{}
	fs.Visit(func(f *flag.Flag) { tf.seen[f.Name] = true })
	return nil
}

// expiresValue parses --expires into the int64 the server stores.
// Empty string falls back to def. "never" is the documented sentinel
// for "no expiry"; everything else must be a Unix-seconds integer to
// stay scriptable — humans should compute the absolute timestamp
// upstream (e.g. `date -v+30d +%s`) rather than having the CLI guess
// timezones.
func expiresValue(s string, def int64) (int64, error) {
	if s == "" {
		return def, nil
	}
	if s == "never" {
		return api.TokenExpiresNever, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("--expires must be 'never' or a Unix-seconds integer, got %q", s)
	}
	return n, nil
}

func runCreate(args []string) error {
	fs := flag.NewFlagSet("token create", flag.ContinueOnError)
	tf := &tokenFlags{}
	tf.bind(fs)
	if err := tf.parse(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(tf.name) == "" {
		return errors.New(i18n.T("token.name_required"))
	}
	exp, err := expiresValue(tf.expires, api.TokenExpiresNever)
	if err != nil {
		return err
	}
	req := api.TokenCreate{
		Name:               tf.name,
		Group:              tf.group,
		ExpiredTime:        exp,
		UnlimitedQuota:     tf.unlimited,
		RemainQuota:        tf.quota,
		ModelLimitsEnabled: tf.models != "",
		ModelLimits:        tf.models,
		CrossGroupRetry:    tf.crossGroup,
	}
	if tf.ip != "" {
		req.AllowIPs = &tf.ip
	}
	client, _, err := newClient()
	if err != nil {
		return err
	}
	if err := client.CreateToken(cliout.WithCtx(), req); err != nil {
		return classifyErr(err)
	}
	cliout.Println(i18n.T("token.created"))
	return nil
}

func runUpdate(args []string) error {
	id, rest, err := parseID(args, "update")
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("token update", flag.ContinueOnError)
	tf := &tokenFlags{}
	tf.bind(fs)
	if err := tf.parse(fs, rest); err != nil {
		return err
	}
	if len(tf.seen) == 0 {
		return errors.New(i18n.T("token.update_no_flags"))
	}
	client, _, err := newClient()
	if err != nil {
		return err
	}
	// Read-modify-write: full PUT overwrites every field, so we need
	// the current row to preserve flags the user didn't pass. The
	// alternative — making each field optional in the backend — is a
	// much bigger surface change.
	cur, err := client.GetToken(cliout.WithCtx(), id)
	if err != nil {
		return classifyErr(err)
	}
	req := api.TokenUpdate{
		ID:                 cur.ID,
		Name:               cur.Name,
		Status:             cur.Status,
		ExpiredTime:        cur.ExpiredTime,
		RemainQuota:        cur.RemainQuota,
		UnlimitedQuota:     cur.UnlimitedQuota,
		ModelLimitsEnabled: cur.ModelLimitsEnabled,
		ModelLimits:        cur.ModelLimits,
		AllowIPs:           cur.AllowIPs,
		Group:              cur.Group,
		CrossGroupRetry:    cur.CrossGroupRetry,
		SpecificChannelID:  cur.SpecificChannelID,
	}
	if tf.seen["name"] {
		req.Name = tf.name
	}
	if tf.seen["group"] {
		req.Group = tf.group
	}
	if tf.seen["unlimited"] {
		req.UnlimitedQuota = tf.unlimited
	}
	if tf.seen["quota"] {
		req.RemainQuota = tf.quota
	}
	if tf.seen["expires"] {
		v, err := expiresValue(tf.expires, cur.ExpiredTime)
		if err != nil {
			return err
		}
		req.ExpiredTime = v
	}
	if tf.seen["models"] {
		req.ModelLimits = tf.models
		req.ModelLimitsEnabled = tf.models != ""
	}
	if tf.seen["ip"] {
		if tf.ip == "" {
			req.AllowIPs = nil
		} else {
			s := tf.ip
			req.AllowIPs = &s
		}
	}
	if tf.seen["cross-group"] {
		req.CrossGroupRetry = tf.crossGroup
	}
	out, err := client.UpdateToken(cliout.WithCtx(), req)
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("token.updated")+"\n", out.ID, statusLabel(out.Status))
	return nil
}

// --- enable / disable ------------------------------------------------

func runSetStatus(args []string, status int) (err error) {
	verb := "enable"
	if status == api.TokenStatusDisabled {
		verb = "disable"
	}
	id, _, err := parseID(args, verb)
	if err != nil {
		return err
	}
	client, _, err := newClient()
	if err != nil {
		return err
	}
	out, err := client.SetTokenStatus(cliout.WithCtx(), id, status)
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("token.status_changed")+"\n", out.ID, statusLabel(out.Status))
	return nil
}

// --- revoke ----------------------------------------------------------

func runRevoke(args []string) error {
	fs := flag.NewFlagSet("token revoke", flag.ContinueOnError)
	yes := fs.Bool("y", false, "skip the confirmation prompt")
	yesLong := fs.Bool("yes", false, "alias of -y")
	if len(args) == 0 {
		return errors.New(i18n.T("token.usage_revoke"))
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf(i18n.T("token.invalid_id"), args[0])
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	skip := *yes || *yesLong
	client, _, err := newClient()
	if err != nil {
		return err
	}
	if !skip && cliprompt.IsInteractive() {
		t, err := client.GetToken(cliout.WithCtx(), id)
		if err != nil {
			return classifyErr(err)
		}
		ok, err := cliprompt.YesNo(
			bufio.NewReader(os.Stdin),
			fmt.Sprintf(i18n.T("token.revoke_confirm"), t.ID, t.Name),
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
	if err := client.DeleteToken(cliout.WithCtx(), id); err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("token.revoked")+"\n", id)
	return nil
}
