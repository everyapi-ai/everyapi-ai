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

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
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
		return errors.New(i18n.T("seller.usage_update"))
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf(i18n.T("seller.invalid_channel_id"), args[0])
	}
	fs := flag.NewFlagSet("seller update", flag.ContinueOnError)
	name := fs.String("name", "", "channel display name")
	models := fs.String("models", "", "CSV of model ids")
	status := fs.Int("status", 0, "1=enabled, 2=manually disabled")
	remark := fs.String("remark", "", "free-form note")
	testModel := fs.String("test-model", "", "model used by channel health checks")
	modelMapping := fs.String("model-mapping", "", "JSON model remap")
	statusCodeMap := fs.String("status-code-mapping", "", "JSON status-code remap")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	seen := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if len(seen) == 0 {
		return errors.New(i18n.T("seller.update_no_flags"))
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
		return fmt.Errorf(i18n.T("seller.channel_not_found"), id)
	}
	// Seed EVERY editable field from the current row, then overlay only the
	// flags the caller actually passed (fs.Visit-tracked in `seen`).
	// Previously test-model / model-mapping / remark were written from their
	// flag values unconditionally — and status-code-mapping had no flag at
	// all — so any edit that didn't re-supply them blanked the channel's
	// existing values.
	req := api.SellerChannelUpdate{
		Name:          cur.Name,
		Models:        cur.Models,
		Status:        cur.Status,
		TestModel:     cur.TestModel,
		ModelMapping:  cur.ModelMapping,
		StatusCodeMap: cur.StatusCodeMap,
		Remark:        cur.Remark,
	}
	if seen["name"] {
		req.Name = *name
	}
	if seen["models"] {
		req.Models = *models
	}
	if seen["status"] {
		req.Status = *status
	} else if req.Status != 1 && req.Status != 2 {
		// cur.Status is auto-disabled (3): the seller-update endpoint only
		// accepts 1/2, so forwarding 3 (which a field-only edit does, since
		// req.Status is seeded from cur) is rejected. Default to 2 (manually
		// disabled) so editing a remark/model on an auto-disabled channel
		// works — it stays disabled but in a seller-settable state.
		req.Status = 2
	}
	if seen["remark"] {
		req.Remark = *remark
	}
	if seen["test-model"] {
		req.TestModel = *testModel
	}
	if seen["model-mapping"] {
		req.ModelMapping = *modelMapping
	}
	if seen["status-code-mapping"] {
		req.StatusCodeMap = *statusCodeMap
	}
	if err := client.UpdateSellerChannel(cliout.WithCtx(), id, req); err != nil {
		return classifySellerErr(err)
	}
	cliout.Printf(i18n.T("seller.channel_updated")+"\n", id)
	return nil
}

// --- seller remove -----------------------------------------------

func sellerRemove(args []string) error {
	if len(args) == 0 {
		return errors.New(i18n.T("seller.usage_remove"))
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf(i18n.T("seller.invalid_channel_id"), args[0])
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
			fmt.Sprintf(i18n.T("seller.remove_confirm"), id, name),
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
	if err := client.DeleteSellerChannel(cliout.WithCtx(), id); err != nil {
		return classifySellerErr(err)
	}
	cliout.Printf(i18n.T("seller.channel_removed")+"\n", id)
	return nil
}

// --- seller refresh ----------------------------------------------

func sellerRefresh(args []string) error {
	if len(args) == 0 {
		return errors.New(i18n.T("seller.usage_refresh"))
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf(i18n.T("seller.invalid_channel_id"), args[0])
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
			return fmt.Errorf(i18n.T("seller.channel_not_found"), id)
		}
		kind = oauthKindForChannelType(t)
		if kind == "" {
			return fmt.Errorf(i18n.T("seller.oauth_only_refresh"), id, channelTypeLabel(t))
		}
	}
	res, err := client.RefreshChannelCredential(cliout.WithCtx(), id, kind)
	if err != nil {
		return classifySellerErr(err)
	}
	cliout.Printf(i18n.T("seller.refresh_done")+"\n", res.ChannelID, res.ChannelType)
	if res.Email != "" {
		cliout.Printf(i18n.T("seller.refresh_account_label")+"\n", res.Email)
	}
	if res.ExpiresAt != "" {
		cliout.Printf(i18n.T("seller.refresh_expires_label")+"\n", res.ExpiresAt)
	}
	if res.LastRefresh != "" {
		cliout.Printf(i18n.T("seller.refresh_last_refresh_label")+"\n", res.LastRefresh)
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
		cliout.Println(i18n.T("seller.no_sales"))
		return nil
	}
	cliout.Printf(i18n.T("common.rows_of_total")+"\n", len(rows), total)
	totalCharge, totalTake := 0, 0
	for _, r := range rows {
		when := time.Unix(r.CreatedAt, 0).Format("2006-01-02 15:04:05")
		cliout.Printf("  %s  %-30s  in=%-5d  out=%-5d  charge=%-6d  take=%-6d  buyer=%s\n",
			when, r.ModelName, r.PromptTokens, r.CompletionTokens, r.BuyerCharge, r.SellerTake, shortHash(r.BuyerAnon))
		totalCharge += r.BuyerCharge
		totalTake += r.SellerTake
	}
	cliout.Printf(i18n.T("seller.sales_page_total")+"\n", totalCharge, totalTake)
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
		cliout.Println(i18n.T("seller.eligible_yes"))
	} else {
		cliout.Println(i18n.T("seller.eligible_no"))
	}
	cliout.Println(i18n.T("seller.gates_header"))
	cliout.Printf(i18n.T("seller.gate_marketplace_enabled")+"\n", elig.MarketplaceEnabled)
	cliout.Printf(i18n.T("seller.gate_account_active")+"\n", elig.AccountActive)
	cliout.Printf(i18n.T("seller.gate_email_verified")+"\n", elig.EmailVerified)
	cliout.Printf(i18n.T("seller.gate_account_age_ok")+"\n", elig.AccountAgeOK, elig.MinAgeDays)
	cliout.Printf(i18n.T("seller.gate_has_consume_log")+"\n", elig.HasConsumeLog)
	cliout.Printf(i18n.T("seller.gate_under_cap")+"\n", elig.UnderCap, elig.ChannelCount, elig.ChannelCap)
	return nil
}

// --- seller compensation ------------------------------------------

func sellerCompensation(args []string) error {
	if len(args) == 0 {
		return errors.New(i18n.T("seller.usage_compensation"))
	}
	switch args[0] {
	case "submit":
		return sellerCompensationSubmit(args[1:])
	case "list":
		return sellerCompensationList(args[1:])
	case "help", "--help", "-h":
		cliout.Println(i18n.T("seller.usage_compensation_help"))
		return nil
	default:
		return fmt.Errorf(i18n.T("seller.unknown_compensation_sub"), args[0])
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
		return errors.New(i18n.T("seller.compensation_upstream_required"))
	}
	if strings.TrimSpace(*desc) == "" {
		return errors.New(i18n.T("seller.compensation_description_required"))
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
	cliout.Printf(i18n.T("seller.compensation_filed")+"\n", row.ID, row.Status, row.SuggestedCap)
	cliout.Println(i18n.T("seller.compensation_admin_review"))
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
		cliout.Println(i18n.T("seller.compensation_no_claims"))
		return nil
	}
	cliout.Printf(i18n.T("seller.claims_total")+"\n", len(items), total)
	for _, c := range items {
		filed := time.Unix(c.FiledAt, 0).Format("2006-01-02")
		cliout.Printf("  [#%d] %s  upstream=%s  status=%s  cap=%d  approved=%d\n",
			c.ID, filed, c.UpstreamProvider, c.Status, c.SuggestedCap, c.ApprovedAmount)
		if c.Description != "" {
			line := c.Description
			if r := []rune(line); len(r) > 100 {
				line = string(r[:100]) + "…"
			}
			cliout.Printf("        %s\n", line)
		}
	}
	return nil
}
