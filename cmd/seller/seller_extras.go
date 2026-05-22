package seller

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
)

// --- seller update -----------------------------------------------

// sellerUpdate is read-modify-write over a single seller channel.
// Like cmd/token's update, we fetch the current row (via the
// pre-existing list endpoint — there's no GET-by-id on /api/seller
// today, so we filter list locally), overlay only the flags the
// caller passed, and PUT the merged shape. The backend rejects
// unknown status values and enforces the auto-disable re-enable
// limit; both surface verbatim.
func sellerUpdate(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi seller update <id> [--name N] [--models M] [--status 1|2] [--remark R] [--test-model M]")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid channel id %q", args[0])
	}
	fs := flag.NewFlagSet("seller update", flag.ContinueOnError)
	name := fs.String("name", "", "channel display name")
	models := fs.String("models", "", "CSV of model ids")
	status := fs.Int("status", 0, "1=enabled, 2=manually disabled")
	remark := fs.String("remark", "", "free-form note")
	testModel := fs.String("test-model", "", "model used by channel health checks")
	modelMapping := fs.String("model-mapping", "", "JSON model remap")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	seen := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if len(seen) == 0 {
		return errors.New("nothing to update: pass at least one flag")
	}
	client, _, err := sellerClient()
	if err != nil {
		return err
	}
	all, err := client.ListSellerChannels(cliout.WithCtx())
	if err != nil {
		return classifySellerErr(err)
	}
	var cur *api.SellerChannel
	for i := range all {
		if all[i].ID == id {
			cur = &all[i]
			break
		}
	}
	if cur == nil {
		return fmt.Errorf("channel #%d not found among your %d mounted channel(s)", id, len(all))
	}
	req := api.SellerChannelUpdate{
		Name:         cur.Name,
		Models:       cur.Models,
		Status:       cur.Status,
		TestModel:    *testModel,
		ModelMapping: *modelMapping,
		Remark:       *remark,
	}
	if seen["name"] {
		req.Name = *name
	}
	if seen["models"] {
		req.Models = *models
	}
	if seen["status"] {
		req.Status = *status
	}
	if err := client.UpdateSellerChannel(cliout.WithCtx(), id, req); err != nil {
		return classifySellerErr(err)
	}
	cliout.Printf("Channel #%d updated.\n", id)
	return nil
}

// --- seller remove -----------------------------------------------

func sellerRemove(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi seller remove <id> [-y]")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid channel id %q", args[0])
	}
	fs := flag.NewFlagSet("seller remove", flag.ContinueOnError)
	yes := fs.Bool("y", false, "skip confirmation")
	yesLong := fs.Bool("yes", false, "alias of -y")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	client, _, err := sellerClient()
	if err != nil {
		return err
	}
	if !*yes && !*yesLong && cliprompt.IsInteractive() {
		all, _ := client.ListSellerChannels(cliout.WithCtx())
		name := "(unknown)"
		for _, ch := range all {
			if ch.ID == id {
				name = ch.Name
				break
			}
		}
		ok, err := cliprompt.YesNo(
			bufio.NewReader(os.Stdin),
			fmt.Sprintf("Delete seller channel #%d %q? Buyers routing here will start failing immediately.", id, name),
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
	if err := client.DeleteSellerChannel(cliout.WithCtx(), id); err != nil {
		return classifySellerErr(err)
	}
	cliout.Printf("Channel #%d removed.\n", id)
	return nil
}

// --- seller refresh ----------------------------------------------

func sellerRefresh(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi seller refresh <id>")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid channel id %q", args[0])
	}
	fs := flag.NewFlagSet("seller refresh", flag.ContinueOnError)
	kindFlag := fs.String("kind", "", "OAuth kind: codex / claude / gemini (auto-detect by default)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	client, _, err := sellerClient()
	if err != nil {
		return err
	}
	kind := strings.ToLower(*kindFlag)
	if kind == "" {
		// Auto-detect from the channel's type. SellerChannel.Type
		// is the backend's integer enum; map back via the existing
		// channelTypeLabel + a small whitelist of the OAuth kinds
		// we know how to refresh.
		all, err := client.ListSellerChannels(cliout.WithCtx())
		if err != nil {
			return classifySellerErr(err)
		}
		var t int
		found := false
		for _, ch := range all {
			if ch.ID == id {
				t = ch.Type
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("channel #%d not found among your channels", id)
		}
		kind = oauthKindForChannelType(t)
		if kind == "" {
			return fmt.Errorf("channel #%d (type=%s) is not an OAuth channel — refresh only applies to codex / claude / gemini", id, channelTypeLabel(t))
		}
	}
	res, err := client.RefreshChannelCredential(cliout.WithCtx(), id, kind)
	if err != nil {
		return classifySellerErr(err)
	}
	cliout.Printf("Channel #%d (%s) credential refreshed.\n", res.ChannelID, res.ChannelType)
	if res.Email != "" {
		cliout.Printf("  account: %s\n", res.Email)
	}
	if res.ExpiresAt > 0 {
		cliout.Printf("  expires: %s\n", time.Unix(res.ExpiresAt, 0).Format("2006-01-02 15:04:05"))
	}
	if res.LastRefresh > 0 {
		cliout.Printf("  last refresh: %s\n", time.Unix(res.LastRefresh, 0).Format("2006-01-02 15:04:05"))
	}
	return nil
}

// oauthKindForChannelType maps the backend channel-type integer to
// the URL suffix used by the refresh endpoints. Returns "" for
// non-OAuth types (which can't be refreshed). The integers must
// match sellerChannelTypeAliases above — `seller refresh` walks the
// same channel-type id space the `setup` / `add-oauth` wizard
// targets, so a drift here would mean a channel mounted via
// add-oauth couldn't be refreshed.
func oauthKindForChannelType(t int) string {
	switch t {
	case sellerChannelTypeAliases["claude"]:
		return "claude"
	case sellerChannelTypeAliases["gemini"]:
		return "gemini"
	case sellerChannelTypeAliases["codex"]:
		return "codex"
	default:
		return ""
	}
}

// --- seller sales ------------------------------------------------

func sellerSales(args []string) error {
	fs := flag.NewFlagSet("seller sales", flag.ContinueOnError)
	page := fs.Int("page", 0, "1-based page index")
	limit := fs.Int("limit", 20, "page size (max 200)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, _, err := sellerClient()
	if err != nil {
		return err
	}
	rows, total, err := client.GetSellerSales(cliout.WithCtx(), *page, *limit)
	if err != nil {
		return classifySellerErr(err)
	}
	if len(rows) == 0 {
		cliout.Println("No sales yet — buyers route here only after a channel is enabled and discoverable.")
		return nil
	}
	cliout.Printf("%d row(s) of %d total:\n", len(rows), total)
	totalCharge, totalTake := 0, 0
	for _, r := range rows {
		when := time.Unix(r.CreatedAt, 0).Format("2006-01-02 15:04:05")
		cliout.Printf("  %s  %-30s  in=%-5d  out=%-5d  charge=%-6d  take=%-6d  buyer=%s\n",
			when, r.ModelName, r.PromptTokens, r.CompletionTokens, r.BuyerCharge, r.SellerTake, shortHash(r.BuyerAnon))
		totalCharge += r.BuyerCharge
		totalTake += r.SellerTake
	}
	cliout.Printf("  page total: charge=%d  take=%d\n", totalCharge, totalTake)
	return nil
}

func shortHash(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// --- seller eligibility ------------------------------------------

func sellerEligibility(args []string) error {
	fs := flag.NewFlagSet("seller eligibility", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, _, err := sellerClient()
	if err != nil {
		return err
	}
	elig, err := client.GetSellerEligibility(cliout.WithCtx())
	if err != nil {
		return classifySellerErr(err)
	}
	if elig.Eligible {
		cliout.Println("✓ Eligible to mount channels.")
	} else {
		cliout.Println("✗ Not yet eligible.")
	}
	cliout.Println("Gates:")
	cliout.Printf("  marketplace_enabled : %v\n", elig.MarketplaceEnabled)
	cliout.Printf("  account_active      : %v\n", elig.AccountActive)
	cliout.Printf("  email_verified      : %v\n", elig.EmailVerified)
	cliout.Printf("  account_age_ok      : %v (min %d days)\n", elig.AccountAgeOK, elig.MinAgeDays)
	cliout.Printf("  has_consume_log     : %v\n", elig.HasConsumeLog)
	cliout.Printf("  under_cap           : %v (%d / %d mounted)\n", elig.UnderCap, elig.ChannelCount, elig.ChannelCap)
	return nil
}

// --- seller compensation ------------------------------------------

func sellerCompensation(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi seller compensation {submit|list} [flags]")
	}
	switch args[0] {
	case "submit":
		return sellerCompensationSubmit(args[1:])
	case "list":
		return sellerCompensationList(args[1:])
	case "help", "--help", "-h":
		cliout.Println("everyapi seller compensation {submit|list} — file / view compensation claims")
		return nil
	default:
		return fmt.Errorf("unknown compensation subcommand %q", args[0])
	}
}

func sellerCompensationSubmit(args []string) error {
	fs := flag.NewFlagSet("seller compensation submit", flag.ContinueOnError)
	upstream := fs.String("upstream", "", "upstream provider (e.g. anthropic / openai / google)")
	proof := fs.String("proof", "", "link to status-page / outage proof")
	desc := fs.String("description", "", "what happened and why you're claiming")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*upstream) == "" {
		return errors.New("--upstream is required")
	}
	if strings.TrimSpace(*desc) == "" {
		return errors.New("--description is required")
	}
	client, _, err := sellerClient()
	if err != nil {
		return err
	}
	row, err := client.SubmitCompensationClaim(cliout.WithCtx(), api.CompensationClaimSubmit{
		UpstreamProvider: *upstream,
		ProofURL:         *proof,
		Description:      *desc,
	})
	if err != nil {
		return classifySellerErr(err)
	}
	cliout.Printf("Claim filed (id=%d, status=%s, suggested cap=%d).\n", row.ID, row.Status, row.SuggestedCap)
	cliout.Println("An admin will review; check 'everyapi seller compensation list' for updates.")
	return nil
}

func sellerCompensationList(args []string) error {
	fs := flag.NewFlagSet("seller compensation list", flag.ContinueOnError)
	status := fs.String("status", "", "filter: pending / approved / rejected")
	page := fs.Int("page", 0, "1-based page")
	limit := fs.Int("limit", 20, "page size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, _, err := sellerClient()
	if err != nil {
		return err
	}
	items, total, err := client.ListCompensationClaims(cliout.WithCtx(), *status, *page, *limit)
	if err != nil {
		return classifySellerErr(err)
	}
	if len(items) == 0 {
		cliout.Println("No claims found.")
		return nil
	}
	cliout.Printf("%d claim(s) of %d total:\n", len(items), total)
	for _, c := range items {
		filed := time.Unix(c.FiledAt, 0).Format("2006-01-02")
		cliout.Printf("  [#%d] %s  upstream=%s  status=%s  cap=%d  approved=%d\n",
			c.ID, filed, c.UpstreamProvider, c.Status, c.SuggestedCap, c.ApprovedAmount)
		if c.Description != "" {
			line := c.Description
			if len(line) > 100 {
				line = line[:100] + "…"
			}
			cliout.Printf("        %s\n", line)
		}
	}
	return nil
}
