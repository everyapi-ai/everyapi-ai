package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const statusMachineProtocolVersion = 1

type statusMachineOutput struct {
	Version   int    `json:"version"`
	SignedIn  bool   `json:"signed_in"`
	Username  string `json:"username,omitempty"`
	APIBase   string `json:"api_base,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
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

// statusMachine is intentionally local-only. It reports whether a credential
// bundle exists and exposes safe account metadata without touching the network
// or resolving a relay key, so no secret ever needs to enter the desktop core.
func statusMachine() error {
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
		return machineStatusError("invalid_credentials", err)
	}
	out.SignedIn = true
	out.Username = strings.TrimSpace(cliout.Sanitize(creds.Username))
	out.APIBase = config.ResolveAPIBaseForBase(creds.APIBase)
	if creds.RelayKeyExpiresAt > 0 {
		out.ExpiresAt = time.Unix(creds.RelayKeyExpiresAt, 0).UTC().Format(time.RFC3339)
	}
	if err := json.NewEncoder(cliout.Out).Encode(out); err != nil {
		return machineStatusError("unavailable", err)
	}
	return nil
}
