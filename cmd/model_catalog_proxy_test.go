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
	"path/filepath"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
)

// startCatalogProxyForTest hosts the catalogue transform on its own listener — the shape a launch takes when no sanitizer is running. Callers must have set XDG_CONFIG_HOME already: the transform opens model-catalog.log, and without isolation it appends to the developer's real ~/.config/everyapi.
func startCatalogProxyForTest(t *testing.T, upstream string, models, detailOnly []tools.Model, aliases map[string]string) string {
	t.Helper()
	logger, closeLog := loopbackProxyLogger("model-catalog.log")
	t.Cleanup(closeLog)
	proxyURL, stop, err := startModelCatalogProxy(upstream, modelCatalogTransform(models, detailOnly, aliases, logger), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return proxyURL
}

func TestModelCatalogProxyOnlyAdvertisesLaunchModels(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	proxyURL := startCatalogProxyForTest(t, "https://api.invalid", []tools.Model{{ID: "chat-ok"}}, nil, nil)
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	models, aliases, _ := claudeCatalogModels([]tools.Model{{ID: "glm-4.7", OwnedBy: "zhipu"}})
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
	proxyURL := startCatalogProxyForTest(t, upstream.URL, models, nil, aliases)
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

// TestRewriteModelAliasForwardsWhatItCannotRewrite pins the passthrough half of the scan-only rewrite. Dropping the whole-envelope Unmarshal means a body the scanner cannot make sense of must reach the gateway untouched rather than being rejected locally — and, just as importantly, must still arrive at all: every early return has to put the drained body back on the request.
func TestRewriteModelAliasForwardsWhatItCannotRewrite(t *testing.T) {
	const alias = "claude-everyapi-glm"
	for _, tc := range []struct {
		name string
		body string
	}{
		{"not JSON at all", `this is not JSON`},
		{"no model field", `{"messages":[]}`},
		{"model is not a string", `{"model":null}`},
		{"model is not an alias", `{"model":"claude-opus-4-8"}`},
		{"empty body", ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "http://example.test/v1/messages", strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if err := rewriteModelAlias(req, map[string]string{alias: "glm-4.7"}); err != nil {
				t.Fatalf("a body the proxy cannot rewrite must be forwarded, not rejected: %v", err)
			}
			forwarded, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(forwarded) != tc.body {
				t.Fatalf("forwarded body = %q, want it byte-identical to %q", forwarded, tc.body)
			}
			if req.ContentLength != int64(len(tc.body)) {
				t.Fatalf("ContentLength = %d, want %d", req.ContentLength, len(tc.body))
			}
		})
	}
}

// TestRewriteModelAliasRewritesWithoutValidatingTheWholeBody records the one behaviour change of the scan-only path: a body whose top-level model is locatable is rewritten and forwarded even though its tail is malformed. The whole-envelope Unmarshal used to turn this into a local 400. Rejecting it is the gateway's call, and the gateway can only make it if the alias has already been swapped for the real model id.
func TestRewriteModelAliasRewritesWithoutValidatingTheWholeBody(t *testing.T) {
	const alias = "claude-everyapi-glm"
	truncated := `{"model":"` + alias + `","messages":[{"role":"user"`
	req, err := http.NewRequest(http.MethodPost, "http://example.test/v1/messages", strings.NewReader(truncated))
	if err != nil {
		t.Fatal(err)
	}
	if err := rewriteModelAlias(req, map[string]string{alias: "glm-4.7"}); err != nil {
		t.Fatal(err)
	}
	forwarded, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"model":"glm-4.7","messages":[{"role":"user"`
	if string(forwarded) != want {
		t.Fatalf("forwarded body = %q, want %q", forwarded, want)
	}
}

// TestModelCatalogProxyLogsUpstreamFailures covers the hop's only evidence of a failure. It sits closest to the gateway on both launch paths, so it is the first to see an outage, and it cannot log to stderr without corrupting the launched tool's TUI — leaving the file as the sole record behind the 502.
func TestModelCatalogProxyLogsUpstreamFailures(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)

	// A server that is already closed: every relay attempt fails to connect.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	// Not startCatalogProxyForTest: this test has to close the log itself, before reading the file back. Both are still registered for cleanup — closing early and releasing on abort are independent concerns, and the assertions below can fail (a port reused after dead.Close() answers with something other than a 502), which would otherwise strand the descriptor, the listener and the Serve goroutine for the rest of the run. Both closers are idempotent, so the explicit calls and the cleanup coexist.
	logger, closeLog := loopbackProxyLogger("model-catalog.log")
	t.Cleanup(closeLog)
	proxyURL, stop, err := startModelCatalogProxy(deadURL, modelCatalogTransform([]tools.Model{{ID: "chat-ok"}}, nil, nil, logger), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	const credential = "sk-everyapi-must-not-be-logged"
	resp, err := http.Post(proxyURL+"/v1/messages?api_key="+credential, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	stop()
	closeLog()

	written, err := os.ReadFile(filepath.Join(configRoot, "everyapi", "model-catalog.log"))
	if err != nil {
		t.Fatalf("the 502 left no log behind, so the hop is still a black hole: %v", err)
	}
	if !strings.Contains(string(written), "/v1/messages") {
		t.Fatalf("log does not identify the failing request: %s", written)
	}
	// On the injected path this address is the tool's base URL, and the launch line no longer prints it — the log is the only place it appears.
	if !strings.Contains(string(written), "listening on "+proxyURL) {
		t.Fatalf("log does not record the address the proxy bound: %s", written)
	}
	if strings.Contains(string(written), credential) {
		t.Fatalf("query-string credential leaked into the log: %s", written)
	}
}

func TestTransparentClaudeCatalogAliasReachesGatewayAsRealModel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	models, aliases, _ := claudeCatalogModels([]tools.Model{{ID: "glm-4.7", OwnedBy: "zhipu"}})
	alias := models[0].ID

	var gatewayBody []byte
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gatewayBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer gateway.Close()

	catalogProxy := startCatalogProxyForTest(t, gateway.URL, models, nil, aliases)
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

// TestModelCatalogTransformSkipsEncodedBodies covers the one shape the scan cannot handle. A compressed body has no findable top-level "model", so the alias would travel to the gateway unrewritten and come back as an id that appears in no model list. Decompressing here is not worth owning, so the limit is explicit: forward untouched and say so in the log, rather than burning a full read on a body that cannot yield a match.
func TestModelCatalogTransformSkipsEncodedBodies(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	logger, closeLog := loopbackProxyLogger("model-catalog.log")
	t.Cleanup(closeLog)

	var seen []byte
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, _ = io.ReadAll(r.Body)
	})
	handler := modelCatalogTransform(nil, nil, map[string]string{"claude-everyapi-glm": "glm-4.7"}, logger)(next)

	const body = "\x1f\x8b\x08not-really-gzip-but-opaque-to-the-scanner"
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Encoding", "gzip")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if string(seen) != body {
		t.Fatalf("forwarded body = %q, want it untouched: %q", seen, body)
	}
	closeLog()
	written, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "everyapi", "model-catalog.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "gzip-encoded") {
		t.Fatalf("the skipped rewrite left no explanation for the upstream error it causes:\n%s", written)
	}
}

// TestClaudeCatalogWithholdsFamilyDefaultIDs covers the picker duplicate: the id behind a Claude Code family row must not also be published through discovery, or the same model is offered twice — once labelled "Opus 5", once as the raw id.
//
// The other three cases in the table are the ones that make withholding safe rather than lossy. An older sibling never became a family row, an id parseClaudeModelID rejects cannot have become one, and a non-claude id is on the alias path entirely; all three stay reachable from the picker.
func TestClaudeCatalogWithholdsFamilyDefaultIDs(t *testing.T) {
	published, aliases, _ := claudeCatalogModels([]tools.Model{
		{ID: "claude-opus-5"},
		{ID: "claude-opus-4-5"},
		{ID: "claude-sonnet-5"},
		{ID: "claude-opus-5-thinking"},
		{ID: "glm-4.7"},
	})
	ids := make(map[string]bool, len(published))
	for _, model := range published {
		ids[model.ID] = true
	}
	for _, withheld := range []string{"claude-opus-5", "claude-sonnet-5"} {
		if ids[withheld] {
			t.Errorf("%q is a family row already; publishing it too puts the model in the picker twice", withheld)
		}
	}
	for _, kept := range []string{"claude-opus-4-5", "claude-opus-5-thinking"} {
		if !ids[kept] {
			t.Errorf("%q never became a family row, so withholding it makes the model unreachable from the picker", kept)
		}
	}
	if len(aliases) != 1 {
		t.Fatalf("only the non-claude id should be aliased, got %#v", aliases)
	}
	for alias, upstream := range aliases {
		if upstream != "glm-4.7" || !ids[alias] {
			t.Fatalf("alias %q -> %q is missing from the published catalogue", alias, upstream)
		}
	}
}

// TestClaudeCatalogWithholdingTracksTheCatalogue pins that the withheld set is derived per launch rather than from a hardcoded id list. A catalogue whose newest opus is 4-5 makes THAT the family row, so it is the id that must be withheld and claude-opus-4-1 that must survive — the reverse of the previous test.
func TestClaudeCatalogWithholdingTracksTheCatalogue(t *testing.T) {
	published, _, withheld := claudeCatalogModels([]tools.Model{{ID: "claude-opus-4-1"}, {ID: "claude-opus-4-5"}})
	if len(published) != 1 || published[0].ID != "claude-opus-4-1" {
		t.Fatalf("expected only the non-winning opus to stay published, got %#v", published)
	}
	if len(withheld) != 1 || withheld[0].ID != "claude-opus-4-5" {
		t.Fatalf("the winning opus has to come back as withheld so it stays answerable by id, got %#v", withheld)
	}
}

// TestClaudeCatalogWithholdingIsListOnly separates the two halves of "withheld". Leaving the family default out of GET /v1/models is the point — it is what stops the picker offering the model twice. Answering GET /v1/models/<that id> with a 404 is not, and would be self-contradictory: ANTHROPIC_DEFAULT_OPUS_MODEL and the --model argument both point Claude Code at exactly this id, so the launch would be telling it to run a model its own discovery endpoint denies exists.
func TestClaudeCatalogWithholdingIsListOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	published, aliases, withheld := claudeCatalogModels([]tools.Model{
		{ID: "claude-opus-5", OwnedBy: "anthropic"},
		{ID: "claude-opus-4-5", OwnedBy: "anthropic"},
	})
	proxyURL := startCatalogProxyForTest(t, "https://api.invalid", published, withheld, aliases)

	list, err := http.Get(proxyURL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer list.Body.Close()
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(list.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].ID != "claude-opus-4-5" {
		t.Fatalf("the family default is still listed, so the picker shows it twice: %#v", body.Data)
	}

	detail, err := http.Get(proxyURL + "/v1/models/claude-opus-5")
	if err != nil {
		t.Fatal(err)
	}
	defer detail.Body.Close()
	if detail.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/models/claude-opus-5 = %d, want 200: it is the id ANTHROPIC_DEFAULT_OPUS_MODEL points at", detail.StatusCode)
	}
	var entry struct {
		ID      string `json:"id"`
		OwnedBy string `json:"owned_by"`
	}
	if err := json.NewDecoder(detail.Body).Decode(&entry); err != nil {
		t.Fatal(err)
	}
	if entry.ID != "claude-opus-5" || entry.OwnedBy != "anthropic" {
		t.Fatalf("detail lookup returned %#v, want the withheld model's own entry", entry)
	}
}
