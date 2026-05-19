package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// fakeHandler returns whatever text is passed at construction time —
// keeps protocol tests independent of the real api/config layers.
func fakeHandler(text string) ToolHandler {
	return func(_ context.Context, _ json.RawMessage) (string, error) {
		return text, nil
	}
}

func fakeFailHandler(msg string) ToolHandler {
	return func(_ context.Context, _ json.RawMessage) (string, error) {
		return "", &fakeErr{msg}
	}
}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

// runWithTools is a test driver: pipe `input` lines through serve(),
// capture stdout, parse each response. Goroutine + done-channel
// because serve() blocks on stdin EOF.
func runWithTools(t *testing.T, input string, tools []Tool) []json.RawMessage {
	t.Helper()
	in := strings.NewReader(input)
	var out bytes.Buffer
	var log bytes.Buffer
	done := make(chan struct{})
	go func() {
		if err := serve(in, &out, &log, tools); err != nil {
			t.Errorf("serve returned error: %v", err)
		}
		close(done)
	}()
	<-done

	// Split on newline, drop empty last line if any.
	rawLines := bytes.Split(bytes.TrimRight(out.Bytes(), "\n"), []byte{'\n'})
	if len(rawLines) == 1 && len(rawLines[0]) == 0 {
		return nil
	}
	resps := make([]json.RawMessage, len(rawLines))
	for i, l := range rawLines {
		resps[i] = json.RawMessage(l)
	}
	return resps
}

// decodeResp pulls the JSON-RPC envelope so individual tests can
// assert on result vs error without re-parsing the same shape.
func decodeResp(t *testing.T, raw json.RawMessage) jsonRPCResponse {
	t.Helper()
	var r struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *jsonRPCError   `json:"error"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("decode response: %v\nraw: %s", err, raw)
	}
	return jsonRPCResponse{
		JSONRPC: r.JSONRPC,
		ID:      r.ID,
		Result:  r.Result,
		Error:   r.Error,
	}
}

// ---- handshake ------------------------------------------------------

func TestServe_InitializeHandshake(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}` + "\n"
	resps := runWithTools(t, input, nil)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	r := decodeResp(t, resps[0])
	if r.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q", r.JSONRPC)
	}
	if r.Error != nil {
		t.Fatalf("unexpected error: %+v", r.Error)
	}
	// Pull out fields we care about; ignore the rest.
	var body struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(r.Result.(json.RawMessage), &body); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if body.ProtocolVersion != mcpProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", body.ProtocolVersion, mcpProtocolVersion)
	}
	if body.ServerInfo.Name != "everyapi-mcp" {
		t.Errorf("serverInfo.name = %q", body.ServerInfo.Name)
	}
	if _, ok := body.Capabilities["tools"]; !ok {
		t.Errorf("capabilities missing tools: %+v", body.Capabilities)
	}
}

// ---- tools/list -----------------------------------------------------

func TestServe_ToolsListIncludesV0Set(t *testing.T) {
	tools := []Tool{
		{Name: "z_one", Description: "Z", InputSchema: emptyObjectSchema, Handler: fakeHandler("z")},
		{Name: "a_two", Description: "A", InputSchema: emptyObjectSchema, Handler: fakeHandler("a")},
	}
	input := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	resps := runWithTools(t, input, tools)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	r := decodeResp(t, resps[0])
	if r.Error != nil {
		t.Fatalf("unexpected error: %+v", r.Error)
	}
	var body struct {
		Tools []toolDescriptor `json:"tools"`
	}
	if err := json.Unmarshal(r.Result.(json.RawMessage), &body); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	if len(body.Tools) != 2 {
		t.Fatalf("want 2 tools, got %d", len(body.Tools))
	}
	// Stable alphabetical ordering — tests downstream UX of MCP
	// clients that just iterate the slice.
	if body.Tools[0].Name != "a_two" || body.Tools[1].Name != "z_one" {
		t.Errorf("tools not sorted: %+v", body.Tools)
	}
}

// TestRegisterTools_HasV1Set pins the V1 tool set so adding /
// removing a tool is a deliberate spec change that breaks this
// test. Doesn't invoke handlers — just checks the registry.
func TestRegisterTools_HasV0Set(t *testing.T) {
	got := registerTools()
	want := map[string]bool{
		"everyapi_status":                           true,
		"everyapi_topup":                            true,
		"everyapi_seller_list":                      true,
		"everyapi_seller_withdraw":                  true,
		"everyapi_seller_add_oauth_codex_start":     true,
		"everyapi_seller_add_oauth_codex_poll":      true,
		"everyapi_seller_add_oauth_claude_start":    true,
		"everyapi_seller_add_oauth_claude_complete": true,
	}
	if len(got) != len(want) {
		t.Fatalf("want %d tools, got %d (%v)", len(want), len(got), toolNames(got))
	}
	for _, tool := range got {
		if !want[tool.Name] {
			t.Errorf("unexpected tool: %q", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("tool %q missing description", tool.Name)
		}
		if len(tool.InputSchema) == 0 {
			t.Errorf("tool %q missing input schema", tool.Name)
		}
		if tool.Handler == nil {
			t.Errorf("tool %q missing handler", tool.Name)
		}
	}
}

func toolNames(ts []Tool) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

// ---- tools/call routing --------------------------------------------

func TestServe_ToolsCall_DispatchesToHandler(t *testing.T) {
	tools := []Tool{
		{Name: "echo", Description: "echo", InputSchema: emptyObjectSchema, Handler: fakeHandler("hello world")},
	}
	input := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{}}}` + "\n"
	resps := runWithTools(t, input, tools)
	r := decodeResp(t, resps[0])
	if r.Error != nil {
		t.Fatalf("unexpected error: %+v", r.Error)
	}
	var body struct {
		Content []toolContent `json:"content"`
		IsError bool          `json:"isError"`
	}
	if err := json.Unmarshal(r.Result.(json.RawMessage), &body); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if body.IsError {
		t.Errorf("isError true on successful tool")
	}
	if len(body.Content) != 1 || body.Content[0].Text != "hello world" {
		t.Errorf("content = %+v", body.Content)
	}
}

func TestServe_ToolsCall_HandlerErrorBecomesIsError(t *testing.T) {
	tools := []Tool{
		{Name: "fail", Description: "fail", InputSchema: emptyObjectSchema, Handler: fakeFailHandler("not logged in")},
	}
	input := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fail","arguments":{}}}` + "\n"
	resps := runWithTools(t, input, tools)
	r := decodeResp(t, resps[0])
	// Handler errors become tool-result errors, NOT JSON-RPC errors.
	// AI client sees isError=true with the text; protocol layer
	// stays happy.
	if r.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", r.Error)
	}
	var body struct {
		Content []toolContent `json:"content"`
		IsError bool          `json:"isError"`
	}
	if err := json.Unmarshal(r.Result.(json.RawMessage), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.IsError {
		t.Errorf("handler error did not surface as isError")
	}
	if body.Content[0].Text != "not logged in" {
		t.Errorf("error text = %q", body.Content[0].Text)
	}
}

func TestServe_ToolsCall_UnknownTool(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"does_not_exist","arguments":{}}}` + "\n"
	resps := runWithTools(t, input, nil)
	r := decodeResp(t, resps[0])
	if r.Error == nil {
		t.Fatal("want JSON-RPC error for unknown tool")
	}
	if r.Error.Code != errInvalidParams {
		t.Errorf("error code = %d, want %d", r.Error.Code, errInvalidParams)
	}
	if !strings.Contains(r.Error.Message, "does_not_exist") {
		t.Errorf("error message lacks tool name: %q", r.Error.Message)
	}
}

// ---- unknown methods -----------------------------------------------

func TestServe_UnknownMethod(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":6,"method":"resources/list"}` + "\n"
	resps := runWithTools(t, input, nil)
	r := decodeResp(t, resps[0])
	if r.Error == nil || r.Error.Code != errMethodNotFound {
		t.Errorf("want method-not-found error, got %+v", r.Error)
	}
}

// ---- notifications --------------------------------------------------

func TestServe_NotificationDropped(t *testing.T) {
	// Notification = no `id`. Per JSON-RPC 2.0 §4.1, server MUST
	// NOT reply. We verify by sending a notification then a real
	// request and confirming only one reply comes back.
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":7,"method":"initialize"}`,
		"",
	}, "\n")
	resps := runWithTools(t, input, nil)
	if len(resps) != 1 {
		t.Fatalf("want exactly 1 reply (notification should be dropped), got %d", len(resps))
	}
	r := decodeResp(t, resps[0])
	if string(r.ID) != "7" {
		t.Errorf("reply id = %s, want 7", r.ID)
	}
}

// ---- multi-message stream ------------------------------------------

func TestServe_MultiMessageStream(t *testing.T) {
	tools := []Tool{
		{Name: "echo", Description: "echo", InputSchema: emptyObjectSchema, Handler: fakeHandler("ok")},
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{}}}`,
		"",
	}, "\n")
	resps := runWithTools(t, input, tools)
	if len(resps) != 3 {
		t.Fatalf("want 3 replies, got %d", len(resps))
	}
	for i, want := range []string{"1", "2", "3"} {
		r := decodeResp(t, resps[i])
		if string(r.ID) != want {
			t.Errorf("response[%d].id = %s, want %s", i, r.ID, want)
		}
	}
}

// ---- malformed input ------------------------------------------------

func TestServe_MalformedJSON_RepliesParseError(t *testing.T) {
	input := "this is not json\n"
	resps := runWithTools(t, input, nil)
	if len(resps) != 1 {
		t.Fatalf("want 1 reply, got %d", len(resps))
	}
	r := decodeResp(t, resps[0])
	if r.Error == nil || r.Error.Code != errParseError {
		t.Errorf("want parse-error reply, got %+v", r.Error)
	}
}

// ---- empty input cleanly exits -------------------------------------

func TestServe_EmptyInputExits(t *testing.T) {
	resps := runWithTools(t, "", nil)
	if resps != nil {
		t.Errorf("want no replies on empty input, got %d", len(resps))
	}
}

// guards against go vet "unused" on io stdlib import in some refactors
var _ io.Reader = (*strings.Reader)(nil)
