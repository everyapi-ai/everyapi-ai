// Package api is the HTTP client for the Relaya backend. Endpoints
// the CLI talks to:
//   - POST /api/cli/device-auth-start  (no auth)
//   - POST /api/cli/device-auth-poll   (no auth, identity = device_code)
//   - GET  /api/user/self              (bearer = access_token)
//
// Anything user-scoped uses the access token from credentials.json.
// Device-auth endpoints are unauthenticated.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Client wraps http.Client with a base URL and the user's access
// token (when present). Construct one per command — the CLI is a
// short-lived process, no connection pooling concerns.
type Client struct {
	base   string
	token  string
	userID int
	hc     *http.Client
}

func New(base, token string) *Client {
	return &Client{
		base:  base,
		token: token,
		hc:    &http.Client{Timeout: 30 * time.Second},
	}
}

// WithUserID associates the caller's numeric user_id with the client
// so authenticated requests can populate the `Relaya-User-Id` header.
// The backend's UserAuth middleware checks BOTH a valid access token
// AND that this header is present + a positive integer (see
// backend/internal/middleware/auth.go ~ line 107). The header was
// originally part of the dashboard's CORS/cache fingerprint and got
// promoted to a hard requirement; missing it returns
// "user ID not provided" with HTTP 401.
//
// Set during credentials load — pass the cached value from
// credentials.json so we don't have to call /api/user/self before
// every other call just to discover our own id.
func (c *Client) WithUserID(id int) *Client {
	c.userID = id
	return c
}

// APIError surfaces a non-2xx server response. Code == 401 is the
// signal for "token expired, run `relaya login` again"; the cmd layer
// special-cases it to render a friendly message instead of the JSON
// blob.
type APIError struct {
	StatusCode int
	Body       string
	// Message is the server's `message` field when the response JSON
	// follows the Relaya { success, message } envelope; falls back to
	// raw body when the envelope is missing (e.g. nginx 502).
	Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("relaya api %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("relaya api %d: %s", e.StatusCode, e.Body)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	// UserAuth requires a positive Relaya-User-Id alongside the
	// bearer (see WithUserID's doc for rationale). Skip the header
	// when userID isn't set so unauthenticated endpoints
	// (/api/cli/device-auth-start, /api/status) don't ship a bogus
	// "0".
	if c.userID > 0 {
		req.Header.Set("Relaya-User-Id", strconv.Itoa(c.userID))
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		ae := &APIError{StatusCode: resp.StatusCode, Body: string(data)}
		// Try to parse the standard Relaya envelope for a friendlier
		// error message; ignore parse failures (non-JSON 5xx is fine).
		var env struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &env) == nil {
			ae.Message = env.Message
		}
		return ae
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// IsUnauthorized reports whether the error is a 401 from the API —
// used by cmd/status to render "token expired, run `relaya login`".
func IsUnauthorized(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.StatusCode == http.StatusUnauthorized
	}
	return false
}
