package seller

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/sellertype"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// Seller is the top-level dispatcher for `everyapi seller …`. Each
// subcommand is a thin handler: load creds → call one or two API client
// methods → render. OAuth flows (`add-oauth claude/chatgpt`) are
// intentionally out of scope here — they need PKCE + a local listener
// + browser launch, which is its own follow-up release.
//
// Subcommands:
//
//	everyapi seller list                          List the user's mounted channels
//	everyapi seller withdraw [--quota <int>]      Transfer pending earnings to main balance
//	everyapi seller add-key   --type <T> --name <N> --key <K> --models <M>  [--remark <R>]
//	                                            POST /api/seller/channel with a plain API key
//	everyapi seller setup                         Interactive wizard wrapping add-key
//	everyapi seller help                          Print this help
func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(sellerUsage)
		if len(args) == 0 {
			return fmt.Errorf(i18n.T("common.missing_subcommand"), "everyapi seller")
		}
		return nil
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		return sellerList(rest)
	case "withdraw":
		return sellerWithdraw(rest)
	case "add-key":
		return sellerAddKey(rest)
	case "add-oauth":
		return sellerAddOAuth(rest)
	case "setup":
		return sellerSetup(rest)
	case "update":
		return sellerUpdate(rest)
	case "remove":
		return sellerRemove(rest)
	case "refresh":
		return sellerRefresh(rest)
	case "sales":
		return sellerSales(rest)
	case "eligibility":
		return sellerEligibility(rest)
	case "compensation":
		return sellerCompensation(rest)
	default:
		cliout.Printf("%s\n", sellerUsage)
		return fmt.Errorf(i18n.T("common.unknown_subcommand"), "seller", sub)
	}
}

const sellerUsage = `everyapi seller — channel-marketplace seller commands

USAGE
  everyapi seller <subcommand> [flags]

SUBCOMMANDS
  list                              List the channels you've mounted
  withdraw [--quota <int>]          Transfer pending seller earnings to main balance
                                    (no flag → transfer everything)
  add-key  --type <T> --name <N> --key <K> --models <M> [--remark <R>]
                                    Mount a channel with a plain API key
  add-oauth codex --name <N> --models <M> [--no-browser]
                                    Mount a channel via Codex device-authorization
                                    (one-click OAuth — the seller never copies a token)
  setup                             Interactive wizard: pick API key
                                    or OAuth (codex / claude / gemini),
                                    then walk through add-key / add-oauth
  update <id> [flags]               Edit name / models / status / remark / keys
  remove <id> [-y]                  Delete a channel (asks unless -y)
  refresh <id>                      Rotate the OAuth credential on an
                                    OAuth-mounted channel (codex/claude/gemini)
  sales [--page P] [--limit N]      Recent buyer-charge rows (anonymised)
  eligibility                       Show why you can / can't mount more channels
  compensation submit  --upstream <p> --description <d> [--proof <url>]
                                    File a compensation claim for an upstream outage
  compensation list   [--status pending|approved|rejected]
                                    List your filed claims
  help                              Show this message`

// ---- seller list ---------------------------------------------------

// sellerList prints every channel the user owns. Mirrors the MCP
// everyapi_seller_list output shape so a buyer who's been using the AI
// surface sees consistent data on the terminal.
func sellerList(args []string) error {
	fs := flag.NewFlagSet("seller list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, _, err := sellerClient()
	if err != nil {
		return err
	}
	channels, err := client.ListSellerChannels(cliout.WithCtx())
	if err != nil {
		return classifySellerErr(err)
	}
	if len(channels) == 0 {
		cliout.Println(i18n.T("seller.no_channels"))
		cliout.Println(i18n.T("seller.setup_hint"))
		return nil
	}
	cliout.Printf(i18n.T("seller.list_count")+"\n", len(channels))
	for _, ch := range channels {
		cliout.Printf("  [#%d] %s — type=%s status=%s\n",
			ch.ID, ch.Name, channelTypeLabel(ch.KindSlug), channelStatusLabel(ch.Status))
		if ch.Models != "" {
			cliout.Printf("        models: %s\n", ch.Models)
		}
	}
	return nil
}

// channelStatusLabel mirrors the MCP server's statusLabel; kept
// duplicated rather than imported across the cmd/mcp boundary so the
// two packages don't develop a cyclic dependency on a tiny helper.
func channelStatusLabel(s int) string {
	switch s {
	case 1:
		return "enabled"
	case 2:
		return "disabled (manual)"
	case 3:
		return "disabled (auto)"
	default:
		return fmt.Sprintf("status=%d", s)
	}
}

// ---- seller withdraw -----------------------------------------------

// sellerWithdraw moves SellerQuota → Quota. Empty --quota = "everything
// pending"; explicit --quota lets a power user partial-transfer in DB
// units (the same shape as the MCP tool's `quota` arg).
func sellerWithdraw(args []string) error {
	fs := flag.NewFlagSet("seller withdraw", flag.ContinueOnError)
	quota := fs.Int("quota", 0, "amount to transfer in DB units (omit for the full pending balance)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectPositionals(fs); err != nil {
		return err
	}
	// Distinguish an omitted --quota (sweep the full pending balance) from
	// an explicit value. Overloading amount==0 as the "full balance"
	// sentinel would turn `--quota 0` (or a caller whose amount computes
	// to 0 = "nothing") into a full sweep — the opposite of intent. Mirror
	// the fs.Visit pattern token/seller `update` already use.
	quotaSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "quota" {
			quotaSet = true
		}
	})
	if quotaSet && *quota <= 0 {
		return fmt.Errorf("--quota must be a positive amount (got %d); omit --quota to withdraw the full pending balance", *quota)
	}
	client, creds, err := sellerClient()
	if err != nil {
		return err
	}

	amount := *quota
	if !quotaSet {
		self, err := client.GetSelf(cliout.WithCtx())
		if err != nil {
			return classifySellerErr(err)
		}
		amount = self.SellerQuota
		if amount <= 0 {
			cliout.Println(i18n.T("seller.withdraw_nothing"))
			return nil
		}
	}

	if err := client.TransferSellerQuota(cliout.WithCtx(), amount); err != nil {
		return classifySellerErr(err)
	}

	// Render the transferred amount in USD so the user sees the same units
	// the dashboard / `everyapi auth status` use. perUnit is a free
	// /api/status round-trip; on failure (rare) report success WITHOUT
	// misrendering raw DB units as dollars.
	if status, sErr := client.GetStatus(cliout.WithCtx()); sErr == nil && status.QuotaPerUnit > 0 {
		cliout.Printf(i18n.T("seller.withdraw_done"), float64(amount)/status.QuotaPerUnit)
	} else {
		// No quota→USD divisor available: dividing raw DB units by 1.0 and
		// labelling the result "$" overstates the value by QuotaPerUnit
		// (default 500000), so a $1 withdrawal would print "$500000.00".
		// Show a unit-neutral line instead; the wallet link below carries
		// the real figure.
		cliout.Printf(i18n.T("seller.withdraw_done_no_usd"), amount)
	}
	cliout.Printf(i18n.T("seller.withdraw_check_url"), api.WebOriginFromBase(creds.APIBase))
	return nil
}

// ---- seller add-key ------------------------------------------------

// addKeyArgs is the parsed shape of `seller add-key` flags. Kept as a
// dedicated struct so parseAddKeyArgs can be tested without a network
// or filesystem.
//
// Keys/KeyRemarks: --key may be repeated to attach N credentials as a
// multi-key backup pool (B2, PRODUCT §4.5). --key-remark is optional
// and index-aligned: the i-th --key-remark labels the i-th --key.
// Aligning by position keeps the flag surface trivially scriptable
// without nesting (no `--key=foo:label` syntax to parse). Backend
// rejects OAuth blobs in a multi-key set; for those, supply a single
// --key.
type addKeyArgs struct {
	Type    string
	Name    string
	Keys    []string
	Remarks []string
	Models  string
	Remark  string
}

// stringSliceFlag is a flag.Value that accumulates repeated flag
// invocations into a slice (stdlib's flag has no built-in for this).
// Used for --key and --key-remark so the CLI accepts an arbitrary
// number of credentials in one invocation.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string     { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error { *s = append(*s, v); return nil }

// sellerAddKey wraps POST /api/seller/channel. The type is a human
// alias (openai / claude / gemini / …) resolved to the backend
// kind_slug via sellertype.Resolve; an unknown alias is rejected
// locally with the list of valid choices rather than forwarded to a
// backend 422.
func sellerAddKey(args []string) error {
	parsed, err := parseAddKeyArgs(args)
	if err != nil {
		return err
	}
	kindSlug, err := resolveSellerType(parsed.Type)
	if err != nil {
		return err
	}
	client, creds, err := sellerClient()
	if err != nil {
		return err
	}

	// Eligibility pre-check. Same reasoning as `seller add-oauth`:
	// failing the gate AFTER the seller typed out a real API key is a
	// worse experience than telling them up front which gate to fix.
	// A failed eligibility query (not a failed gate — actual transport
	// error) is non-fatal: fall through to the create call, which will
	// retry the same gates server-side and produce a coherent error.
	elig, err := client.GetSellerEligibility(cliout.WithCtx())
	if err == nil && !elig.Eligible {
		renderEligibility(elig)
		cliout.Println("")
		cliout.Println(i18n.T("seller.eligibility_check_failed"))
		cliout.Printf(i18n.T("seller.dashboard_url_line")+"\n", api.WebOriginFromBase(creds.APIBase))
		return errors.New(i18n.T("seller.not_eligible_error"))
	}

	id, err := client.CreateSellerChannel(cliout.WithCtx(), api.SellerChannelCreate{
		Name:       parsed.Name,
		KindSlug:   kindSlug,
		Keys:       parsed.Keys,
		KeyRemarks: parsed.Remarks,
		Models:     parsed.Models,
		Remark:     parsed.Remark,
	})
	if err != nil {
		return classifySellerErr(err)
	}
	pool := ""
	if len(parsed.Keys) > 1 {
		pool = fmt.Sprintf(i18n.T("seller.key_backup_pool"), len(parsed.Keys))
	}
	cliout.Printf(i18n.T("seller.mounted_with_pool"), id, parsed.Name, channelTypeLabel(kindSlug), pool)
	cliout.Println(i18n.T("seller.mounted_status_hint"))
	return nil
}

// parseAddKeyArgs validates the flag set. All four core flags are
// required because there's no sane default — picking a name / type /
// key / models is the seller decision the wizard helps with; this
// command is the non-interactive shortcut for scripted onboarding.
// --remark is the channel-level note (optional). --key may be repeated
// for a multi-key backup pool; --key-remark per-key labels are
// optional and index-aligned with --key (more --key-remark than --key
// is a hard error to catch typos early).
//
// Exported via package-internal name so cmd/seller_test.go can poke at
// it without hitting the network.
func parseAddKeyArgs(args []string) (*addKeyArgs, error) {
	fs := flag.NewFlagSet("seller add-key", flag.ContinueOnError)
	a := &addKeyArgs{}
	var keys stringSliceFlag
	var remarks stringSliceFlag
	fs.StringVar(&a.Type, "type", "", "upstream channel type alias (openai / claude / gemini / codex / vertex / aws / xai / deepseek)")
	fs.StringVar(&a.Name, "name", "", "channel display name (free-form)")
	fs.Var(&keys, "key", "upstream API key (per-key) — or a Vertex ADC JSON / AWS region+key blob. Repeat the flag for a multi-key backup pool, e.g. --key sk-a --key sk-b")
	fs.Var(&remarks, "key-remark", "per-key label, repeatable, aligned by position with --key (1st --key-remark labels the 1st --key). For the channel-wide note use --remark instead.")
	fs.StringVar(&a.Models, "models", "", "comma-separated models this channel serves (empty rejected — pick at least one)")
	fs.StringVar(&a.Remark, "remark", "", "channel-level note (one per channel, free-form). For per-key labels use --key-remark instead.")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if err := rejectPositionals(fs); err != nil {
		return nil, err
	}
	a.Keys = []string(keys)
	a.Remarks = []string(remarks)
	var missing []string
	if a.Type == "" {
		missing = append(missing, "--type")
	}
	if a.Name == "" {
		missing = append(missing, "--name")
	}
	if len(a.Keys) == 0 {
		missing = append(missing, "--key")
	}
	if a.Models == "" {
		missing = append(missing, "--models")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf(i18n.T("seller.oauth_missing_flags"), strings.Join(missing, ", "))
	}
	// More --key-remark than --key is a typo, not a feature: the extra
	// remarks would never be applied. Catch it here so the failure
	// message names the mismatch instead of the backend rejecting an
	// already-submitted form.
	if len(a.Remarks) > len(a.Keys) {
		return nil, fmt.Errorf("got %d --key-remark but only %d --key — each --key-remark labels one --key by position", len(a.Remarks), len(a.Keys))
	}
	// Duplicate --key value is also a typo. The backend's SetMultiKeySet
	// silently keeps the first occurrence ("first wins" — a dup is a
	// user error, not a state to split), which means a pasted-twice
	// credential would mount a channel that LOOKS like a 2-key pool but
	// actually carries one credential twice in storage and only one of
	// them in routing state. Surface it here so the seller can fix the
	// argv before submit.
	if len(a.Keys) > 1 {
		seen := make(map[string]bool, len(a.Keys))
		for _, k := range a.Keys {
			if seen[k] {
				return nil, fmt.Errorf("duplicate --key value — each credential must be distinct (the same key listed twice is a typo, not a backup)")
			}
			seen[k] = true
		}
	}
	return a, nil
}

// ---- seller setup --------------------------------------------------

// sellerSetup is the small-talk wizard for mounting a new seller
// channel. Asks the user up-front which auth method they want —
// raw API key, or one of the OAuth flows (Codex / Claude / Gemini)
// — and routes into either the inline add-key wizard (key path)
// or sellerAddOAuth (OAuth paths). Eligibility is checked once,
// here, so a failed gate surfaces before the user types anything.
//
// The OAuth branches forward the collected name + models via argv
// to sellerAddOAuth — same validator, same flow as a flag-driven
// `everyapi seller add-oauth <provider> --name N --models M` call.
// The add-oauth* handlers re-run the eligibility check internally;
// the duplicate round-trip is intentional (defense-in-depth: nothing
// downstream of this function trusts the caller has gated).
func sellerSetup(args []string) error {
	fs := flag.NewFlagSet("seller setup", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectPositionals(fs); err != nil {
		return err
	}
	client, creds, err := sellerClient()
	if err != nil {
		return err
	}

	elig, err := client.GetSellerEligibility(cliout.WithCtx())
	if err != nil {
		return classifySellerErr(err)
	}
	renderEligibility(elig)
	if !elig.Eligible {
		cliout.Println("")
		cliout.Println(i18n.T("seller.requirements_not_met"))
		cliout.Printf(i18n.T("seller.dashboard_url_line")+"\n", api.WebOriginFromBase(creds.APIBase))
		return nil
	}

	in := bufio.NewReader(os.Stdin)
	cliout.Println("")
	cliout.Println(i18n.T("seller.mounting_new_channel"))
	cliout.Println("")

	// Auth-method picker. Index → branch so the labels stay free
	// to grow without breaking string-match branching.
	methodIdx, err := cliprompt.Pick(i18n.T("seller.method_picker_label"),
		[]string{
			i18n.T("seller.method_api_key"),
			i18n.T("seller.method_codex_oauth"),
			i18n.T("seller.method_claude_oauth"),
			i18n.T("seller.method_gemini_oauth"),
		})
	if err != nil {
		return err
	}
	switch methodIdx {
	case 1:
		return sellerSetupOAuth(in, "codex")
	case 2:
		return sellerSetupOAuth(in, "claude")
	case 3:
		return sellerSetupOAuth(in, "gemini")
	}
	// case 0 falls through to the existing API-key wizard.

	typeAlias, err := cliprompt.Choice(in, i18n.T("seller.prompt_upstream_type"), sellerTypeChoices())
	if err != nil {
		return err
	}
	name, err := cliprompt.Line(in, i18n.T("seller.prompt_channel_name"), "")
	if err != nil {
		return err
	}
	keys, keyRemarks, err := collectSellerKeys(in)
	if err != nil {
		return err
	}
	models, err := cliprompt.Line(in, i18n.T("seller.prompt_models"), "")
	if err != nil {
		return err
	}
	remark, err := cliprompt.Optional(in, i18n.T("seller.prompt_internal_remark"))
	if err != nil {
		return err
	}

	cliout.Println("")
	pool := ""
	if len(keys) > 1 {
		pool = fmt.Sprintf(i18n.T("seller.key_backup_pool"), len(keys))
	}
	cliout.Printf(i18n.T("seller.about_to_mount")+"\n", name, typeAlias, models, pool)
	ok, err := cliprompt.YesNo(in, i18n.T("seller.submit_prompt"), true)
	if err != nil {
		return err
	}
	if !ok {
		cliout.Println(i18n.T("common.cancelled_nothing_submitted"))
		return nil
	}

	// Forward via argv so wizard + non-interactive path share one
	// validator + one POST site. `--key` / `--key-remark` are
	// stringSliceFlag and parseAddKeyArgs aligns them by position.
	// If any remark is non-empty we must push EVERY slot (empty
	// included) so the i-th remark stays paired with the i-th key —
	// sparse remarks would silently slide onto an earlier key.
	forward := []string{
		"--type", typeAlias,
		"--name", name,
		"--models", models,
		"--remark", remark,
	}
	anyRemark := false
	for _, r := range keyRemarks {
		if r != "" {
			anyRemark = true
			break
		}
	}
	for i, k := range keys {
		forward = append(forward, "--key", k)
		if anyRemark {
			forward = append(forward, "--key-remark", keyRemarks[i])
		}
	}
	return sellerAddKey(forward)
}

// collectSellerKeys prompts the user for one or more keys + per-key
// sellerSetupOAuth is the OAuth branch of the setup wizard. Asks
// for name + models (the only two values add-oauth* needs from the
// user beyond what the OAuth provider gives) and forwards via
// argv to sellerAddOAuth so the wizard and the flag-driven call
// share one execution path. Each provider gets a sensible default
// model list so a user who just wants the most common models can
// hit Enter through both prompts.
func sellerSetupOAuth(in *bufio.Reader, provider string) error {
	name, err := cliprompt.Line(in, i18n.T("seller.prompt_channel_name"), "")
	if err != nil {
		return err
	}
	defaultModels := map[string]string{
		"codex":  "gpt-4o,gpt-4o-mini,o1,o1-mini",
		"claude": "claude-3-5-sonnet-latest,claude-3-opus-latest",
		"gemini": "gemini-1.5-pro,gemini-1.5-flash",
	}[provider]
	models, err := cliprompt.Line(in, i18n.T("seller.prompt_models"), defaultModels)
	if err != nil {
		return err
	}

	cliout.Println("")
	cliout.Printf(i18n.T("seller.about_to_mount_oauth")+"\n", provider, name, models)
	ok, err := cliprompt.YesNo(in, i18n.T("seller.submit_prompt"), true)
	if err != nil {
		return err
	}
	if !ok {
		cliout.Println(i18n.T("common.cancelled_nothing_submitted"))
		return nil
	}

	return sellerAddOAuth([]string{provider, "--name", name, "--models", models})
}

// remarks. Pulled out of sellerSetup so the multi-slot/OAuth-blob
// interaction can be unit-tested with a mock stdin (bytes.Buffer
// wrapped in bufio.Reader) — the rest of the wizard (eligibility
// fetch, type/name prompts, submit) is too entangled with I/O for an
// in-process test to be worth the mock surface.
//
// Semantics:
//   - slot 1 is always required; a lone OAuth blob there is the legal
//     single-key OAuth channel and breaks the loop.
//   - slot ≥ 2 OAuth blob is illegal (SetMultiKeySet rejects multi-key
//     sets containing a blob) — warn and re-prompt the SAME slot
//     without appending, so keys[] doesn't get poisoned.
//   - per-key remark is collected for every slot to keep i-th remark
//     paired with i-th key (sparse remarks would silently slide).
func collectSellerKeys(in *bufio.Reader) ([]string, []string, error) {
	var keys []string
	var remarks []string
	for slot := 1; ; slot++ {
		var label string
		if slot == 1 {
			label = i18n.T("seller.prompt_upstream_key")
		} else {
			label = fmt.Sprintf(i18n.T("seller.prompt_backup_key"), slot)
		}
		k, err := cliprompt.Line(in, label, "")
		if err != nil {
			return nil, nil, err
		}
		isBlob := strings.HasPrefix(strings.TrimSpace(k), "{")
		if isBlob && slot > 1 {
			cliout.Println(i18n.T("seller.oauth_blob_in_backup_1"))
			cliout.Println(i18n.T("seller.oauth_blob_in_backup_2"))
			cliout.Println(i18n.T("seller.oauth_blob_in_backup_3"))
			slot-- // retry this same slot number
			continue
		}
		r, err := cliprompt.Optional(in, fmt.Sprintf(i18n.T("seller.prompt_remark_for_key"), slot))
		if err != nil {
			return nil, nil, err
		}
		keys = append(keys, k)
		remarks = append(remarks, r)

		// A lone OAuth blob (slot 1) is a complete single-key channel;
		// stop without offering to add backups (the same isBlob check
		// above would bounce the next slot anyway).
		if isBlob {
			break
		}
		more, err := cliprompt.YesNo(in, i18n.T("seller.prompt_add_another_backup"), false)
		if err != nil {
			return nil, nil, err
		}
		if !more {
			break
		}
	}
	return keys, remarks, nil
}

func renderEligibility(e *api.SellerEligibility) {
	cliout.Println(i18n.T("seller.eligibility_header"))
	cliout.Printf(i18n.T("seller.eligibility_marketplace_enabled")+"\n", checkmark(e.MarketplaceEnabled))
	cliout.Printf(i18n.T("seller.eligibility_account_active")+"\n", checkmark(e.AccountActive))
	cliout.Printf(i18n.T("seller.eligibility_email_verified")+"\n", checkmark(e.EmailVerified))
	cliout.Printf(i18n.T("seller.eligibility_account_age")+"\n", checkmark(e.AccountAgeOK), e.MinAgeDays)
	cliout.Printf(i18n.T("seller.eligibility_has_consume_log")+"\n", checkmark(e.HasConsumeLog))
	cliout.Printf(i18n.T("seller.eligibility_under_cap")+"\n", checkmark(e.UnderCap), e.ChannelCount, e.ChannelCap)
}

func checkmark(ok bool) string {
	if ok {
		return "[x]"
	}
	return "[ ]"
}

// ---- internals -----------------------------------------------------

// sellerClient is the shared "load creds, build client, hand them
// both back" path every seller subcommand starts with. Returning creds
// alongside the client saves the caller a second config.Load() when it
// needs the API base for api.WebOriginFromBase.
func sellerClient() (*api.Client, *config.Credentials, error) {
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return nil, nil, errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return nil, nil, err
	}
	return api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID), creds, nil
}

// classifySellerErr maps backend API errors to friendly CLI messages.
// 401 = stale token (same shape as `everyapi auth status`); everything else
// passes through so the server's explicit messages (eligibility
// reasons, cap-reached, type-not-allowed) surface verbatim.
func classifySellerErr(err error) error {
	if err == nil {
		return nil
	}
	// A stale token surfaces as a 401 OR, the legacy way, an HTTP-200
	// envelope rejection (EnvelopeError) — e.g. GetSelf in the no-quota
	// branch. Both mean "re-login", same as `everyapi auth status` /
	// doctor; without the EnvelopeError arm a withdraw on an expired
	// token would leak the raw backend envelope instead.
	var envErr *api.EnvelopeError
	if api.IsUnauthorized(err) || errors.As(err, &envErr) {
		return errors.New(i18n.T("auth.session_expired"))
	}
	return err
}

// The alias map and its helpers live in internal/sellertype — shared
// with the MCP seller tools so the two surfaces can't drift. These
// package-local names are kept as thin delegates so call sites and
// tests stay untouched; error wording (i18n) stays here because the
// MCP side is English-only.

func resolveSellerType(s string) (string, error) {
	if slug, ok := sellertype.Resolve(s); ok {
		return slug, nil
	}
	s = strings.ToLower(strings.TrimSpace(s))
	return "", fmt.Errorf(i18n.T("seller.unknown_channel_type"), s, strings.Join(sellerTypeChoices(), ", "))
}

func sellerTypeChoices() []string { return sellertype.Choices() }

func channelTypeLabel(slug string) string { return sellertype.Label(slug) }

// Prompt helpers (Line, Optional, Choice, YesNo) live in
// internal/cliprompt — shared with cmd/proxy and the login flow.
