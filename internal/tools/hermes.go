package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

// hermesModelEnv lets a user override the boot model without editing
// the generated config: `EVERYAPI_HERMES_MODEL=gpt-5.1 everyapi use hermes`.
const hermesModelEnv = "EVERYAPI_HERMES_MODEL"

// prepareHermes generates an isolated HERMES_HOME under
// ~/.config/everyapi/hermes-home and seeds it with the single file
// hermes reads to resolve its main model — config.yaml:
//
//   - provider: custom    → hermes trusts model.base_url verbatim
//     (see _config_base_url_trustworthy_for_bare_custom in hermes'
//     runtime_provider.py: a `custom` provider trusts any base_url)
//     and uses model.api_key as the request credential. Without this
//     hermes falls back to OpenRouter, and OPENAI_API_KEY only
//     attaches when base_url's host is openai.com — so an EveryAPI
//     host would otherwise go out unauthenticated and 401.
//   - base_url: <apiBase>/v1 → EveryAPI's OpenAI-compatible surface.
//   - api_key:  the relay key, inlined (hermes reads model.api_key
//     for config-driven custom endpoints).
//   - api_mode: chat_completions → EveryAPI exposes every model
//     (claude AND gpt) under /v1/chat/completions, so pinning this
//     keeps the transport stable across `hermes model` switches.
//
// Writing config.yaml also satisfies hermes' first-run guard
// (_has_any_provider_configured), so launch skips the setup wizard.
//
// HERMES_HOME is redirected (not ~/.hermes) so the user's real hermes
// config stays untouched for plain `hermes` invocations. The directory
// is reused across runs (sessions/logs accumulate there per the
// user's opt-in to a session-isolated EveryAPI environment);
// config.yaml is rewritten every call so a fresh relay key always
// wins.
func prepareHermes(apiBase, token string) (map[string]string, error) {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve everyapi config dir: %w", err)
	}
	hermesHome := filepath.Join(cfgDir, "hermes-home")
	// 0700 — config.yaml carries an inline bearer token; treat the
	// whole directory as secret. MkdirAll is a no-op when it already
	// exists with the right permissions.
	if err := os.MkdirAll(hermesHome, 0o700); err != nil {
		return nil, fmt.Errorf("create hermes-home: %w", err)
	}

	if err := writeHermesConfigYAML(hermesHome, apiBase, token); err != nil {
		return nil, err
	}

	return map[string]string{"HERMES_HOME": hermesHome}, nil
}

// LastHermesModel returns the model pinned in the most recently
// generated hermes config.yaml, or "" if none exists yet (first run,
// unreadable file, or no recognizable default: line). `everyapi use
// hermes` uses it to default its model picker to your previous choice
// instead of snapping back to the first model in the current catalog.
func LastHermesModel() string {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return ""
	}
	body, err := os.ReadFile(filepath.Join(cfgDir, "hermes-home", "config.yaml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		// The writer emits exactly `  default: "<model>"`. Match the
		// key, then pull the double-quoted scalar back out.
		if !strings.HasPrefix(strings.TrimSpace(line), "default:") {
			continue
		}
		lo := strings.IndexByte(line, '"')
		hi := strings.LastIndexByte(line, '"')
		if lo < 0 || hi <= lo {
			return ""
		}
		return yamlUnquoteInner(line[lo+1 : hi])
	}
	return ""
}

// yamlUnquoteInner reverses yamlDoubleQuote's escaping for the inner
// bytes of a double-quoted scalar (quotes already stripped). Kept in
// lockstep with yamlDoubleQuote.
func yamlUnquoteInner(s string) string {
	r := strings.NewReplacer(
		`\\`, `\`,
		`\"`, `"`,
		`\n`, "\n",
		`\r`, "\r",
		`\t`, "\t",
	)
	return r.Replace(s)
}

// writeHermesConfigYAML atomically writes config.yaml selecting the
// everyapi custom provider as the main model. The file is FULLY
// REGENERATED on every `everyapi use hermes` launch: any edits a user
// makes here are wiped on the next run. Customization belongs in
// ~/.hermes (untouched) for plain `hermes` invocations.
//
// 0600 perms — the inline api_key is a relay credential.
//
// YAML is built by string concat (the shape is small and stable), but
// the three interpolated values are run through yamlDoubleQuote rather
// than concatenated raw: base_url and api_key are charset-safe in
// practice, but model comes from EVERYAPI_HERMES_MODEL — user-supplied
// env — and a stray `"` or newline there would otherwise break the YAML
// or inject sibling keys. Escaping all three keeps the writer correct
// regardless of input.
func writeHermesConfigYAML(hermesHome, apiBase, token string) error {
	base := joinBase(apiBase, "/v1")
	model := strings.TrimSpace(os.Getenv(hermesModelEnv))
	if model == "" {
		return fmt.Errorf("%s is empty: resolve a live catalog model before preparing hermes", hermesModelEnv)
	}

	var b strings.Builder
	b.WriteString("# Auto-generated by `everyapi use hermes` — fully regenerated on every launch.\n")
	b.WriteString("# Do not edit; any changes will be lost on the next run.\n")
	b.WriteString("# To customize hermes outside everyapi, edit ~/.hermes (untouched).\n")
	b.WriteString("#\n")
	b.WriteString("# provider=custom pins hermes at EveryAPI's OpenAI-compatible /v1\n")
	b.WriteString("# endpoint with your relay key. Both claude and gpt models are\n")
	b.WriteString("# reachable here — switch with `hermes model`, or set\n")
	b.WriteString("# EVERYAPI_HERMES_MODEL before launch to change this boot default.\n")
	b.WriteString("model:\n")
	b.WriteString("  provider: custom\n")
	b.WriteString("  default: " + yamlDoubleQuote(model) + "\n")
	b.WriteString("  base_url: " + yamlDoubleQuote(base) + "\n")
	b.WriteString("  api_key: " + yamlDoubleQuote(token) + "\n")
	b.WriteString("  api_mode: chat_completions\n")
	return writeFileAtomic(filepath.Join(hermesHome, "config.yaml"), []byte(b.String()), 0o600)
}

// yamlDoubleQuote renders s as a YAML double-quoted scalar, escaping the
// characters that would otherwise terminate the scalar or inject
// structure (`\`, `"`) plus the common control chars YAML spells with
// backslash escapes. Sufficient for the values written here (model,
// base_url, relay key); not a general-purpose YAML serializer.
func yamlDoubleQuote(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	)
	return `"` + r.Replace(s) + `"`
}
