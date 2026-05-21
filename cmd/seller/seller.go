package seller

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
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
			return errors.New("missing subcommand (try 'everyapi seller help')")
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
	default:
		cliout.Printf("%s\n", sellerUsage)
		return fmt.Errorf("unknown 'seller' subcommand %q", sub)
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
		cliout.Println("No seller channels mounted yet.")
		cliout.Println("Use 'everyapi seller setup' (wizard) or 'everyapi seller add-key' (flags).")
		return nil
	}
	cliout.Printf("%d seller channel(s):\n", len(channels))
	for _, ch := range channels {
		cliout.Printf("  [#%d] %s — type=%s status=%s\n",
			ch.ID, ch.Name, channelTypeLabel(ch.Type), channelStatusLabel(ch.Status))
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
	client, creds, err := sellerClient()
	if err != nil {
		return err
	}

	amount := *quota
	if amount == 0 {
		self, err := client.GetSelf(cliout.WithCtx())
		if err != nil {
			return classifySellerErr(err)
		}
		amount = self.SellerQuota
		if amount <= 0 {
			cliout.Println("Nothing to withdraw — your seller balance is $0.")
			return nil
		}
	}

	if err := client.TransferSellerQuota(cliout.WithCtx(), amount); err != nil {
		return classifySellerErr(err)
	}

	// Render the transferred amount in USD so the user sees the same
	// units the dashboard / `everyapi status` use. perUnit is a free
	// /api/status round-trip; tolerate a failure (rare) by falling back
	// to raw DB units rather than aborting after a successful transfer.
	perUnit := 1.0
	if status, sErr := client.GetStatus(cliout.WithCtx()); sErr == nil && status.QuotaPerUnit > 0 {
		perUnit = status.QuotaPerUnit
	}
	cliout.Printf("Transferred $%.2f from seller balance to main balance.\n", float64(amount)/perUnit)
	cliout.Printf("Check it: %s/wallet\n", api.WebOriginFromBase(creds.APIBase))
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
// alias (openai / claude / gemini / …) resolved to the backend integer
// id via sellerChannelTypeAliases; if the user types a number we pass
// it through, which is the escape hatch for any future type the alias
// map doesn't list yet.
func sellerAddKey(args []string) error {
	parsed, err := parseAddKeyArgs(args)
	if err != nil {
		return err
	}
	typeID, err := resolveSellerType(parsed.Type)
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
		cliout.Println("Marketplace eligibility check failed. Fix the unchecked items above, then re-run.")
		cliout.Printf("Dashboard: %s/seller/channels\n", api.WebOriginFromBase(creds.APIBase))
		return errors.New("not eligible to mount a seller channel")
	}

	id, err := client.CreateSellerChannel(cliout.WithCtx(), api.SellerChannelCreate{
		Name:       parsed.Name,
		Type:       typeID,
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
		pool = fmt.Sprintf(" with %d-key backup pool", len(parsed.Keys))
	}
	cliout.Printf("Mounted channel #%d (%s, type=%s)%s.\n", id, parsed.Name, channelTypeLabel(typeID), pool)
	cliout.Println("Status: enabled. Run 'everyapi seller list' to inspect, or visit the dashboard.")
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
	fs.StringVar(&a.Type, "type", "", "upstream channel type alias (openai / claude / gemini / codex / vertex / aws / xai / deepseek) or numeric id")
	fs.StringVar(&a.Name, "name", "", "channel display name (free-form)")
	fs.Var(&keys, "key", "upstream API key (per-key) — or a Vertex ADC JSON / AWS region+key blob. Repeat the flag for a multi-key backup pool, e.g. --key sk-a --key sk-b")
	fs.Var(&remarks, "key-remark", "per-key label, repeatable, aligned by position with --key (1st --key-remark labels the 1st --key). For the channel-wide note use --remark instead.")
	fs.StringVar(&a.Models, "models", "", "comma-separated models this channel serves (empty rejected — pick at least one)")
	fs.StringVar(&a.Remark, "remark", "", "channel-level note (one per channel, free-form). For per-key labels use --key-remark instead.")
	if err := fs.Parse(args); err != nil {
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
		return nil, fmt.Errorf("missing required flag(s): %s", strings.Join(missing, ", "))
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
		cliout.Println("You don't meet the mount requirements yet. Fix the items above first, then re-run.")
		cliout.Printf("Dashboard: %s/seller/channels\n", api.WebOriginFromBase(creds.APIBase))
		return nil
	}

	in := bufio.NewReader(os.Stdin)
	cliout.Println("")
	cliout.Println("Mounting a new channel. Press Esc / Ctrl+C to cancel.")
	cliout.Println("")

	// Auth-method picker. Index → branch so the labels stay free
	// to grow without breaking string-match branching.
	methodIdx, err := cliprompt.Pick("Authentication method",
		[]string{
			"API key             — paste a vendor key (openai / anthropic / etc.)",
			"Codex / ChatGPT OAuth — bind a ChatGPT Plus subscription (device flow, no token paste)",
			"Claude OAuth        — bind an Anthropic Claude subscription (one paste from the browser)",
			"Gemini OAuth        — bind a Google Gemini account (full one-click)",
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

	typeAlias, err := cliprompt.Choice(in, "Upstream type", sellerTypeChoices())
	if err != nil {
		return err
	}
	name, err := cliprompt.Line(in, "Channel name", "")
	if err != nil {
		return err
	}
	keys, keyRemarks, err := collectSellerKeys(in)
	if err != nil {
		return err
	}
	models, err := cliprompt.Line(in, "Models (comma-separated)", "")
	if err != nil {
		return err
	}
	remark, err := cliprompt.Optional(in, "Internal remark (optional)")
	if err != nil {
		return err
	}

	cliout.Println("")
	pool := ""
	if len(keys) > 1 {
		pool = fmt.Sprintf(" with %d-key backup pool", len(keys))
	}
	cliout.Printf("About to mount: %s / type=%s / models=%s%s\n", name, typeAlias, models, pool)
	ok, err := cliprompt.YesNo(in, "Submit?", true)
	if err != nil {
		return err
	}
	if !ok {
		cliout.Println("Cancelled — nothing was submitted.")
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
	name, err := cliprompt.Line(in, "Channel name", "")
	if err != nil {
		return err
	}
	defaultModels := map[string]string{
		"codex":  "gpt-4o,gpt-4o-mini,o1,o1-mini",
		"claude": "claude-3-5-sonnet-latest,claude-3-opus-latest",
		"gemini": "gemini-1.5-pro,gemini-1.5-flash",
	}[provider]
	models, err := cliprompt.Line(in, "Models (comma-separated)", defaultModels)
	if err != nil {
		return err
	}

	cliout.Println("")
	cliout.Printf("About to mount via %s OAuth: %s / models=%s\n", provider, name, models)
	ok, err := cliprompt.YesNo(in, "Submit?", true)
	if err != nil {
		return err
	}
	if !ok {
		cliout.Println("Cancelled — nothing was submitted.")
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
			label = "Upstream API key"
		} else {
			label = fmt.Sprintf("Backup key #%d", slot)
		}
		k, err := cliprompt.Line(in, label, "")
		if err != nil {
			return nil, nil, err
		}
		isBlob := strings.HasPrefix(strings.TrimSpace(k), "{")
		if isBlob && slot > 1 {
			cliout.Println("That looks like an OAuth/JSON credential blob.")
			cliout.Println("OAuth credentials cannot be combined with other keys in a backup pool — they must be the only key on the channel.")
			cliout.Println("Re-enter a plain API key, or press Ctrl-C to abort and re-run with the OAuth blob as the only key.")
			slot-- // retry this same slot number
			continue
		}
		r, err := cliprompt.Optional(in, fmt.Sprintf("Remark for key #%d (optional)", slot))
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
		more, err := cliprompt.YesNo(in, "Add another backup key?", false)
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
	cliout.Println("Marketplace eligibility:")
	cliout.Printf("  %s marketplace enabled\n", checkmark(e.MarketplaceEnabled))
	cliout.Printf("  %s account active\n", checkmark(e.AccountActive))
	cliout.Printf("  %s email verified\n", checkmark(e.EmailVerified))
	cliout.Printf("  %s account ≥ %d day(s) old\n", checkmark(e.AccountAgeOK), e.MinAgeDays)
	cliout.Printf("  %s has at least one successful consumption\n", checkmark(e.HasConsumeLog))
	cliout.Printf("  %s under channel cap (%d / %d)\n", checkmark(e.UnderCap), e.ChannelCount, e.ChannelCap)
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
		return nil, nil, errors.New("not logged in — run 'everyapi login' first")
	}
	if err != nil {
		return nil, nil, err
	}
	return api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID), creds, nil
}

// classifySellerErr maps backend API errors to friendly CLI messages.
// 401 = stale token (same shape as `everyapi status`); everything else
// passes through so the server's explicit messages (eligibility
// reasons, cap-reached, type-not-allowed) surface verbatim.
func classifySellerErr(err error) error {
	if err == nil {
		return nil
	}
	if api.IsUnauthorized(err) {
		return errors.New("your session expired — run 'everyapi login' again")
	}
	return err
}

// sellerChannelTypeAliases maps human names to the server's integer
// channel type id. Curated to match the server-side allow-list for
// seller-mounted channels — anything not in that allowed set would
// 422 at submit, so listing it here would be misleading. Keep the
// keys lowercase; lookup folds case.
var sellerChannelTypeAliases = map[string]int{
	"openai":    1,
	"anthropic": 6,
	"claude":    6,
	"gemini":    13,
	"aws":       18,
	"bedrock":   18,
	"vertex":    26,
	"vertexai":  26,
	"deepseek":  28,
	"xai":       33,
	"grok":      33,
	"codex":     42,
}

// resolveSellerType accepts either the alias name (openai / claude / …)
// or a numeric id straight through. Numeric passthrough is the escape
// hatch for any allowed type the alias map doesn't list yet, without
// blocking the user on a CLI release.
func resolveSellerType(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if id, ok := sellerChannelTypeAliases[s]; ok {
		return id, nil
	}
	if id, err := strconv.Atoi(s); err == nil && id > 0 {
		return id, nil
	}
	return 0, fmt.Errorf("unknown channel type %q — try one of: %s, or a numeric id", s, strings.Join(sellerTypeChoices(), ", "))
}

// sellerTypeChoices returns alias names in stable display order. We
// dedupe across the alias map (claude/anthropic both → 6) and prefer
// the marketing-recognisable spelling: "claude" not "anthropic",
// "vertex" not "vertexai".
func sellerTypeChoices() []string {
	preferred := []string{"openai", "claude", "gemini", "codex", "vertex", "aws", "xai", "deepseek"}
	// Sanity check that every preferred name resolves — guards against
	// silent drift if the alias map is renamed without updating this list.
	out := make([]string, 0, len(preferred))
	for _, n := range preferred {
		if _, ok := sellerChannelTypeAliases[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

// channelTypeLabel returns the human alias for a backend integer id,
// for output rendering. Falls back to the raw id string when the
// integer is outside our alias map (forward-compat with future types).
func channelTypeLabel(id int) string {
	type kv struct {
		name string
		id   int
	}
	pairs := make([]kv, 0, len(sellerChannelTypeAliases))
	for n, i := range sellerChannelTypeAliases {
		pairs = append(pairs, kv{n, i})
	}
	// Stable iteration so the label for id=6 is deterministically
	// "claude" (the marketing name we prefer over "anthropic"), even
	// though both alias to 6. Sort puts the alphabetically-first first;
	// for the four ambiguous pairs ((anthropic, claude), (aws,
	// bedrock), (vertex, vertexai), (xai, grok)) we want claude / aws /
	// vertex / grok respectively. Hardcode the preferred display name
	// instead of relying on alphabetic luck.
	preferredFor := map[int]string{
		6:  "claude",
		18: "aws",
		26: "vertex",
		33: "xai",
	}
	if name, ok := preferredFor[id]; ok {
		return name
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].name < pairs[j].name })
	for _, p := range pairs {
		if p.id == id {
			return p.name
		}
	}
	return fmt.Sprintf("type=%d", id)
}

// Prompt helpers (Line, Optional, Choice, YesNo) live in
// internal/cliprompt — shared with cmd/proxy and the login flow.
