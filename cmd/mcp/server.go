package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/version"
)

// JSON-RPC 2.0 wire types. We keep the request/response shapes
// flexible — params and result are json.RawMessage so dispatch can
// hand off the bytes to the tool handler unmarshaled.

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// JSON-RPC 2.0 standard error codes (subset).
const (
	errParseError      = -32700
	errInvalidRequest  = -32600
	errMethodNotFound  = -32601
	errInvalidParams   = -32602
	errInternalError   = -32603
)

// MCP protocol constants. Pinned to the latest stable spec
// (2024-11-05) at the time of writing — bumping this requires
// audit of every message shape.
const mcpProtocolVersion = "2024-11-05"

// ToolHandler is what each tool plugs in. The handler receives the
// raw args blob (so it can decode its own shape) and the credentials
// the server resolved at startup; returns the user-facing text or an
// error. Errors are surfaced as MCP tool-result errors (isError=true)
// rather than JSON-RPC errors — protocol-wise the call succeeded,
// the tool itself failed.
type ToolHandler func(ctx context.Context, args json.RawMessage) (string, error)

// Tool describes one callable surface. InputSchema is a JSON Schema
// object describing the args shape (a {} schema for "no args" is the
// minimum); MCP clients render it as a form / hint to the model.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Handler     ToolHandler
}

// emptyObjectSchema is the JSON Schema for "no arguments". Most of
// our V0 tools take no params; sharing a single constant keeps the
// tool registry concise and the over-the-wire bytes consistent.
var emptyObjectSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)

// run is the package-internal entry point used by both main and the
// black-box tests. It owns the stdio dispatch loop. Reads one JSON
// message per line from `in`, writes one response line per request
// to `out`, sends server logs to `log`. Exits cleanly on EOF.
//
// Concurrency: requests are processed serially. MCP doesn't require
// concurrent dispatch (most clients pipeline anyway), and serial
// keeps response ordering predictable for tests + avoids the need
// to interleave-mutex stdout writes from goroutines.
// Run is the package entry point: takes stdio handles, dispatches
// JSON-RPC messages forever, returns on stdin EOF. Called from the
// `everyapi mcp` subcommand in main.go.
func Run(in io.Reader, out io.Writer, log io.Writer) error {
	tools := registerTools()
	return serve(in, out, log, tools)
}

// serve is the protocol layer factored out from run so tests can
// inject a synthetic tool list without touching credentials.
func serve(in io.Reader, out io.Writer, log io.Writer, tools []Tool) error {
	toolsByName := make(map[string]*Tool, len(tools))
	for i := range tools {
		toolsByName[tools[i].Name] = &tools[i]
	}

	scanner := bufio.NewScanner(in)
	// Default scanner buffer is 64KB. MCP messages can carry tool
	// schemas + large text content; bump to 1MB so a chatty response
	// doesn't truncate mid-line and corrupt the stream.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	// Single mutex around stdout — even though the loop is currently
	// serial, future tool handlers that emit progress notifications
	// will need this. Cheap insurance.
	var outMu sync.Mutex
	writeMsg := func(v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			fmt.Fprintf(log, "everyapi-mcp: marshal response failed: %v\n", err)
			return err
		}
		outMu.Lock()
		defer outMu.Unlock()
		// MCP stdio framing: one JSON object per line, no length
		// prefix. Newline-terminate so clients can readline().
		if _, err := out.Write(append(data, '\n')); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
		return nil
	}

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			// Malformed line. JSON-RPC says reply with parse error
			// at id=null. Skip if we can't even tell it's a request.
			_ = writeMsg(jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error: &jsonRPCError{
					Code:    errParseError,
					Message: "parse error: " + err.Error(),
				},
			})
			continue
		}
		if req.JSONRPC != "2.0" {
			if isNotification(&req) {
				continue
			}
			_ = writeMsg(jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &jsonRPCError{
					Code:    errInvalidRequest,
					Message: "jsonrpc must be \"2.0\"",
				},
			})
			continue
		}

		resp := dispatch(&req, toolsByName, log)
		if isNotification(&req) {
			// Notifications (id absent) MUST NOT get a reply per
			// JSON-RPC 2.0 §4.1. Our dispatch still runs the handler
			// for side effects (none today, but cleaner contract);
			// we just drop the response.
			continue
		}
		_ = writeMsg(resp)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("read stdin: %w", err)
	}
	return nil
}

// isNotification returns true when the request omits `id` (per
// JSON-RPC, that's the marker for "fire and forget"). MCP clients
// send notifications for events like `notifications/initialized`
// after the initialize handshake completes.
func isNotification(req *jsonRPCRequest) bool {
	return len(req.ID) == 0 || string(req.ID) == "null"
}

// dispatch routes one request to the appropriate handler. Always
// returns a response object (caller drops it for notifications).
func dispatch(req *jsonRPCRequest, tools map[string]*Tool, log io.Writer) jsonRPCResponse {
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = handleInitialize()
	case "tools/list":
		resp.Result = handleToolsList(tools)
	case "tools/call":
		result, err := handleToolsCall(req.Params, tools)
		if err != nil {
			// JSON-RPC-level error: the call itself was malformed
			// (unknown tool, bad params shape). Tool-level errors —
			// "not logged in", network failures — come back inside
			// `result` with isError=true.
			resp.Error = err
			return resp
		}
		resp.Result = result
	case "notifications/initialized":
		// MCP-spec notification confirming the client finished
		// initialize. Nothing to do — caller suppresses the response
		// because isNotification is true.
		resp.Result = nil
	default:
		resp.Error = &jsonRPCError{
			Code:    errMethodNotFound,
			Message: "method not found: " + req.Method,
		}
		fmt.Fprintf(log, "everyapi-mcp: unknown method %q\n", req.Method)
	}
	return resp
}

// handleInitialize returns the server's capabilities. We advertise
// only the `tools` capability — no resources, no prompts, no
// sampling.
func handleInitialize() any {
	return map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"serverInfo": map[string]any{
			"name":    "everyapi-mcp",
			"version": version.Version,
		},
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
	}
}

// toolDescriptor is the MCP-side view of a Tool — Name +
// Description + the JSON Schema for arguments. Defined separately
// from Tool so we don't accidentally leak the Handler field over
// the wire.
type toolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func handleToolsList(tools map[string]*Tool) any {
	// Stable order by tool name keeps tests deterministic and gives
	// the MCP client a predictable display ordering.
	names := make([]string, 0, len(tools))
	for n := range tools {
		names = append(names, n)
	}
	// Manual sort to avoid pulling in sort.Strings just for this
	// (it's cheap), keeping the import surface minimal.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	descriptors := make([]toolDescriptor, 0, len(tools))
	for _, n := range names {
		t := tools[n]
		descriptors = append(descriptors, toolDescriptor{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return map[string]any{"tools": descriptors}
}

// toolsCallParams matches the wire shape of tools/call.
type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// MCP tool-call result envelope. content is always an array of
// {type, text} (or other content types we don't use in V0). isError
// signals "the call succeeded protocol-wise but the tool decided
// this is a failure" — distinct from a JSON-RPC error.
type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func handleToolsCall(params json.RawMessage, tools map[string]*Tool) (any, *jsonRPCError) {
	var p toolsCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &jsonRPCError{
			Code:    errInvalidParams,
			Message: "invalid tools/call params: " + err.Error(),
		}
	}
	t, ok := tools[p.Name]
	if !ok {
		return nil, &jsonRPCError{
			Code:    errInvalidParams,
			Message: "unknown tool: " + p.Name,
		}
	}
	// Bound the handler so a misbehaving backend can't hold up the
	// entire stdio pump. 30s is generous for any of our V0 tools
	// (all are single API round-trips against the gateway).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	text, err := t.Handler(ctx, p.Arguments)
	if err != nil {
		return toolCallResult{
			Content: []toolContent{{Type: "text", Text: err.Error()}},
			IsError: true,
		}, nil
	}
	return toolCallResult{
		Content: []toolContent{{Type: "text", Text: text}},
	}, nil
}
