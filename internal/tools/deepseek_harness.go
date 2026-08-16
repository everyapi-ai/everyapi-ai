package tools

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const deepSeekHarnessCredentialRef = "EVERYAPI_API_KEY"

func prepareDeepSeekHarnessWithModels(
	apiBase, token string,
	models []Model,
) (map[string]string, error) {
	compatible := make([]Model, 0, len(models))
	for _, model := range models {
		if model.ID != "" && containsEndpoint(model.SupportedEndpointTypes, "openai") {
			compatible = append(compatible, model)
		}
	}
	if len(compatible) == 0 {
		return nil, fmt.Errorf("DeepSeek Harness requires at least one OpenAI chat-completions model")
	}
	dshHome, err := deepSeekHarnessHome()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dshHome, 0o700); err != nil {
		return nil, fmt.Errorf("create DeepSeek Harness home: %w", err)
	}
	if err := os.Chmod(dshHome, 0o700); err != nil {
		return nil, fmt.Errorf("protect DeepSeek Harness home: %w", err)
	}
	settingsPath := filepath.Join(dshHome, "settings.yaml")
	settings, err := loadYAMLMapping(settingsPath)
	if err != nil {
		return nil, err
	}
	providers, err := ensureYAMLMapPath(settings, "llm-pi-ai", "providers")
	if err != nil {
		return nil, fmt.Errorf("DeepSeek Harness settings %s: %w", settingsPath, err)
	}
	setYAMLMapValue(providers, "everyapi", deepSeekHarnessProviderNode(apiBase, compatible))

	credentialsPath := filepath.Join(dshHome, ".credentials.yaml")
	credentials, err := loadYAMLMapping(credentialsPath)
	if err != nil {
		return nil, err
	}
	setYAMLMapValue(credentials, deepSeekHarnessCredentialRef, scalarYAMLNode(token))
	if err := writeYAMLPrivate(credentialsPath, credentials); err != nil {
		return nil, err
	}

	if err := writeYAMLPrivate(settingsPath, settings); err != nil {
		return nil, err
	}
	return map[string]string{}, nil
}

func deepSeekHarnessHome() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("DSH_HOME")); configured != "" {
		resolved, err := filepath.Abs(configured)
		if err != nil {
			return "", fmt.Errorf("resolve DSH_HOME: %w", err)
		}
		return resolved, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve DeepSeek Harness home: %w", err)
	}
	return filepath.Join(home, ".dsh"), nil
}

func containsEndpoint(endpoints []string, want string) bool {
	for _, endpoint := range endpoints {
		if endpoint == want {
			return true
		}
	}
	return false
}

func deepSeekHarnessProviderNode(apiBase string, models []Model) *yaml.Node {
	provider := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setYAMLMapValue(provider, "displayName", scalarYAMLNode("EveryAPI"))
	setYAMLMapValue(provider, "apiKeyEnv", scalarYAMLNode(deepSeekHarnessCredentialRef))
	setYAMLMapValue(provider, "api", scalarYAMLNode("openai-completions"))
	setYAMLMapValue(provider, "baseURL", scalarYAMLNode(joinBase(apiBase, "/v1")))
	modelList := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, model := range models {
		entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setYAMLMapValue(entry, "id", scalarYAMLNode(model.ID))
		modelList.Content = append(modelList.Content, entry)
	}
	setYAMLMapValue(provider, "models", modelList)
	return provider
}

func loadYAMLMapping(path string) (*yaml.Node, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("refuse unsafe DeepSeek Harness config path %s", path)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect DeepSeek Harness config %s: %w", path, err)
	}
	document := &yaml.Node{Kind: yaml.DocumentNode}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		document.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
		return document.Content[0], nil
	}
	if err != nil {
		return nil, fmt.Errorf("read DeepSeek Harness config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(body, document); err != nil {
		return nil, fmt.Errorf("parse DeepSeek Harness config %s: %w", path, err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("DeepSeek Harness config %s must be a YAML mapping", path)
	}
	return document.Content[0], nil
}

func ensureYAMLMapPath(root *yaml.Node, keys ...string) (*yaml.Node, error) {
	current := root
	path := make([]string, 0, len(keys))
	for _, key := range keys {
		path = append(path, key)
		next := yamlMapValue(current, key)
		if next == nil {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setYAMLMapValue(current, key, next)
		} else if next.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s must be a YAML mapping", strings.Join(path, "."))
		}
		current = next
	}
	return current, nil
}

func yamlMapValue(mapping *yaml.Node, key string) *yaml.Node {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func setYAMLMapValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			mapping.Content[index+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, scalarYAMLNode(key), value)
}

func scalarYAMLNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func writeYAMLPrivate(path string, root *yaml.Node) error {
	document := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	var body bytes.Buffer
	encoder := yaml.NewEncoder(&body)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return fmt.Errorf("encode DeepSeek Harness config %s: %w", path, err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("finish DeepSeek Harness config %s: %w", path, err)
	}
	if strings.TrimSpace(body.String()) == "" {
		return fmt.Errorf("refuse empty DeepSeek Harness config %s", path)
	}
	return writeFileAtomic(path, body.Bytes(), 0o600)
}
