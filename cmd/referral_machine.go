package cmd

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
	"rsc.io/qr"
)

const referralMachineProtocolVersion = 1

// referralQRLevel is the error-correction level for the invite QR. Medium (~15%) is the usual choice for a screen-rendered code: the card is never printed, creased, or partially covered, so the extra redundancy of Q/H would only make the modules smaller — and therefore harder for a phone to resolve — inside a fixed-size card.
const referralQRLevel = qr.M

type referralMachineOutput struct {
	Version  int  `json:"version"`
	SignedIn bool `json:"signed_in"`
	// Code is the account's affiliate code, shown as text next to the QR so it can be dictated over a call.
	Code string `json:"code,omitempty"`
	// InviteURL is what the QR encodes: the dashboard's sign-in page with the referral code attached, exactly the shape the web referral page builds.
	InviteURL string `json:"invite_url,omitempty"`
	QRMIME    string `json:"qr_mime,omitempty"`
	QRData    string `json:"qr_data,omitempty"`
	// InviteCount / PendingRewardUSD describe what the invites earned so far; InviterRewardUSD / InviteeRewardUSD are the operator's per-invite rates. All four are omitted when zero, which the desktop reads as "nothing to say here" rather than "$0.00" — an operator with referral rewards switched off should get no reward copy at all.
	InviteCount      int     `json:"invite_count,omitempty"`
	PendingRewardUSD float64 `json:"pending_reward_usd,omitempty"`
	InviterRewardUSD float64 `json:"inviter_reward_usd,omitempty"`
	InviteeRewardUSD float64 `json:"invitee_reward_usd,omitempty"`
}

// ReferralMachine reports everything the desktop's invite card needs in one call: the affiliate code, the invite URL built from it, that URL rendered as a QR PNG, and the reward figures.
//
// The QR is rendered here rather than in the desktop shell for the same reason the avatar is fetched here (see AvatarMachine): the window's CSP allows no remote image origin, and the shell has no QR encoder — the CLI already carries one through its login flow. Rendering in the sidecar keeps the renderer's image policy closed and adds no dependency on either side.
//
// A signed-out account is not an error: the command reports `signed_in:false` with no fields and the desktop shows its signed-out state.
func ReferralMachine(args []string) error {
	if !statusMachineRequested(args) {
		return machineStatusError("invalid_request", errors.New("referral requires --format=json"))
	}

	unlock, err := acquireCredentialLock()
	if err != nil {
		return machineStatusError("unavailable", fmt.Errorf("lock credential cache: %w", err))
	}
	defer unlock()

	out := referralMachineOutput{Version: referralMachineProtocolVersion}
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return encodeReferralOutput(out)
	}
	if err != nil {
		return credentialLoadMachineError(err)
	}
	out.SignedIn = true

	apiBase := config.ResolveAPIBaseForBase(creds.APIBase)
	client := api.ForCredentials(creds)
	code, err := client.GetAffCode(cliout.WithCtx())
	if err != nil {
		return accountMachineError(fmt.Errorf("fetch affiliate code: %w", err))
	}
	code = strings.TrimSpace(cliout.Sanitize(code))
	if code == "" {
		// The backend lazy-generates on first read, so an empty code means the account surface answered but has nothing usable — there is no invite link to build and no card to show.
		return machineStatusError("unavailable", errors.New("affiliate code is empty"))
	}
	out.Code = code
	inviteURL, err := buildInviteURL(apiBase, code)
	if err != nil {
		return err
	}
	out.InviteURL = inviteURL

	png, err := qr.Encode(out.InviteURL, referralQRLevel)
	if err != nil {
		return machineStatusError("unavailable", fmt.Errorf("render invite qr: %w", err))
	}
	out.QRMIME = "image/png"
	out.QRData = base64.StdEncoding.EncodeToString(png.PNG())

	// The counters and rates are decoration around the link: a gateway that answers the code request but not these still leaves a scannable card, so their failures are swallowed rather than turned into an error the user would read as "invites are broken". The same tolerance extends to the values: a figure the desktop's parsers would refuse (negative, non-finite, or past the 1e12/1e15 ceilings) would otherwise fail every field and hide the QR too, so it is clamped to "nothing to say" as well.
	perUnit := 1.0
	if status, err := client.GetStatus(cliout.WithCtx()); err == nil {
		if status.QuotaPerUnit > 0 {
			perUnit = status.QuotaPerUnit
		}
		out.InviterRewardUSD = referralSanitizeUSD(status.QuotaForInviter / perUnit)
		out.InviteeRewardUSD = referralSanitizeUSD(status.QuotaForInvitee / perUnit)
	}
	if self, err := client.GetSelf(cliout.WithCtx()); err == nil {
		out.InviteCount = referralSanitizeCount(self.AffCount)
		out.PendingRewardUSD = referralSanitizeUSD(float64(self.AffQuota) / perUnit)
	}
	return encodeReferralOutput(out)
}

// referralSanitizeUSD clamps a reward figure to the range the desktop's invite-card parser accepts (finite, 0..=1e12). A value outside that range would make the card's parsers reject the whole payload — QR included — so it is reported as "no reward data" instead.
func referralSanitizeUSD(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > 1e12 {
		return 0
	}
	return value
}

// referralSanitizeCount clamps an invite counter to the 0..=1e15 range the desktop's parser accepts, for the same reason as referralSanitizeUSD.
func referralSanitizeCount(value int) int {
	if value <= 0 || value > 1e15 {
		return 0
	}
	return value
}

// buildInviteURL attaches the affiliate code to the dashboard's sign-in page — the same link shape the web referral page hands out, so a code scanned from the desktop and one copied from the browser land the invitee in the identical place. WebOriginFromBase moves official API hosts to the dashboard host; self-hosted bases pass through and serve the route themselves.
//
// The desktop's invite-card parser only renders https, or http to a loopback host for local development. A base outside that policy would hand the window a link it immediately refuses, so this reports an error rather than advertising a shape no consumer accepts.
func buildInviteURL(apiBase, code string) (string, error) {
	origin := api.WebOriginFromBase(strings.TrimSuffix(apiBase, "/"))
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return "", machineStatusError("unavailable", fmt.Errorf("build invite url: unparseable api base %q", origin))
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return "", machineStatusError("unavailable", fmt.Errorf("build invite url: http base %q is not a loopback host", origin))
	}
	parsed.Path = "/signin"
	query := parsed.Query()
	query.Set("aff", code)
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed.String(), nil
}

// isLoopbackHost reports whether host names the loopback interface, matching the desktop parser's rule for http invite URLs.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func encodeReferralOutput(out referralMachineOutput) error {
	if err := json.NewEncoder(cliout.Out).Encode(out); err != nil {
		return machineStatusError("unavailable", err)
	}
	return nil
}
