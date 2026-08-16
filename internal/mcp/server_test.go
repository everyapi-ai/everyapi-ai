package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// fakeHandler returns whatever text is passed at construction time — keeps protocol tests independent of the real api/config layers.
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

// runWithTools is a test driver: pipe `input` lines through serve(), capture stdout, parse each response. Goroutine + done-channel because serve() blocks on stdin EOF.
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

// decodeResp pulls the JSON-RPC envelope so individual tests can assert on result vs error without re-parsing the same shape.
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
	// Stable alphabetical ordering — tests downstream UX of MCP clients that just iterate the slice.
	if body.Tools[0].Name != "a_two" || body.Tools[1].Name != "z_one" {
		t.Errorf("tools not sorted: %+v", body.Tools)
	}
}

// TestRegisterTools_HasFullSet pins the registered tool set so adding / removing a tool is a deliberate spec change that breaks this test. Doesn't invoke handlers — just checks the registry.
func TestRegisterTools_HasV0Set(t *testing.T) {
	got := registerTools()
	want := map[string]bool{
		// V0
		"everyapi_status":          true,
		"everyapi_topup":           true,
		"everyapi_seller_list":     true,
		"everyapi_seller_withdraw": true,
		// V3: plain-API-key seller onboarding
		"everyapi_seller_eligibility": true,
		"everyapi_seller_add_key":     true,
		// V1: seller OAuth onboarding
		"everyapi_seller_add_oauth_codex_start":     true,
		"everyapi_seller_add_oauth_codex_poll":      true,
		"everyapi_seller_add_oauth_claude_start":    true,
		"everyapi_seller_add_oauth_claude_complete": true,
		// V2: BYO-GPU edge surface (read + delete)
		"everyapi_edge_list":   true,
		"everyapi_edge_status": true,
		"everyapi_edge_remove": true,
		// V2: operator marketplace toggle
		"everyapi_admin_marketplace_status": true,
		"everyapi_admin_marketplace_set":    true,
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
	// Handler errors become tool-result errors, NOT JSON-RPC errors. AI client sees isError=true with the text; protocol layer stays happy.
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

// TestServe_Ping pins the MCP `ping` utility method: the receiver MUST reply with an empty result, NOT a method-not-found error (a strict host would treat the error as a dead server and tear down the session).
func TestServe_Ping(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":9,"method":"ping"}` + "\n"
	resps := runWithTools(t, input, nil)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	r := decodeResp(t, resps[0])
	if r.Error != nil {
		t.Fatalf("ping must not error, got %+v", r.Error)
	}
	if r.Result == nil {
		t.Fatal("ping result should be a (non-nil) empty object")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(r.Result.(json.RawMessage), &body); err != nil {
		t.Fatalf("ping result should be a JSON object: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("ping result should be empty, got %v", body)
	}
}

// ---- notifications --------------------------------------------------

func TestServe_NotificationDropped(t *testing.T) {
	// Notification = no `id`. Per JSON-RPC 2.0 §4.1, server MUST NOT reply. We verify by sending a notification then a real request and confirming only one reply comes back.
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

// ---- F1: handler panic recovery ------------------------------------

func fakePanicHandler(msg string) ToolHandler {
	return func(_ context.Context, _ json.RawMessage) (string, error) {
		panic(msg)
	}
}

func TestServe_ToolsCall_HandlerPanicBecomesIsError(t *testing.T) {
	// A panicking handler must NOT unwind out of the serial dispatch loop and kill the long-lived server. It should surface as an isError tool result, and the server must keep serving the next request.
	tools := []Tool{
		{Name: "boom", Description: "boom", InputSchema: emptyObjectSchema, Handler: fakePanicHandler("kaboom")},
		{Name: "echo", Description: "echo", InputSchema: emptyObjectSchema, Handler: fakeHandler("ok")},
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"boom","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{}}}`,
		"",
	}, "\n")
	resps := runWithTools(t, input, tools)
	if len(resps) != 2 {
		t.Fatalf("want 2 replies (panic recovered + next request served), got %d", len(resps))
	}

	// First: panic surfaces as a tool-result error, not a JSON-RPC error.
	r0 := decodeResp(t, resps[0])
	if r0.Error != nil {
		t.Fatalf("panic became a JSON-RPC error, want isError result: %+v", r0.Error)
	}
	var body0 struct {
		Content []toolContent `json:"content"`
		IsError bool          `json:"isError"`
	}
	if err := json.Unmarshal(r0.Result.(json.RawMessage), &body0); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body0.IsError {
		t.Errorf("panic did not surface as isError")
	}
	if len(body0.Content) == 0 || !strings.Contains(body0.Content[0].Text, "panicked") {
		t.Errorf("panic text = %+v, want mention of panic", body0.Content)
	}

	// Second: server kept serving.
	r1 := decodeResp(t, resps[1])
	if r1.Error != nil {
		t.Fatalf("second request errored after panic: %+v", r1.Error)
	}
	if string(r1.ID) != "2" {
		t.Errorf("second reply id = %s, want 2 (server stopped serving?)", r1.ID)
	}
}

// ---- F2: oversized line degrades to a per-request error ------------

func TestServe_OversizedLine_DegradesPerRequest(t *testing.T) {
	// A single message larger than the 1MB cap must NOT terminate the session (the old bufio.Scanner path returned bufio.ErrTooLong and the process exited). It should yield one per-request error and the server must keep serving the following message.
	huge := strings.Repeat("x", (1<<20)+1024) // > maxMessageBytes
	input := huge + "\n" +
		`{"jsonrpc":"2.0","id":9,"method":"initialize"}` + "\n"
	resps := runWithTools(t, input, nil)
	if len(resps) != 2 {
		t.Fatalf("want 2 replies (oversize error + initialize), got %d", len(resps))
	}

	r0 := decodeResp(t, resps[0])
	if r0.Error == nil || r0.Error.Code != errInvalidRequest {
		t.Fatalf("want invalid-request error for oversized line, got %+v", r0.Error)
	}

	r1 := decodeResp(t, resps[1])
	if r1.Error != nil {
		t.Fatalf("request after oversized line errored: %+v", r1.Error)
	}
	if string(r1.ID) != "9" {
		t.Errorf("reply id = %s, want 9 (server stopped serving?)", r1.ID)
	}
}

// ---- F3: notification-ness is decided by method, not id ------------

func TestServe_RequestMethodWithoutID_NotExecuted(t *testing.T) {
	// A side-effecting tools/call framed WITHOUT an id must NOT run the handler (the result/error envelope would otherwise be silently dropped). It should be refused with an invalid-request error, and the handler must never fire.
	var called bool
	tools := []Tool{
		{
			Name:        "everyapi_seller_withdraw",
			Description: "withdraw",
			InputSchema: emptyObjectSchema,
			Handler: func(_ context.Context, _ json.RawMessage) (string, error) {
				called = true
				return "withdrew", nil
			},
		},
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"everyapi_seller_withdraw","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":11,"method":"initialize"}`,
		"",
	}, "\n")
	resps := runWithTools(t, input, tools)

	if called {
		t.Fatal("side-effecting tools/call without id was executed")
	}
	if len(resps) != 2 {
		t.Fatalf("want 2 replies (invalid-request + initialize), got %d", len(resps))
	}
	r0 := decodeResp(t, resps[0])
	if r0.Error == nil || r0.Error.Code != errInvalidRequest {
		t.Fatalf("want invalid-request for id-less tools/call, got %+v", r0.Error)
	}
	r1 := decodeResp(t, resps[1])
	if r1.Error != nil || string(r1.ID) != "11" {
		t.Errorf("server did not keep serving: id=%s err=%+v", r1.ID, r1.Error)
	}
}

func TestServe_NotificationMethodWithoutID_RunsAndDrops(t *testing.T) {
	// Genuine notifications/* methods still run for side effects and are dropped (no reply). Verified via a following real request.
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":12,"method":"initialize"}`,
		"",
	}, "\n")
	resps := runWithTools(t, input, nil)
	if len(resps) != 1 {
		t.Fatalf("want exactly 1 reply (notification dropped), got %d", len(resps))
	}
	r := decodeResp(t, resps[0])
	if string(r.ID) != "12" {
		t.Errorf("reply id = %s, want 12", r.ID)
	}
}

// guards against go vet "unused" on io stdlib import in some refactors
var _ io.Reader = (*strings.Reader)(nil)
