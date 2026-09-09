package tools

import "strings"

// MiMo Code documents inline config and environment substitutions at
// https://mimo.xiaomi.com/mimocode/env-vars and /config-files.
// Reuse the provider catalog while keeping credentials in the child environment.
func prepareMiMoWithModels(apiBase, token string, models []Model, bootModel string) (map[string]string, error) {
	env, err := prepareOpenCodeWithModels(apiBase, token, models, bootModel)
	if err != nil {
		return nil, err
	}
	env["MIMOCODE_CONFIG_CONTENT"] = strings.Replace(env["OPENCODE_CONFIG_CONTENT"], "https://opencode.ai/config.json", "https://mimo.xiaomi.com/mimocode/config.json", 1)
	delete(env, "OPENCODE_CONFIG_CONTENT")
	env["MIMOCODE_DISABLE_PROJECT_CONFIG"] = "true"
	return env, nil
}
