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

	"github.com/everyapi-ai/everyapi-ai/internal/tools"
)

// startModelCatalogProxy keeps dynamic client pickers honest. Requests other
// than model discovery pass through unchanged; /v1/models is answered from the
// protocol-filtered launch snapshot so media/embedding models cannot appear in
// a coding client's /model picker.
func startModelCatalogProxy(upstreamBase string, models []tools.Model, aliases map[string]string) (string, func(), error) {
	target, err := url.Parse(upstreamBase)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return "", nil, fmt.Errorf("parse model catalog proxy upstream %q: %w", upstreamBase, err)
	}
	reverse := httputil.NewSingleHostReverseProxy(target)
	reverse.ErrorLog = log.New(io.Discard, "", 0)
	reverse.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "model catalog upstream unavailable", http.StatusBadGateway)
	}
	direct := reverse.Director
	reverse.Director = func(r *http.Request) {
		direct(r)
		r.Host = target.Host
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && (r.URL.Path == "/v1/models" || strings.HasPrefix(r.URL.Path, "/v1/models/")) {
			serveLaunchModelCatalog(w, r, models)
			return
		}
		if len(aliases) > 0 && strings.HasPrefix(r.URL.Path, "/v1/messages") {
			if err := rewriteModelAlias(r, aliases); err != nil {
				status := http.StatusBadRequest
				if errors.Is(err, errAliasRequestTooLarge) {
					status = http.StatusRequestEntityTooLarge
				}
				http.Error(w, "rewrite model alias: "+err.Error(), status)
				return
			}
		}
		reverse.ServeHTTP(w, r)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("bind model catalog proxy: %w", err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
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

func serveLaunchModelCatalog(w http.ResponseWriter, r *http.Request, models []tools.Model) {
	w.Header().Set("Content-Type", "application/json")
	id := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	if r.URL.Path != "/v1/models" {
		for _, model := range models {
			if model.ID == id {
				_ = json.NewEncoder(w).Encode(modelCatalogEntry(model))
				return
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

func claudeCatalogModels(models []tools.Model) ([]tools.Model, map[string]string) {
	result := make([]tools.Model, 0, len(models))
	aliases := make(map[string]string)
	for _, model := range models {
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
	return result, aliases
}

const maxAliasRewriteBody = 64 << 20

var errAliasRequestTooLarge = errors.New("request body exceeds 64 MiB alias-rewrite limit")

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
	var envelope struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("parse JSON request: %w", err)
	}
	upstream, ok := aliases[envelope.Model]
	if !ok {
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		return nil
	}
	start, end, found := topLevelStringValueRange(body, "model")
	if !found {
		return errors.New("top-level model field was not found")
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
