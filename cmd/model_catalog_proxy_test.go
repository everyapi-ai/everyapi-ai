package cmd

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/tools"
)

func TestModelCatalogProxyOnlyAdvertisesLaunchModels(t *testing.T) {
	proxyURL, stop, err := startModelCatalogProxy("https://api.invalid", []tools.Model{{ID: "chat-ok"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	resp, err := http.Get(proxyURL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			ID          string `json:"id"`
			Type        string `json:"type"`
			Object      string `json:"object"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
		HasMore bool `json:"has_more"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].ID != "chat-ok" {
		t.Fatalf("catalog = %#v", body.Data)
	}
	if body.Data[0].Type != "model" || body.Data[0].Object != "model" || body.Data[0].DisplayName != "chat-ok" || body.HasMore {
		t.Fatalf("catalog is not compatible with both Anthropic and OpenAI discovery: %#v", body)
	}
}

func TestClaudeCatalogAliasesNonClaudeModelsAndRewritesRequests(t *testing.T) {
	models, aliases := claudeCatalogModels([]tools.Model{{ID: "glm-4.7", OwnedBy: "zhipu"}})
	if len(models) != 1 || models[0].ID == "glm-4.7" || models[0].DisplayName != "glm-4.7" {
		t.Fatalf("Claude catalog models = %#v", models)
	}
	alias := models[0].ID
	if aliases[alias] != "glm-4.7" {
		t.Fatalf("Claude aliases = %#v", aliases)
	}

	var upstreamBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	proxyURL, stop, err := startModelCatalogProxy(upstream.URL, models, aliases)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	payload := []byte(`{"model":"` + alias + `","messages":[{"role":"user","content":"hi"}]}`)
	resp, err := http.Post(proxyURL+"/v1/messages", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if !bytes.Contains(upstreamBody, []byte(`"model":"glm-4.7"`)) || bytes.Contains(upstreamBody, []byte(alias)) {
		t.Fatalf("upstream body was not rewritten: %s", upstreamBody)
	}
}

func TestRewriteModelAliasOnlyChangesTopLevelModel(t *testing.T) {
	const alias = "claude-everyapi-glm"
	original := `{"messages":[{"role":"user","content":"embedded: {\"model\":\"claude-everyapi-glm\"}"}],"model":"claude-everyapi-glm"}`
	req, err := http.NewRequest(http.MethodPost, "http://example.test/v1/messages", strings.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	if err := rewriteModelAlias(req, map[string]string{alias: "glm-4.7"}); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Model    string `json:"model"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Model != "glm-4.7" {
		t.Fatalf("top-level model = %q, want glm-4.7", payload.Model)
	}
	if len(payload.Messages) != 1 || !strings.Contains(payload.Messages[0].Content, alias) {
		t.Fatalf("nested user content was changed: %#v", payload.Messages)
	}
}

func TestTransparentClaudeCatalogAliasReachesGatewayAsRealModel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	models, aliases := claudeCatalogModels([]tools.Model{{ID: "glm-4.7", OwnedBy: "zhipu"}})
	alias := models[0].ID

	var gatewayBody []byte
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gatewayBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer gateway.Close()

	catalogProxy, stopCatalog, err := startModelCatalogProxy(gateway.URL, models, aliases)
	if err != nil {
		t.Fatal(err)
	}
	defer stopCatalog()
	connectorSession, err := startTransparentConnector(catalogProxy, gateway.URL, "relay-key")
	if err != nil {
		t.Fatal(err)
	}
	defer connectorSession.stop()

	caPEM, err := os.ReadFile(connectorSession.caPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("connector CA bundle contains no certificate")
	}
	proxy, err := url.Parse(connectorSession.proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxy),
		TLSClientConfig: &tls.Config{RootCAs: roots},
	}}
	modelsResp, err := client.Get("https://api.anthropic.com/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	var discovered struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(modelsResp.Body).Decode(&discovered); err != nil {
		_ = modelsResp.Body.Close()
		t.Fatal(err)
	}
	_ = modelsResp.Body.Close()
	if len(discovered.Data) != 1 || discovered.Data[0].ID != alias {
		t.Fatalf("transparent Claude discovery = %#v, want alias %q", discovered.Data, alias)
	}
	payload := []byte(`{"model":"` + alias + `","messages":[{"role":"user","content":"hi"}]}`)
	resp, err := client.Post("https://api.anthropic.com/v1/messages", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if !bytes.Contains(gatewayBody, []byte(`"model":"glm-4.7"`)) || bytes.Contains(gatewayBody, []byte(alias)) {
		t.Fatalf("transparent gateway body was not rewritten: %s", gatewayBody)
	}
}
