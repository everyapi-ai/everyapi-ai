package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

type codexUserDefaults struct {
	Model                string `toml:"model"`
	ModelReasoningEffort string `toml:"model_reasoning_effort"`
}

const preparedCodexProfileFile = ".everyapi-codex-profile"

// prepareCodex generates the isolated auth and provider configuration Codex reads on startup:
//
//   - auth.json pins auth_mode to "apikey", so Codex skips its ChatGPT device-login dance even when the user's real ~/.codex/auth.json is in chatgpt mode.
//   - config.toml defines an `everyapi` model_provider pointing at <apiBase>/v1 (wire_api = "responses" — the gateway exposes the full /v1/responses surface, see backend/internal/router/relay-router.go) and selects it as the default model_provider. Codex does NOT read OPENAI_BASE_URL on its own; this is how requests actually get routed through EveryAPI.
//
// Live-catalog launches keep sessions in a persistent CODEX_HOME and pass the generated config through a unique, lifecycle-bound Codex profile, so concurrent keys/groups cannot overwrite provider or model metadata. Compatibility callers that do not provide a catalog retain the legacy fixed config.
//
// Returns the env additions to overlay on top of envFn — primarily CODEX_HOME so codex sees our config dir instead of ~/.codex.
func prepareCodex(apiBase, token string) (map[string]string, error) {
	return prepareCodexWithModels(apiBase, token, nil, "")
}

func prepareCodexWithModels(apiBase, _ string, models []Model, bootModel string) (map[string]string, error) {
	codexHome := ""
	var err error
	if len(models) > 0 {
		codexHome, err = newPreparedHome("codex")
	} else {
		var cfgDir string
		cfgDir, err = config.ConfigDir()
		codexHome = filepath.Join(cfgDir, "codex-home")
	}
	if err != nil {
		return nil, err
	}
	// 0700 keeps Codex's auth/config boundary private even though auth.json carries
	// only a launch-independent placeholder; the real relay key stays process-scoped.
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return nil, fmt.Errorf("create codex-home: %w", err)
	}

	if err := writeCodexAuthJSON(codexHome, transparentPlaceholderCredential); err != nil {
		if len(models) > 0 {
			removePreparedHomeAfterQuiet(codexHome)
		}
		return nil, err
	}
	catalogPath, err := writeCodexModelCatalog(codexHome, models)
	if err != nil {
		if len(models) > 0 {
			removePreparedHomeAfterQuiet(codexHome)
		}
		return nil, err
	}
	if err := writeCodexConfigTOMLWithCatalog(codexHome, apiBase, catalogPath, bootModel); err != nil {
		if len(models) > 0 {
			removePreparedHomeAfterQuiet(codexHome)
		}
		return nil, err
	}

	if len(models) > 0 {
		return preparedCodexHomeEnv(codexHome)
	}
	return map[string]string{"CODEX_HOME": codexHome}, nil
}

// prepareCodexTransparent keeps Codex on its built-in OpenAI provider while forcing API-key auth with a non-secret placeholder. Connector replaces that placeholder only after decrypting a registered api.openai.com model route.
func prepareCodexTransparent() (map[string]string, error) {
	return prepareCodexTransparentWithModels(nil, "")
}

func prepareCodexTransparentWithModels(models []Model, bootModel string) (map[string]string, error) {
	codexHome := ""
	var err error
	if len(models) > 0 {
		codexHome, err = newPreparedHome("codex")
	} else {
		var cfgDir string
		cfgDir, err = config.ConfigDir()
		codexHome = filepath.Join(cfgDir, "codex-home")
	}
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return nil, fmt.Errorf("create codex-home: %w", err)
	}
	if err := writeCodexAuthJSON(codexHome, transparentPlaceholderCredential); err != nil {
		if len(models) > 0 {
			removePreparedHomeAfterQuiet(codexHome)
		}
		return nil, err
	}
	catalogPath, err := writeCodexModelCatalog(codexHome, models)
	if err != nil {
		if len(models) > 0 {
			removePreparedHomeAfterQuiet(codexHome)
		}
		return nil, err
	}
	if err := writeCodexOfficialConfigTOMLWithCatalog(codexHome, catalogPath, bootModel); err != nil {
		if len(models) > 0 {
			removePreparedHomeAfterQuiet(codexHome)
		}
		return nil, err
	}
	if len(models) > 0 {
		return preparedCodexHomeEnv(codexHome)
	}
	return map[string]string{"CODEX_HOME": codexHome}, nil
}

// preparedCodexHomeEnv keeps per-launch provider and model metadata in a generated Codex profile while using the persistent home as the real CODEX_HOME. Rollouts must be real descendants of CODEX_HOME: Codex canonicalizes both paths before thread/fork (the /btw command), so linking a temporary home's sessions directory to persistent storage makes every rollout look external and rejects the fork.
func preparedCodexHomeEnv(codexHome string) (env map[string]string, err error) {
	defer func() {
		if err != nil {
			removePreparedHomeAfterQuiet(codexHome)
		}
	}()
	configDir, err := config.ConfigDir()
	if err != nil {
		return nil, err
	}
	persistentHome := filepath.Join(configDir, "codex-home")
	if err := os.MkdirAll(persistentHome, 0o700); err != nil {
		return nil, fmt.Errorf("create persistent codex home: %w", err)
	}
	for _, name := range []string{"sessions", "archived_sessions"} {
		if err := os.MkdirAll(filepath.Join(persistentHome, name), 0o700); err != nil {
			return nil, fmt.Errorf("create persistent Codex %s: %w", name, err)
		}
	}
	if err := writeCodexAuthJSON(persistentHome, transparentPlaceholderCredential); err != nil {
		return nil, err
	}
	// --profile layers on top of the root config. Clear legacy routing/model assignments only after the generated profile has inherited the one supported migration value (reasoning effort), otherwise a stale fixed-home model can silently override a launch where the user made no selection.
	if err := writeFileAtomic(
		filepath.Join(persistentHome, "config.toml"),
		[]byte("# EveryAPI routes Codex through a lifecycle-bound --profile.\n"),
		0o644,
	); err != nil {
		return nil, err
	}
	profileBody, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		return nil, fmt.Errorf("read prepared Codex profile: %w", err)
	}
	profileName := "everyapi-" + filepath.Base(codexHome)
	profilePath := filepath.Join(persistentHome, profileName+".config.toml")
	if err := os.WriteFile(filepath.Join(codexHome, preparedCodexProfileFile), []byte(profilePath), 0o600); err != nil {
		return nil, fmt.Errorf("record prepared Codex profile: %w", err)
	}
	// Record ownership before creating the persistent resource. A hard kill can
	// now leave a harmless marker with no profile, never an untracked profile.
	if err := writeFileAtomic(profilePath, profileBody, 0o600); err != nil {
		return nil, fmt.Errorf("write prepared Codex profile: %w", err)
	}
	args, err := json.Marshal([]string{"--profile", profileName})
	if err != nil {
		_ = os.Remove(profilePath)
		return nil, fmt.Errorf("encode prepared Codex profile arguments: %w", err)
	}
	env = preparedHomeEnv("CODEX_HOME", codexHome)
	env["CODEX_HOME"] = persistentHome
	env[preparedArgvMarker] = string(args)
	return env, nil
}

// removePreparedCodexProfile removes the profile owned by one prepared home. The marker is validated against EveryAPI's persistent Codex home before deletion so a corrupted prepared directory cannot turn cleanup into an arbitrary-file remove. Callers retain the marker-bearing prepared home on failure so a later orphan sweep can retry.
func removePreparedCodexProfile(home string) error {
	configDir, err := config.ConfigDir()
	if err != nil {
		return err
	}
	persistentHome := filepath.Join(configDir, "codex-home")
	profilePath := ""
	if body, readErr := os.ReadFile(filepath.Join(home, preparedCodexProfileFile)); readErr == nil {
		candidate := strings.TrimSpace(string(body))
		rel, relErr := filepath.Rel(persistentHome, candidate)
		if relErr == nil && !filepath.IsAbs(rel) && filepath.Dir(rel) == "." && strings.HasPrefix(rel, "everyapi-codex-") && strings.HasSuffix(rel, ".config.toml") {
			profilePath = candidate
		}
	}
	if profilePath == "" {
		// Older/interrupted launches may have been killed between profile creation
		// and marker creation. The name is derived from the prepared home, including
		// after the stale sweep renames that home into the .reaping-* namespace.
		homeName := strings.TrimPrefix(filepath.Base(home), preparedHomeReapPrefix)
		if !strings.HasPrefix(homeName, "codex-") {
			return nil
		}
		profilePath = filepath.Join(persistentHome, "everyapi-"+homeName+".config.toml")
	}
	if err := os.Remove(profilePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove prepared Codex profile: %w", err)
	}
	return nil
}

func writeCodexOfficialConfigTOML(codexHome string) error {
	return writeCodexOfficialConfigTOMLWithCatalog(codexHome, "", "")
}

func writeCodexOfficialConfigTOMLWithCatalog(codexHome, catalogPath, bootModel string) error {
	defaults, err := readCodexUserDefaults(codexHome)
	if err != nil {
		return err
	}
	applySelectedCodexReasoningEffort(&defaults)
	if err := inheritPersistentCodexReasoningEffort(&defaults, codexHome, catalogPath); err != nil {
		return err
	}
	// Same fresh-home problem as the injected path, and this is the one that matters more: transparent mode is the DEFAULT for codex, so seeding only the injected path would leave `everyapi use codex` — the plain command — still booting on whatever the catalogue happened to list first.
	if defaults.Model == "" {
		defaults.Model = bootModel
	}
	encodedDefaults, err := encodeCodexUserDefaults(defaults)
	if err != nil {
		return err
	}
	body := []byte("# Auto-generated by `everyapi use codex --transparent`.\n" +
		"# Uses Codex's built-in OpenAI provider and official API origin.\n" +
		"# Root-level model and model_reasoning_effort values are preserved.\n\n" +
		"model_provider = \"openai\"\n" + codexAgentInstructionsConfig() + codexCatalogConfig(catalogPath) + encodedDefaults)
	return writeFileAtomic(filepath.Join(codexHome, "config.toml"), body, 0o644)
}

// writeCodexAuthJSON atomically writes auth.json with apikey mode + the supplied credential. Schema mirrors what codex itself emits for the `codex login --api-key ...` path: OPENAI_API_KEY top-level (NOT nested under tokens), tokens nulled out, auth_mode pinned. EveryAPI launch paths pass a launch-independent placeholder; the real relay key comes from the child process environment.
//
// 0600 perms — same as how codex writes its own auth.json.
func writeCodexAuthJSON(codexHome, token string) error {
	payload := map[string]any{
		"OPENAI_API_KEY": token,
		"tokens":         nil,
		"auth_mode":      "apikey",
	}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal codex auth.json: %w", err)
	}
	return writeFileAtomic(filepath.Join(codexHome, "auth.json"), body, 0o600)
}

// writeCodexConfigTOML writes config.toml selecting the everyapi model_provider as default. We do NOT copy the user's real ~/.codex/config.toml — the user explicitly chose isolation, so personality/theme/etc. start fresh. The EveryAPI-owned provider configuration is regenerated on every `everyapi use codex` launch.
//
// wire_api = "responses" matches the backend's native /v1/responses surface. requires_openai_auth = false because EveryAPI's relay key is its own credential, not an OpenAI ChatGPT session token.
func writeCodexConfigTOML(codexHome, apiBase string) error {
	return writeCodexConfigTOMLWithCatalog(codexHome, apiBase, "", "")
}

func writeCodexConfigTOMLWithCatalog(codexHome, apiBase, catalogPath, bootModel string) error {
	base := joinBase(apiBase, "/v1")
	defaults, err := readCodexUserDefaults(codexHome)
	if err != nil {
		return err
	}
	applySelectedCodexReasoningEffort(&defaults)
	if err := inheritPersistentCodexReasoningEffort(&defaults, codexHome, catalogPath); err != nil {
		return err
	}
	// A live-catalog launch gets a fresh per-launch profile, so the read above finds no model to preserve. The selection EveryAPI persisted is the value that survives across launches. A model set in the legacy fixed config still wins, so this only fills a gap.
	if defaults.Model == "" {
		defaults.Model = bootModel
	}
	encodedDefaults, err := encodeCodexUserDefaults(defaults)
	if err != nil {
		return err
	}
	// TOML built by string concat — small and shape-stable, not worth dragging in a TOML writer dep just for two stanzas. base_url is defensively escaped as a TOML basic string (mirroring hermes's yamlDoubleQuote) so a stray quote / newline in a user-supplied --api-base can't break out of the value or inject a key into the [model_providers.everyapi] stanza.
	var b strings.Builder
	b.WriteString("# Auto-generated by `everyapi use codex`.\n")
	b.WriteString("# EveryAPI regenerates the provider configuration on every launch.\n")
	b.WriteString("# Root-level model and model_reasoning_effort values are preserved.\n")
	b.WriteString("\n")
	b.WriteString("model_provider = \"everyapi\"\n")
	b.WriteString(codexAgentInstructionsConfig())
	b.WriteString(codexCatalogConfig(catalogPath))
	b.WriteString(encodedDefaults)
	b.WriteString("\n")
	b.WriteString("[model_providers.everyapi]\n")
	b.WriteString("name = \"EveryAPI\"\n")
	b.WriteString("base_url = " + tomlBasicQuote(base) + "\n")
	b.WriteString("env_key = \"OPENAI_API_KEY\"\n")
	// Pin the routing surface: codex's WireApi default is Chat, which would hit /v1/chat/completions. We want the gateway's native /v1/responses surface (see the doc comment above).
	b.WriteString("wire_api = \"responses\"\n")
	// The gateway exposes Codex's standalone search companion endpoint beside
	// /v1/responses. Custom providers default this capability off, which makes
	// `everyapi use codex --transparent=false` silently omit the web tool.
	b.WriteString("supports_standalone_web_search = true\n")
	// EveryAPI's relay key is its own credential, not an OpenAI ChatGPT session token (already the codex default; written to match intent).
	b.WriteString("requires_openai_auth = false\n")
	// Static attribution is safe because it identifies the client kind, not a
	// logical conversation. Codex itself owns and emits resumable thread/session
	// identifiers; never mint X-EveryAPI-Session per launcher process.
	b.WriteString("http_headers = { \"X-EveryAPI-Agent\" = \"codex\" }\n")
	return writeFileAtomic(filepath.Join(codexHome, "config.toml"), []byte(b.String()), 0o644)
}

func codexAgentInstructionsConfig() string {
	instructions := AgentInstructions()
	if instructions == "" {
		return ""
	}
	return "developer_instructions = " + tomlBasicQuote(instructions) + "\n"
}

func codexCatalogConfig(path string) string {
	if path == "" {
		return ""
	}
	return "model_catalog_json = " + tomlBasicQuote(path) + "\n"
}

// codexBundledCatalog reads Codex's own model metadata. Two places need it in a single launch — the reasoning-level picker and writeCodexModelCatalog — and the answer cannot change mid-launch, so the default implementation is memoized to spawn the process once rather than twice on the critical path.
//
// The memo lives inside the default value rather than wrapping the variable, because tests replace the variable wholesale; a swapped-in function is called directly and never sees this cache.
var codexBundledCatalog = sync.OnceValues(func() ([]byte, error) {
	output, err := exec.Command("codex", "debug", "models", "--bundled").Output()
	if err != nil {
		return nil, fmt.Errorf("read bundled Codex model metadata (update Codex CLI if `debug models --bundled` is unavailable): %w", err)
	}
	return output, nil
})

func writeCodexModelCatalog(codexHome string, models []Model) (string, error) {
	if len(models) == 0 {
		return "", nil
	}
	body, err := codexBundledCatalog()
	if err != nil {
		return "", err
	}
	var bundled struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(body, &bundled); err != nil || len(bundled.Models) == 0 {
		if err == nil {
			err = errors.New("catalog contains no models")
		}
		return "", fmt.Errorf("parse bundled Codex model metadata: %w", err)
	}
	bySlug := make(map[string]map[string]any, len(bundled.Models))
	for _, model := range bundled.Models {
		if slug, _ := model["slug"].(string); slug != "" {
			bySlug[slug] = model
		}
	}
	generated := make([]map[string]any, 0, len(models))
	seen := map[string]bool{}
	for priority, model := range models {
		if strings.TrimSpace(model.ID) == "" || seen[model.ID] {
			continue
		}
		seen[model.ID] = true
		template := bundled.Models[0]
		if exact := bySlug[model.ID]; exact != nil {
			template = exact
		}
		encoded, _ := json.Marshal(template)
		var entry map[string]any
		if err := json.Unmarshal(encoded, &entry); err != nil {
			return "", fmt.Errorf("clone Codex model metadata: %w", err)
		}
		entry["slug"] = model.ID
		entry["display_name"] = model.ID
		entry["description"] = "Available through EveryAPI"
		entry["visibility"] = "list"
		entry["supported_in_api"] = true
		entry["priority"] = priority + 1
		if bySlug[model.ID] == nil {
			entry["default_reasoning_level"] = nil
			entry["supported_reasoning_levels"] = []any{}
			entry["additional_speed_tiers"] = []any{}
			entry["service_tiers"] = []any{}
			entry["support_verbosity"] = false
			entry["default_verbosity"] = nil
			entry["availability_nux"] = nil
			entry["upgrade"] = nil
		}
		generated = append(generated, entry)
	}
	if len(generated) == 0 {
		return "", errors.New("write Codex model catalog: no usable model ids")
	}
	payload, err := json.MarshalIndent(map[string]any{"models": generated}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode Codex model catalog: %w", err)
	}
	path := filepath.Join(codexHome, "models.json")
	if err := writeFileAtomic(path, append(payload, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func readCodexUserDefaults(codexHome string) (codexUserDefaults, error) {
	configPath := filepath.Join(codexHome, "config.toml")
	body, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return codexUserDefaults{}, nil
		}
		return codexUserDefaults{}, fmt.Errorf("read existing Codex config: %w", err)
	}

	var defaults codexUserDefaults
	if _, err := toml.Decode(string(body), &defaults); err != nil {
		return codexUserDefaults{}, fmt.Errorf("parse existing Codex config: %w", err)
	}
	return defaults, nil
}

// applySelectedCodexReasoningEffort pins the effort chosen at launch. It runs before the two fallbacks — the effort recorded in this home's config.toml, then the one inherited from the persistent home — because those answer "what did the user last leave this at", and an explicit choice made seconds ago outranks both. An unset variable changes nothing, which is what keeps every non-picker caller (a scripted launch, the legacy fixed-home path) on its previous behavior.
func applySelectedCodexReasoningEffort(defaults *codexUserDefaults) {
	if level := selectedReasoningLevel(); level != "" {
		defaults.ModelReasoningEffort = level
	}
}

func inheritPersistentCodexReasoningEffort(defaults *codexUserDefaults, codexHome, catalogPath string) error {
	if defaults.ModelReasoningEffort != "" || catalogPath == "" {
		return nil
	}
	configDir, err := config.ConfigDir()
	if err != nil {
		return err
	}
	persistentHome := filepath.Join(configDir, "codex-home")
	if filepath.Clean(persistentHome) == filepath.Clean(codexHome) {
		return nil
	}
	persistentDefaults, err := readCodexUserDefaults(persistentHome)
	if err != nil {
		return err
	}
	defaults.ModelReasoningEffort = persistentDefaults.ModelReasoningEffort
	return nil
}

func encodeCodexUserDefaults(defaults codexUserDefaults) (string, error) {
	var b strings.Builder
	config := struct {
		Model                string `toml:"model,omitempty"`
		ModelReasoningEffort string `toml:"model_reasoning_effort,omitempty"`
	}{
		Model:                defaults.Model,
		ModelReasoningEffort: defaults.ModelReasoningEffort,
	}
	if err := toml.NewEncoder(&b).Encode(config); err != nil {
		return "", fmt.Errorf("encode Codex user defaults: %w", err)
	}
	return b.String(), nil
}

// tomlBasicQuote renders s as a TOML basic string, escaping the characters that would otherwise terminate the string or inject structure. The escape forms (\\, \", \n, \r, \t) are valid in both TOML basic strings and YAML double-quoted scalars, so this mirrors hermes's yamlDoubleQuote. Sufficient for base_url; not a general TOML serializer.
func tomlBasicQuote(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	)
	return `"` + r.Replace(s) + `"`
}

// writeFileAtomic writes via tmp + rename so a concurrent codex read never sees a half-written file. tmp lives in the same directory so the rename is atomic (cross-FS renames degrade to copy+delete).
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}
