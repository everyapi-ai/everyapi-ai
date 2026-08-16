package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/credentiallock"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func acquireCredentialLock() (func(), error) { return credentiallock.Acquire() }

// credentialProtocolVersion is incremented only for incompatible output-shape changes. Consumers must reject versions they do not understand.
const credentialProtocolVersion = 1

type credentialOutput struct {
	Version   int                      `json:"version"`
	BaseURL   string                   `json:"base_url"`
	APIKey    string                   `json:"api_key"`
	ExpiresAt string                   `json:"expires_at,omitempty"`
	Models    *[]credentialOutputModel `json:"models,omitempty"`
}

type credentialOutputModel struct {
	ID                     string   `json:"id"`
	OwnedBy                string   `json:"owned_by,omitempty"`
	SupportedEndpointTypes []string `json:"supported_endpoint_types"`
	ChatCompletionsBridge  bool     `json:"chat_completions_bridge,omitempty"`
}

// credentialError keeps machine failures stable while allowing the existing top-level CLI error renderer to add localized human-facing framing.
type credentialError struct {
	code string
	err  error
}

func (e *credentialError) Error() string {
	return fmt.Sprintf("EVERYAPI_CREDENTIAL_ERROR:%s: %v", e.code, e.err)
}

func (e *credentialError) Unwrap() error { return e.err }

func machineCredentialError(code string, err error) error {
	return &credentialError{code: code, err: err}
}

// Credential implements the non-interactive credential-process contract used by local integrations. It never prompts and writes exactly one JSON object to stdout on success.
func Credential(args []string) error {
	fs := flag.NewFlagSet("auth credential", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "json", "output format (json)")
	invalidate := fs.Bool("invalidate", false, "invalidate a rejected cached relay key")
	includeModels := fs.Bool("include-models", false, "include the relay-scoped model catalog")
	if err := fs.Parse(args); err != nil {
		return machineCredentialError("invalid_request", err)
	}
	if fs.NArg() != 0 {
		return machineCredentialError("invalid_request", errors.New("unexpected positional arguments"))
	}
	if !strings.EqualFold(strings.TrimSpace(*format), "json") {
		return machineCredentialError("invalid_request", fmt.Errorf("unsupported format %q", *format))
	}
	unlock, err := credentiallock.AcquireTimeout(5 * time.Second)
	if err != nil {
		return machineCredentialError("unavailable", fmt.Errorf("lock credential cache: %w", err))
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()

	// Load only after taking the cross-process lock. OAuth refresh tokens rotate on use, so every contender must observe the credentials saved by the previous process before deciding whether another refresh is needed.
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return machineCredentialError("not_logged_in", err)
	}
	if err != nil {
		return machineCredentialError("invalid_credentials", err)
	}
	var key string
	if *invalidate && creds.RefreshToken != "" && creds.OAuthClientID != "" {
		key, err = api.RefreshOAuthRelayKey(context.Background(), creds)
	} else {
		if *invalidate {
			if err := api.InvalidateCachedRelayKey(creds); err != nil {
				return machineCredentialError("unavailable", fmt.Errorf("invalidate relay key: %w", err))
			}
		}
		key, err = api.ResolveRelayKey(context.Background(), creds, "")
	}
	if err != nil {
		var cacheErr *api.ErrCacheSave
		if key != "" && errors.As(err, &cacheErr) {
			// The freshly rotated key is usable for this invocation even though the cache write failed. Keep stdout valid and leave a key-free diagnostic on stderr.
			fmt.Fprintf(os.Stderr, "Warning: EveryAPI could not cache the refreshed relay key: %v\n", cacheErr)
		} else if errors.Is(err, api.ErrNoRelayKey) {
			return machineCredentialError("no_relay_key", err)
		} else {
			return machineCredentialError("unavailable", err)
		}
	}
	if strings.TrimSpace(key) == "" {
		return machineCredentialError("no_relay_key", api.ErrNoRelayKey)
	}

	apiBase := strings.TrimRight(config.ResolveAPIBaseForBase(creds.APIBase), "/")
	baseURL := apiBase + "/v1"
	out := credentialOutput{
		Version: credentialProtocolVersion,
		BaseURL: baseURL,
		APIKey:  key,
	}
	if creds.RelayKeyExpiresAt > 0 {
		out.ExpiresAt = time.Unix(creds.RelayKeyExpiresAt, 0).UTC().Format(time.RFC3339)
	}
	// The key and any rotated refresh token are persisted now. Do not hold the credential-file lock across the optional model-directory network call; login/logout and ordinary credential consumers have a bounded wait.
	unlock()
	locked = false
	if *includeModels {
		models, err := api.New(apiBase, key).RelayModelCatalog(context.Background())
		if err != nil {
			return machineCredentialError("unavailable", fmt.Errorf("load relay model catalog: %w", err))
		}
		sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
		modelOutput := make([]credentialOutputModel, 0, len(models))
		for _, model := range models {
			modelOutput = append(modelOutput, credentialOutputModel{
				ID:                     model.ID,
				OwnedBy:                model.OwnedBy,
				SupportedEndpointTypes: model.SupportedEndpointTypes,
				ChatCompletionsBridge:  model.ChatCompletionsBridge,
			})
		}
		out.Models = &modelOutput
	}
	if err := json.NewEncoder(cliout.Out).Encode(out); err != nil {
		return machineCredentialError("unavailable", fmt.Errorf("write credential JSON: %w", err))
	}
	return nil
}
