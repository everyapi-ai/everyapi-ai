// Package wallet wires `everyapi wallet …` — payment history, payment methods, and redemption-key application. The existing `everyapi wallet topup` command (which opens the dashboard top-up page behind the §7-5 anti-phishing handshake) stays as-is for actual money-in flows; wallet covers everything that's safe to do over the API without a browser.
package wallet

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

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliargs"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/style"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// styledRedeemed renders the wallet.redeemed line with the credited quota bolded. The amount is **-marked in wallet.redeemed across every locale; routing the formatted string through style.Emph bolds it on a styled terminal and strips the markers when piped / NO_COLOR.
func styledRedeemed(quota int64) string {
	return style.Emph(fmt.Sprintf(i18n.T("wallet.redeemed"), quota))
}

func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(i18n.T("wallet.usage"))
		if len(args) == 0 {
			return fmt.Errorf(i18n.T("common.missing_subcommand"), "everyapi wallet")
		}
		return nil
	}
	switch args[0] {
	case "history":
		return runHistory(args[1:])
	case "info":
		return runInfo(args[1:])
	case "redeem":
		return runRedeem(args[1:])
	default:
		cliout.Println(i18n.T("wallet.usage"))
		return fmt.Errorf(i18n.T("common.unknown_subcommand"), "wallet", args[0])
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

func runHistory(args []string) error {
	fs := flag.NewFlagSet("wallet history", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "page size")
	page := fs.Int("page", 0, "1-based page index")
	keyword := fs.String("keyword", "", "substring filter (trade no / notes)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, total, err := client.ListUserTopups(cliout.WithCtx(), *page, *limit, *keyword)
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println(i18n.T("wallet.no_topups"))
		return nil
	}
	cliout.Printf(i18n.T("log.label.rows_window")+"\n", len(rows), total)
	for _, t := range rows {
		when := time.Unix(t.CreateTime, 0).Format("2006-01-02 15:04:05")
		method := t.PaymentMethod
		if t.PaymentProvider != "" && t.PaymentProvider != t.PaymentMethod {
			method = fmt.Sprintf("%s/%s", t.PaymentMethod, t.PaymentProvider)
		}
		cliout.Printf("  %s  [#%d]  %s  amount=%d  money=%s  status=%s  trade=%s\n",
			when, t.ID, cliout.Sanitize(method), t.Amount, style.Bold(fmt.Sprintf("%.2f", t.Money)), cliout.Sanitize(t.Status), cliout.Sanitize(t.TradeNo))
	}
	return nil
}

func runInfo(args []string) error {
	fs := flag.NewFlagSet("wallet info", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	info, err := client.GetTopupInfo(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	cliout.Println(i18n.T("wallet.label.payment_methods"))
	if len(info.PayMethods) == 0 {
		cliout.Printf("  %s\n", i18n.T("wallet.label.none_enabled"))
	}
	for _, pm := range info.PayMethods {
		cliout.Printf("  - %s (type=%s, min=%s)\n", cliout.Sanitize(pm["name"]), cliout.Sanitize(pm["type"]), cliout.Sanitize(pm["min_topup"]))
	}
	cliout.Println("\n" + i18n.T("wallet.label.feature_flags"))
	cliout.Printf("  fluxa:        %v (min=%d)\n", info.EnableFluxaTopup, info.FluxaMinTopup)
	if info.MinTopup > 0 {
		cliout.Printf("\n"+i18n.T("wallet.label.min_topup")+"\n", info.MinTopup)
	}
	if len(info.AmountOptions) > 0 {
		labels := make([]string, len(info.AmountOptions))
		for i, a := range info.AmountOptions {
			labels[i] = fmt.Sprintf("%.2f", a)
		}
		cliout.Printf("\n"+i18n.T("wallet.label.suggested")+"\n", strings.Join(labels, ", "))
	}
	if len(info.Discount) > 0 {
		keys := make([]string, 0, len(info.Discount))
		for k := range info.Discount {
			keys = append(keys, k)
		}
		// Tiers are numeric amount thresholds ("10","100","1000"); sort by value, not lexically, or "1000" would sort before "20".
		sort.Slice(keys, func(i, j int) bool {
			fi, _ := strconv.ParseFloat(keys[i], 64)
			fj, _ := strconv.ParseFloat(keys[j], 64)
			return fi < fj
		})
		cliout.Println("\n" + i18n.T("wallet.label.discount_tiers"))
		for _, k := range keys {
			cliout.Printf("  ≥%s → ×%g\n", cliout.Sanitize(k), info.Discount[k])
		}
	}
	if info.TopupLink != "" {
		cliout.Printf("\nDashboard top-up: %s\n", cliout.Sanitize(info.TopupLink))
	}
	cliout.Println("\nFor card / e-wallet payments use 'everyapi wallet topup' (browser flow).")
	return nil
}

func runRedeem(args []string) error {
	if len(args) == 0 {
		return errors.New(i18n.T("wallet.usage_redeem"))
	}
	// Intercept help BEFORE treating args[0] as a key: fs.Parse below only sees args[1:], so without this `wallet redeem --help` would POST "--help" as a literal key and return "invalid redemption code".
	switch args[0] {
	case "-h", "--help", "help":
		cliout.Println(i18n.T("wallet.usage_redeem"))
		return nil
	}
	// A redemption key is a transferable bearer secret: taking it as a positional arg writes it into shell history and exposes it via `ps` / `/proc/<pid>/cmdline`. Accept `-` to read the key from stdin (one line) so it never lands in argv — the secure invocation, mirroring `docker login --password-stdin`. The positional form stays for back-compat.
	var key string
	if args[0] == "-" {
		sc := bufio.NewScanner(os.Stdin)
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return fmt.Errorf("%s: %w", i18n.T("wallet.usage_redeem"), err)
			}
			return errors.New(i18n.T("wallet.usage_redeem"))
		}
		key = strings.TrimSpace(sc.Text())
		if key == "" {
			return errors.New(i18n.T("wallet.usage_redeem"))
		}
	} else {
		key = args[0]
	}
	fs := flag.NewFlagSet("wallet redeem", flag.ContinueOnError)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	quota, err := client.Redeem(cliout.WithCtx(), key)
	if err != nil {
		return classifyErr(err)
	}
	cliout.Println(styledRedeemed(quota))
	return nil
}
