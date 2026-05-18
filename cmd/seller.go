package cmd

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/relaya-ai/relaya-ai/internal/api"
	"github.com/relaya-ai/relaya-ai/internal/config"
)

// Seller is the top-level dispatcher for `relaya seller …`. Each
// subcommand is a thin handler: load creds → call one or two API client
// methods → render. OAuth flows (`add-oauth claude/chatgpt`) are
// intentionally out of scope here — they need PKCE + a local listener
// + browser launch, which is its own follow-up release.
//
// Subcommands:
//
//	relaya seller list                          List the user's mounted channels
//	relaya seller withdraw [--quota <int>]      Transfer pending earnings to main balance
//	relaya seller add-key   --type <T> --name <N> --key <K> --models <M>  [--remark <R>]
//	                                            POST /api/seller/channel with a plain API key
//	relaya seller setup                         Interactive wizard wrapping add-key
//	relaya seller help                          Print this help
func Seller(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		println(sellerUsage)
		if len(args) == 0 {
			return errors.New("missing subcommand (try 'relaya seller help')")
		}
		return nil
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		return SellerList(rest)
	case "withdraw":
		return SellerWithdraw(rest)
	case "add-key":
		return SellerAddKey(rest)
	case "setup":
		return SellerSetup(rest)
	default:
		printf("%s\n", sellerUsage)
		return fmt.Errorf("unknown 'seller' subcommand %q", sub)
	}
}

const sellerUsage = `relaya seller — channel-marketplace seller commands

USAGE
  relaya seller <subcommand> [flags]

SUBCOMMANDS
  list                              List the channels you've mounted
  withdraw [--quota <int>]          Transfer pending seller earnings to main balance
                                    (no flag → transfer everything)
  add-key  --type <T> --name <N> --key <K> --models <M> [--remark <R>]
                                    Mount a channel with a plain API key
  setup                             Interactive wizard wrapping add-key
  help                              Show this message`

// ---- seller list ---------------------------------------------------

// SellerList prints every channel the user owns. Mirrors the MCP
// relaya_seller_list output shape so a buyer who's been using the AI
// surface sees consistent data on the terminal.
func SellerList(args []string) error {
	fs := flag.NewFlagSet("seller list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, _, err := sellerClient()
	if err != nil {
		return err
	}
	channels, err := client.ListSellerChannels(withCtx())
	if err != nil {
		return classifySellerErr(err)
	}
	if len(channels) == 0 {
		println("No seller channels mounted yet.")
		println("Use 'relaya seller setup' (wizard) or 'relaya seller add-key' (flags).")
		return nil
	}
	printf("%d seller channel(s):\n", len(channels))
	for _, ch := range channels {
		printf("  [#%d] %s — type=%s status=%s\n",
			ch.ID, ch.Name, channelTypeLabel(ch.Type), channelStatusLabel(ch.Status))
		if ch.Models != "" {
			printf("        models: %s\n", ch.Models)
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

// SellerWithdraw moves SellerQuota → Quota. Empty --quota = "everything
// pending"; explicit --quota lets a power user partial-transfer in DB
// units (the same shape as the MCP tool's `quota` arg).
func SellerWithdraw(args []string) error {
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
		self, err := client.GetSelf(withCtx())
		if err != nil {
			return classifySellerErr(err)
		}
		amount = self.SellerQuota
		if amount <= 0 {
			println("Nothing to withdraw — your seller balance is $0.")
			return nil
		}
	}

	if err := client.TransferSellerQuota(withCtx(), amount); err != nil {
		return classifySellerErr(err)
	}

	// Render the transferred amount in USD so the user sees the same
	// units the dashboard / `relaya status` use. perUnit is a free
	// /api/status round-trip; tolerate a failure (rare) by falling back
	// to raw DB units rather than aborting after a successful transfer.
	perUnit := 1.0
	if status, sErr := client.GetStatus(withCtx()); sErr == nil && status.QuotaPerUnit > 0 {
		perUnit = status.QuotaPerUnit
	}
	printf("Transferred $%.2f from seller balance to main balance.\n", float64(amount)/perUnit)
	printf("Check it: %s/wallet\n", trimAPIBaseToWebOrigin(creds.APIBase))
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

func (s *stringSliceFlag) String() string         { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error     { *s = append(*s, v); return nil }

// SellerAddKey wraps POST /api/seller/channel. The type is a human
// alias (openai / claude / gemini / …) resolved to the backend integer
// id via sellerChannelTypeAliases; if the user types a number we pass
// it through, which is the escape hatch for any future type the alias
// map doesn't list yet.
func SellerAddKey(args []string) error {
	parsed, err := parseAddKeyArgs(args)
	if err != nil {
		return err
	}
	typeID, err := resolveSellerType(parsed.Type)
	if err != nil {
		return err
	}
	client, _, err := sellerClient()
	if err != nil {
		return err
	}

	id, err := client.CreateSellerChannel(withCtx(), api.SellerChannelCreate{
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
	printf("Mounted channel #%d (%s, type=%s)%s.\n", id, parsed.Name, channelTypeLabel(typeID), pool)
	println("Status: enabled. Run 'relaya seller list' to inspect, or visit the dashboard.")
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
	fs.Var(&keys, "key", "upstream API key (or Vertex ADC JSON / AWS region+key blob — same shape as the dashboard form). Repeat the flag for a multi-key backup pool, e.g. --key sk-a --key sk-b")
	fs.Var(&remarks, "key-remark", "optional per-key label, repeatable; aligned by position with --key (1st --key-remark goes with the 1st --key)")
	fs.StringVar(&a.Models, "models", "", "comma-separated models this channel serves (empty rejected — pick at least one)")
	fs.StringVar(&a.Remark, "remark", "", "optional internal note (channel-level — distinct from --key-remark)")
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
	return a, nil
}

// ---- seller setup --------------------------------------------------

// SellerSetup is the small-talk wizard for `relaya seller add-key`:
// query eligibility upfront (so the user finds out about a failed gate
// BEFORE typing a key), then prompt for type / name / key / models, then
// POST. Confirms before submit so a typo is recoverable. Reuses
// parseAddKeyArgs to keep validation in one place — once the wizard
// has the values it assembles an argv and routes through the same
// non-interactive path.
func SellerSetup(args []string) error {
	fs := flag.NewFlagSet("seller setup", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, creds, err := sellerClient()
	if err != nil {
		return err
	}

	elig, err := client.GetSellerEligibility(withCtx())
	if err != nil {
		return classifySellerErr(err)
	}
	renderEligibility(elig)
	if !elig.Eligible {
		println("")
		println("You don't meet the mount requirements yet. Fix the items above first, then re-run.")
		printf("Dashboard: %s/seller/channels\n", trimAPIBaseToWebOrigin(creds.APIBase))
		return nil
	}

	in := bufio.NewReader(os.Stdin)
	println("")
	println("Mounting a new channel. Press Ctrl+C to cancel.")
	println("")

	typeAlias, err := promptChoice(in, "Upstream type", sellerTypeChoices())
	if err != nil {
		return err
	}
	name, err := promptLine(in, "Channel name", "")
	if err != nil {
		return err
	}
	// Collect one or more keys. The first is required; additional keys
	// form a multi-key backup pool (B2). OAuth blobs (`{` prefix) are
	// rejected in multi-key mode by the backend, so once the user
	// supplies one the loop stops asking for more.
	var keys []string
	var keyRemarks []string
	for slot := 1; ; slot++ {
		var label string
		if slot == 1 {
			label = "Upstream API key"
		} else {
			label = fmt.Sprintf("Backup key #%d", slot)
		}
		k, err := promptLine(in, label, "")
		if err != nil {
			return err
		}
		// Detect OAuth blobs BEFORE appending. Two distinct cases:
		//
		//   - slot 1 + blob: legal — a lone OAuth credential is the
		//     valid single-key OAuth channel shape. Accept and break.
		//   - slot ≥ 2 + blob: illegal — SetMultiKeySet rejects any
		//     multi-key set that contains an OAuth blob. If we appended
		//     it, the submit would 400 with a backend message the user
		//     can't tie back to their input. Warn and re-prompt this
		//     slot instead of poisoning keys[].
		isBlob := strings.HasPrefix(strings.TrimSpace(k), "{")
		if isBlob && slot > 1 {
			println("That looks like an OAuth/JSON credential blob.")
			println("OAuth credentials cannot be combined with other keys in a backup pool — they must be the only key on the channel.")
			println("Re-enter a plain API key, or start over with the OAuth blob as the only key.")
			slot-- // retry this same slot number
			continue
		}
		// Always ask for the per-key remark, even on slot 1. Asking
		// keeps the i-th remark paired with the i-th key — skipping
		// slot 1 would create an alignment hazard if the seller later
		// answers yes to "Add another backup key?" The lone-key common
		// case is unaffected: empty input is accepted, and the
		// channel-level "Internal remark" below still covers it.
		r, err := promptOptional(in, fmt.Sprintf("Remark for key #%d (optional)", slot))
		if err != nil {
			return err
		}
		keys = append(keys, k)
		keyRemarks = append(keyRemarks, r)

		// A lone OAuth blob (slot 1) is a complete single-key channel —
		// stop here without offering to add a backup, otherwise the
		// next prompt would just bounce off the same isBlob check above.
		if isBlob {
			break
		}
		more, err := promptYesNo(in, "Add another backup key?", false)
		if err != nil {
			return err
		}
		if !more {
			break
		}
	}
	models, err := promptLine(in, "Models (comma-separated)", "")
	if err != nil {
		return err
	}
	remark, err := promptOptional(in, "Internal remark (optional)")
	if err != nil {
		return err
	}

	println("")
	pool := ""
	if len(keys) > 1 {
		pool = fmt.Sprintf(" with %d-key backup pool", len(keys))
	}
	printf("About to mount: %s / type=%s / models=%s%s\n", name, typeAlias, models, pool)
	ok, err := promptYesNo(in, "Submit?", true)
	if err != nil {
		return err
	}
	if !ok {
		println("Cancelled — nothing was submitted.")
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
	return SellerAddKey(forward)
}

func renderEligibility(e *api.SellerEligibility) {
	println("Marketplace eligibility:")
	printf("  %s marketplace enabled\n", checkmark(e.MarketplaceEnabled))
	printf("  %s account active\n", checkmark(e.AccountActive))
	printf("  %s email verified\n", checkmark(e.EmailVerified))
	printf("  %s account ≥ %d day(s) old\n", checkmark(e.AccountAgeOK), e.MinAgeDays)
	printf("  %s has at least one successful consumption\n", checkmark(e.HasConsumeLog))
	printf("  %s under channel cap (%d / %d)\n", checkmark(e.UnderCap), e.ChannelCount, e.ChannelCap)
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
// needs the API base for trimAPIBaseToWebOrigin.
func sellerClient() (*api.Client, *config.Credentials, error) {
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return nil, nil, errors.New("not logged in — run 'relaya login' first")
	}
	if err != nil {
		return nil, nil, err
	}
	return api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID), creds, nil
}

// classifySellerErr maps backend API errors to friendly CLI messages.
// 401 = stale token (same shape as `relaya status`); everything else
// passes through so the server's explicit messages (eligibility
// reasons, cap-reached, type-not-allowed) surface verbatim.
func classifySellerErr(err error) error {
	if err == nil {
		return nil
	}
	if api.IsUnauthorized(err) {
		return errors.New("your session expired — run 'relaya login' again")
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

// ---- prompt helpers ------------------------------------------------
//
// Used only by SellerSetup. Kept tiny and explicit; pulling in a TUI
// library for four prompts isn't worth the dep, and these are mostly
// just `bufio.ReadLine + TrimSpace` with friendly redo-on-empty.

// promptOptional asks for a value where empty is a legal answer (the
// caller treats "" as "user skipped"). Distinct from promptLine, which
// loops on empty input — kept separate so the call site states intent
// instead of overloading the def parameter.
func promptOptional(in *bufio.Reader, label string) (string, error) {
	printf("%s: ", label)
	line, err := in.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// promptLine asks for a single value. Empty input is rejected unless a
// non-empty default is provided.
func promptLine(in *bufio.Reader, label, def string) (string, error) {
	suffix := ""
	if def != "" {
		suffix = fmt.Sprintf(" [%s]", def)
	}
	for {
		printf("%s%s: ", label, suffix)
		line, err := in.ReadString('\n')
		if err != nil && (err != io.EOF || line == "") {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if def != "" {
				return def, nil
			}
			println("(value required)")
			continue
		}
		return line, nil
	}
}

// promptChoice asks for one of a fixed list of options (1-indexed or by
// name). Loops until the user picks something valid.
func promptChoice(in *bufio.Reader, label string, options []string) (string, error) {
	printf("%s — options:\n", label)
	for i, o := range options {
		printf("  %d) %s\n", i+1, o)
	}
	for {
		printf("Enter name or number: ")
		line, err := in.ReadString('\n')
		if err != nil && (err != io.EOF || line == "") {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for i, o := range options {
			if strings.EqualFold(line, o) || line == strconv.Itoa(i+1) {
				return o, nil
			}
		}
		printf("(unknown choice %q — try again)\n", line)
	}
}

// promptYesNo gates destructive operations. Default applies on empty
// input.
func promptYesNo(in *bufio.Reader, label string, defaultYes bool) (bool, error) {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	for {
		printf("%s %s: ", label, suffix)
		line, err := in.ReadString('\n')
		if err != nil && (err != io.EOF || line == "") {
			return false, err
		}
		line = strings.TrimSpace(strings.ToLower(line))
		switch line {
		case "":
			return defaultYes, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		println("(please answer y or n)")
	}
}
