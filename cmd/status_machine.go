package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const statusMachineProtocolVersion = 1

type statusMachineOutput struct {
	Version  int    `json:"version"`
	SignedIn bool   `json:"signed_in"`
	UserID   int    `json:"user_id,omitempty"`
	Username string `json:"username,omitempty"`
	// AvatarURL is the account's profile picture on the backend's own /api/avatar/:id proxy — same origin as APIBase, never a third-party host. Read from the credential cache so the default (network-free) status call still reports it; refreshed by the --include-balance path below.
	AvatarURL  string   `json:"avatar_url,omitempty"`
	APIBase    string   `json:"api_base,omitempty"`
	ExpiresAt  string   `json:"expires_at,omitempty"`
	BalanceUSD *float64 `json:"balance_usd,omitempty"`
}

type statusMachineError struct {
	code string
	err  error
}

func (e *statusMachineError) Error() string { return "EVERYAPI_STATUS_ERROR:" + e.code }

func (e *statusMachineError) Unwrap() error { return e.err }

func machineStatusError(code string, err error) error {
	return &statusMachineError{code: code, err: err}
}

func statusMachineRequested(args []string) bool {
	for i, arg := range args {
		if arg == "--format=json" || arg == "-format=json" {
			return true
		}
		if (arg == "--format" || arg == "-format") && i+1 < len(args) && args[i+1] == "json" {
			return true
		}
	}
	return false
}

// accountMachineError keeps a rejected credential distinct from an account
// request that merely failed. The desktop redirects to login for
// invalid_credentials, so transport and server failures must stay unavailable.
func accountMachineError(err error) error {
	if api.IsUnauthorized(err) || isLegacyAccountUnauthorized(err) {
		return machineStatusError("invalid_credentials", err)
	}
	return machineStatusError("unavailable", err)
}

// isLegacyAccountUnauthorized is intentionally narrower than
// api.IsUnauthorized: only the account self/referral commands need to support
// old gateways that omitted code:"unauthorized" from their HTTP-200 envelope.
// Keep the SDK predicate exact so unrelated callers never make control-flow
// decisions from localized business-error text.
func isLegacyAccountUnauthorized(err error) bool {
	var envelopeError *api.EnvelopeError
	if !errors.As(err, &envelopeError) {
		return false
	}
	message := strings.ToLower(envelopeError.Message)
	message = strings.NewReplacer("_", " ", ".", " ", "-", " ").Replace(message)
	if !strings.Contains(message, "access token") {
		return false
	}
	for _, invalid := range []string{
		"invalid", "not valid", "invalide", "inválido", "no válido", "ungültig",
		"无效", "無效", "無効", "유효하지",
	} {
		if strings.Contains(message, invalid) {
			return true
		}
	}
	return false
}

func credentialLoadMachineError(err error) error {
	if errors.Is(err, config.ErrNoCredentials) {
		return machineStatusError("invalid_credentials", err)
	}
	return machineStatusError("unavailable", err)
}

// statusMachine reports local account metadata by default. includeBalance is an explicit opt-in to one secret-free account request for the desktop popover.
func statusMachine(includeBalance bool) error {
	unlock, err := acquireCredentialLock()
	if err != nil {
		return machineStatusError("unavailable", fmt.Errorf("lock credential cache: %w", err))
	}
	defer unlock()

	out := statusMachineOutput{Version: statusMachineProtocolVersion}
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		if err := json.NewEncoder(cliout.Out).Encode(out); err != nil {
			return machineStatusError("unavailable", err)
		}
		return nil
	}
	if err != nil {
		return credentialLoadMachineError(err)
	}
	out.SignedIn = true
	out.UserID = creds.UserID
	out.Username = strings.TrimSpace(cliout.Sanitize(creds.Username))
	out.AvatarURL = strings.TrimSpace(cliout.Sanitize(creds.AvatarURL))
	out.APIBase = config.ResolveAPIBaseForBase(creds.APIBase)
	if creds.RelayKeyExpiresAt > 0 {
		out.ExpiresAt = time.Unix(creds.RelayKeyExpiresAt, 0).UTC().Format(time.RFC3339)
	}
	if includeBalance {
		client := api.ForCredentials(creds)
		status, err := client.GetStatus(cliout.WithCtx())
		if err != nil {
			return machineStatusError("unavailable", fmt.Errorf("fetch system status: %w", err))
		}
		var quota int64
		if creds.OAuthClientID != "" {
			relayKey, err := resolveRelayKeyLocked(creds, "")
			if err != nil {
				return accountMachineError(fmt.Errorf("resolve relay key: %w", err))
			}
			summary, err := api.New(config.ResolveAPIBaseForBase(creds.APIBase), relayKey).
				GetAccountSummary(cliout.WithCtx())
			if err != nil {
				return accountMachineError(fmt.Errorf("fetch account summary: %w", err))
			}
			quota = summary.Wallet.Quota
		} else {
			self, err := client.GetSelf(cliout.WithCtx())
			if err != nil {
				return accountMachineError(fmt.Errorf("fetch user: %w", err))
			}
			quota = self.Quota
			// This request already carries the current picture, so refresh the cache here rather than making the desktop wait for the next login. A save failure is non-fatal: reporting status is the primary job.
			if self.AvatarURL != creds.AvatarURL {
				creds.AvatarURL = self.AvatarURL
				_ = config.Save(creds)
			}
			out.AvatarURL = strings.TrimSpace(cliout.Sanitize(self.AvatarURL))
		}
		perUnit := status.QuotaPerUnit
		if perUnit <= 0 {
			perUnit = 1
		}
		balanceUSD := float64(quota) / perUnit
		out.BalanceUSD = &balanceUSD
	}
	if err := json.NewEncoder(cliout.Out).Encode(out); err != nil {
		return machineStatusError("unavailable", err)
	}
	return nil
}
