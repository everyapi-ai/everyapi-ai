package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
)

// modelCatalogTransform keeps dynamic client pickers honest. Requests other than model discovery pass through to next unchanged; /v1/models is answered from the protocol-filtered launch snapshot so media/embedding models cannot appear in a coding client's /model picker.
//
// detailOnly holds models this launch keeps out of the LIST but must still answer for by id — today that is claudeCatalogModels' withheld family defaults. The launch points Claude Code straight at those ids through ANTHROPIC_DEFAULT_<FAMILY>_MODEL and the --model argument, so serving GET /v1/models/<id> a 404 for exactly the id it was told to run on is a contradiction it has no way to resolve. Reachable by id, absent from the list, which is what withholding was meant to mean.
//
// It is a handler decorator rather than a proxy so that the SAME implementation can run on whichever socket the launch already has: hosted on the sanitizer's listener when one is running, or on its own (startModelCatalogProxy) when it is the only transform. Filtering a catalogue and rewriting a model id are content transforms, not transport, and giving each one its own loopback hop made the chain grow a port, a log file and a failure point per transform.
//
// Failures log to ~/.config/everyapi/model-catalog.log via the caller's logger. Like the connector and the sanitizer it MUST NOT log to stderr, which is shared with the launched tool's TUI — and without the file the user's only evidence is an opaque error with no way to tell which transform produced it.
func modelCatalogTransform(models, detailOnly []tools.Model, aliases map[string]string, logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && (r.URL.Path == "/v1/models" || strings.HasPrefix(r.URL.Path, "/v1/models/")) {
				serveLaunchModelCatalog(w, r, models, detailOnly)
				return
			}
			if len(aliases) > 0 && strings.HasPrefix(r.URL.Path, "/v1/messages") {
				// A content-encoded body defeats the scan: the bytes are compressed, so no top-level "model" is findable and the synthetic alias travels to the gateway unrewritten, coming back as "model claude-everyapi-… not found" — an id that appears in no model list. Decompressing here would mean owning gzip/deflate/br round-tripping for a shape no supported client currently sends, so the limit stands and is recorded instead: without this line the upstream error has no local explanation at all.
				if enc := r.Header.Get("Content-Encoding"); enc != "" && !strings.EqualFold(strings.TrimSpace(enc), "identity") {
					logger.Printf("model catalog: %s body is %s-encoded; alias rewrite skipped, a synthetic model id will reach the gateway as-is", r.URL.Path, enc)
					next.ServeHTTP(w, r)
					return
				}
				if err := rewriteModelAlias(r, aliases); err != nil {
					status := http.StatusBadRequest
					if errors.Is(err, errAliasRequestTooLarge) {
						status = http.StatusRequestEntityTooLarge
					}
					logger.Printf("model catalog: alias rewrite for %s failed with %d: %v", r.URL.Path, status, err)
					http.Error(w, "rewrite model alias: "+err.Error(), status)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// startModelCatalogProxy hosts transform on its own loopback listener, relaying whatever it passes through to upstreamBase. This is the launch path where no sanitizer is running, so the catalogue has no other socket to live on.
func startModelCatalogProxy(upstreamBase string, transform func(http.Handler) http.Handler, logger *log.Logger) (string, func(), error) {
	target, err := url.Parse(upstreamBase)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return "", nil, fmt.Errorf("parse model catalog proxy upstream %q: %w", upstreamBase, err)
	}
	reverse := httputil.NewSingleHostReverseProxy(target)
	reverse.ErrorLog = logger
	reverse.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		// Method and path only. r.URL.String() would carry query-string credentials into the log — a real shape here, which is why the connector strips them before relaying (stripClientQueryCredentials).
		logger.Printf("model catalog: relay %s %s to %s failed: %v", r.Method, r.URL.Path, target.Host, err)
		http.Error(w, "model catalog upstream unavailable", http.StatusBadGateway)
	}
	direct := reverse.Director
	reverse.Director = func(r *http.Request) {
		direct(r)
		r.Host = target.Host
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("bind model catalog proxy: %w", err)
	}
	// Record the address, mirroring what the sanitizer logs when it binds. On the injected path this listener's address is what the tool gets as its base URL, and the launch line deliberately no longer prints it — without this line the port the tool is actually talking to appears nowhere at all.
	logger.Printf("model catalog: listening on http://%s → %s", listener.Addr(), upstreamBase)
	// ErrorLog too, not just the reverse proxy's. net/http writes panics recovered per-connection and superfluous-WriteHeader warnings through the Server's own logger, and a nil one falls back to log.Default() — which is stderr, the stream the launched tool's TUI owns. A panic in the composed handler would otherwise dump a goroutine trace into the middle of the tool's rendered UI while this hop's log file stayed empty.
	server := &http.Server{
		Handler:           transform(reverse),
		ErrorLog:          logger,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = server.Serve(listener) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
		})
	}
	return "http://" + listener.Addr().String(), stop, nil
}

func serveLaunchModelCatalog(w http.ResponseWriter, r *http.Request, models, detailOnly []tools.Model) {
	w.Header().Set("Content-Type", "application/json")
	// Matched through Claude Code's 1M context marker, because the id the launch tells the client to boot on carries it (ClaudeBootModelWithContextMarker, on both --model and ANTHROPIC_DEFAULT_OPUS_MODEL) while the catalogue only ever holds the id it was applied to. Without this, the withheld family default 404s on exactly the id the launch pointed the client at — the contradiction detailOnly exists to prevent. No real model id ends in the marker, so stripping it can never shadow a published entry.
	id := tools.ClaudeCatalogueID(strings.TrimPrefix(r.URL.Path, "/v1/models/"))
	if r.URL.Path != "/v1/models" {
		// The published list is searched first, so an id that appears in both answers with the entry the client actually discovered; detailOnly only adds ids the list deliberately omits.
		for _, group := range [][]tools.Model{models, detailOnly} {
			for _, model := range group {
				if model.ID == id {
					_ = json.NewEncoder(w).Encode(modelCatalogEntry(model))
					return
				}
			}
		}
		http.NotFound(w, r)
		return
	}
	data := make([]map[string]any, 0, len(models))
	for _, model := range models {
		data = append(data, modelCatalogEntry(model))
	}
	response := map[string]any{"object": "list", "data": data, "has_more": false}
	if len(models) > 0 {
		response["first_id"] = models[0].ID
		response["last_id"] = models[len(models)-1].ID
	}
	_ = json.NewEncoder(w).Encode(response)
}

func modelCatalogEntry(model tools.Model) map[string]any {
	displayName := model.DisplayName
	if displayName == "" {
		displayName = model.ID
	}
	return map[string]any{
		"id": model.ID, "type": "model", "object": "model",
		"display_name": displayName, "created_at": "1970-01-01T00:00:00Z",
		"owned_by": model.OwnedBy, "supported_endpoint_types": model.SupportedEndpointTypes,
	}
}

// claudeCatalogModels turns a launch catalogue into the one Claude Code will discover: claude-* ids pass through under their own name, everything else is republished under a synthetic claude-everyapi-<slug>-<hash> alias so the client's own model-id validation accepts it, and the returned map lets the transform rewrite that alias back on the way out.
//
// The exception is the handful of ids that are already a family row. Those come back as the third return instead of being published — see ClaudeFamilyAliasedModelIDs for why listing them puts the same model in the picker twice. Dropping them from the list is safe because the family override reaches Claude Code through the environment, not through discovery, so the row survives; what disappears is only its duplicate. They stay answerable by id (modelCatalogTransform's detailOnly), because that override is what points the client at them.
//
// Input order is preserved among the published entries. That is not the same as preserving the head: a remembered model that happens to be its family's winner is withheld, so position 0 moves to the next published id. Claude Code boots on the --model argument managedBootModelArgs prepends rather than on the head of this list, so the shift changes which entry its /model picker opens on, not which model the session starts with.
func claudeCatalogModels(models []tools.Model) ([]tools.Model, map[string]string, []tools.Model) {
	familyAliased := tools.ClaudeFamilyAliasedModelIDs(models)
	result := make([]tools.Model, 0, len(models))
	aliases := make(map[string]string)
	var withheld []tools.Model
	for _, model := range models {
		// Trimmed on lookup, because ClaudeFamilyAliasedModelIDs keys the set on parseClaudeModelID's trimmed id while cliout.Sanitize — which produced these ids — strips control characters without trimming spaces. Matching on the raw id would let " claude-opus-5" become the override AND stay published, which is the duplicate row this whole path exists to remove.
		if familyAliased[strings.TrimSpace(model.ID)] {
			withheld = append(withheld, model)
			continue
		}
		if strings.HasPrefix(strings.ToLower(model.ID), "claude-") {
			result = append(result, model)
			continue
		}
		digest := sha256.Sum256([]byte(model.ID))
		slug := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
				return r
			}
			return '-'
		}, model.ID)
		if len(slug) > 48 {
			slug = slug[:48]
		}
		alias := fmt.Sprintf("claude-everyapi-%s-%x", slug, digest[:4])
		aliased := model
		aliased.ID = alias
		aliased.DisplayName = model.ID
		result = append(result, aliased)
		aliases[alias] = model.ID
	}
	return result, aliases, withheld
}

const maxAliasRewriteBody = 64 << 20

var errAliasRequestTooLarge = errors.New("request body exceeds 64 MiB alias-rewrite limit")

// rewriteModelAlias swaps a synthetic claude-everyapi-* alias back to the real upstream model id. It scans for the top-level "model" string directly instead of unmarshalling the whole envelope: every /v1/messages body passes through here, coding-agent bodies routinely run to megabytes, and the only field that matters is one short string. topLevelStringValueRange already had to locate that exact range to perform the splice, so the full parse was pure overhead.
//
// Consequence: a body this scan cannot make sense of is now forwarded rather than rejected locally with a 400. That is deliberate — validating JSON is the gateway's job, not a loopback hop's, and an alias that survives unrewritten comes back as a clear upstream "model not found" instead of a proxy error the user cannot place. The size limit stays; it guards this process's memory.
func rewriteModelAlias(r *http.Request, aliases map[string]string) error {
	if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxAliasRewriteBody+1))
	_ = r.Body.Close()
	if err != nil {
		return err
	}
	if len(body) > maxAliasRewriteBody {
		return errAliasRequestTooLarge
	}
	// Every early return has to put the consumed body back, or the request reaches the reverse proxy with an empty (already drained) reader.
	restore := func() {
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
	}
	start, end, found := topLevelStringValueRange(body, "model")
	if !found {
		restore()
		return nil
	}
	var requested string
	if err := json.Unmarshal(body[start:end], &requested); err != nil {
		restore()
		return nil
	}
	upstream, ok := aliases[requested]
	if !ok {
		restore()
		return nil
	}
	replacement, _ := json.Marshal(upstream)
	if len(replacement) <= end-start {
		copy(body[start+len(replacement):], body[end:])
		copy(body[start:], replacement)
		body = body[:len(body)-(end-start-len(replacement))]
	} else {
		rewritten := make([]byte, 0, len(body)+len(replacement)-(end-start))
		rewritten = append(rewritten, body[:start]...)
		rewritten = append(rewritten, replacement...)
		rewritten = append(rewritten, body[end:]...)
		body = rewritten
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Length", fmt.Sprint(len(body)))
	return nil
}

func topLevelStringValueRange(body []byte, wantedKey string) (int, int, bool) {
	i := skipJSONSpace(body, 0)
	if i >= len(body) || body[i] != '{' {
		return 0, 0, false
	}
	i++
	for {
		i = skipJSONSpace(body, i)
		if i >= len(body) || body[i] == '}' {
			return 0, 0, false
		}
		keyEnd, ok := jsonStringEnd(body, i)
		if !ok {
			return 0, 0, false
		}
		var key string
		if err := json.Unmarshal(body[i:keyEnd], &key); err != nil {
			return 0, 0, false
		}
		i = skipJSONSpace(body, keyEnd)
		if i >= len(body) || body[i] != ':' {
			return 0, 0, false
		}
		valueStart := skipJSONSpace(body, i+1)
		valueEnd, ok := jsonValueEnd(body, valueStart)
		if !ok {
			return 0, 0, false
		}
		if key == wantedKey && valueStart < len(body) && body[valueStart] == '"' {
			return valueStart, valueEnd, true
		}
		i = skipJSONSpace(body, valueEnd)
		if i >= len(body) || body[i] != ',' {
			return 0, 0, false
		}
		i++
	}
}

func skipJSONSpace(body []byte, i int) int {
	for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '\r') {
		i++
	}
	return i
}

func jsonStringEnd(body []byte, start int) (int, bool) {
	if start >= len(body) || body[start] != '"' {
		return 0, false
	}
	escaped := false
	for i := start + 1; i < len(body); i++ {
		if escaped {
			escaped = false
			continue
		}
		if body[i] == '\\' {
			escaped = true
			continue
		}
		if body[i] == '"' {
			return i + 1, true
		}
	}
	return 0, false
}

func jsonValueEnd(body []byte, start int) (int, bool) {
	if start >= len(body) {
		return 0, false
	}
	if body[start] == '"' {
		return jsonStringEnd(body, start)
	}
	if body[start] != '{' && body[start] != '[' {
		i := start
		for i < len(body) && body[i] != ',' && body[i] != '}' {
			i++
		}
		return skipJSONSpaceBackward(body, i), i > start
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(body); i++ {
		if inString {
			switch {
			case escaped:
				escaped = false
			case body[i] == '\\':
				escaped = true
			case body[i] == '"':
				inString = false
			}
			continue
		}
		switch body[i] {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

func skipJSONSpaceBackward(body []byte, i int) int {
	for i > 0 && (body[i-1] == ' ' || body[i-1] == '\t' || body[i-1] == '\n' || body[i-1] == '\r') {
		i--
	}
	return i
}
