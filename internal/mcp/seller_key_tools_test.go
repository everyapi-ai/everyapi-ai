package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// eligibilityJSON builds a /api/seller/eligibility response body.
// `eligible` drives the headline; the gate booleans are arranged so
// tests can assert specific checklist lines.
func eligibilityJSON(eligible bool) string {
	return `{"success":true,"data":{
		"eligible":` + boolStr(eligible) + `,
		"marketplace_enabled":true,
		"account_active":true,
		"email_verified":` + boolStr(eligible) + `,
		"account_age_ok":true,
		"min_age_days":7,
		"has_consume_log":true,
		"channel_count":1,
		"channel_cap":5,
		"under_cap":true}}`
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// ---- everyapi_seller_eligibility ---------------------------------------

func TestHandleSellerEligibility_Eligible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/seller/eligibility" {
			http.Error(w, "unexpected path "+r.URL.Path, 404)
			return
		}
		w.Write([]byte(eligibilityJSON(true)))
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	out, err := handleSellerEligibility(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	wants := []string{"Eligible", "[x] marketplace open", "[x] email verified", "(1/5)"}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n  got: %s", w, out)
		}
	}
}

func TestHandleSellerEligibility_FailingGate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(eligibilityJSON(false)))
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	out, err := handleSellerEligibility(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Not an error — eligibility is a read, the answer "no" is data.
	wants := []string{"NOT eligible", "[ ] email verified", "/seller/channels"}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n  got: %s", w, out)
		}
	}
}

func TestHandleSellerEligibility_NotLoggedIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, err := handleSellerEligibility(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "everyapi auth login") {
		t.Errorf("want not-logged-in error, got: %v", err)
	}
}

// ---- everyapi_seller_add_key --------------------------------------------

func TestHandleSellerAddKey_ValidationShortCircuits(t *testing.T) {
	// No HTTP server: validation must reject before any network call.
	// "connection refused" instead of the expected message means a
	// half-validated request escaped to the wire.
	withCredentials(t, "http://no-server.invalid", "tok")

	cases := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{"nil args", nil, "missing required argument(s): name, type, keys, models"},
		{"empty object", json.RawMessage(`{}`), "missing required argument(s)"},
		{"missing models", json.RawMessage(`{"name":"n","type":"openai","keys":["sk-1"]}`), "models"},
		{"blank key entry", json.RawMessage(`{"name":"n","type":"openai","keys":["sk-1",""],"models":"gpt-4o"}`), "keys[1] is empty"},
		{"remarks longer than keys", json.RawMessage(`{"name":"n","type":"openai","keys":["sk-1"],"key_remarks":["a","b"],"models":"gpt-4o"}`), "key_remarks has 2 entries but keys has 1"},
		{"duplicate key", json.RawMessage(`{"name":"n","type":"openai","keys":["sk-1","sk-1"],"models":"gpt-4o"}`), "duplicate key"},
		{"unknown type", json.RawMessage(`{"name":"n","type":"frobnicator","keys":["sk-1"],"models":"gpt-4o"}`), `unknown channel type "frobnicator"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handleSellerAddKey(context.Background(), tc.raw)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should contain %q, got: %q", tc.want, err.Error())
			}
		})
	}
}

func TestHandleSellerAddKey_HappyPath(t *testing.T) {
	var created struct {
		Name       string   `json:"name"`
		KindSlug   string   `json:"kind_slug"`
		Keys       []string `json:"keys"`
		KeyRemarks []string `json:"key_remarks"`
		Models     string   `json:"models"`
		Remark     string   `json:"remark"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/seller/eligibility":
			w.Write([]byte(eligibilityJSON(true)))
		case "/api/seller/channel":
			if r.Method != http.MethodPost {
				http.Error(w, "want POST", 405)
				return
			}
			_ = json.NewDecoder(r.Body).Decode(&created)
			w.Write([]byte(`{"success":true,"data":{"id":77}}`))
		default:
			http.Error(w, "unexpected path "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	out, err := handleSellerAddKey(context.Background(), json.RawMessage(
		`{"name":"my-claude","type":"claude","keys":["sk-ant-1","sk-ant-2"],"key_remarks":["primary"],"models":"claude-sonnet-4","remark":"team pool"}`))
	if err != nil {
		t.Fatal(err)
	}
	// The wire request carries the resolved kind_slug and every field.
	if created.KindSlug != "anthropic" {
		t.Errorf("type alias not resolved: sent kind_slug=%q, want \"anthropic\"", created.KindSlug)
	}
	if created.Name != "my-claude" || created.Models != "claude-sonnet-4" || created.Remark != "team pool" {
		t.Errorf("create payload mismatch: %+v", created)
	}
	if len(created.Keys) != 2 || len(created.KeyRemarks) != 1 {
		t.Errorf("keys/remarks not forwarded: %+v", created)
	}
	wants := []string{"#77", "my-claude", "type=claude", "2-key backup pool"}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n  got: %s", w, out)
		}
	}
}

// The backend retired the integer type contract; an unknown or numeric
// type is rejected locally with the choice list, never forwarded.
func TestHandleSellerAddKey_UnknownTypeRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/seller/channel" {
			t.Errorf("unknown type should not reach the create endpoint")
		}
		http.Error(w, "unexpected", 404)
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	_, err := handleSellerAddKey(context.Background(), json.RawMessage(
		`{"name":"n","type":"99","keys":["k"],"models":"m"}`))
	if err == nil {
		t.Fatal("expected an unknown-type error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown channel type") {
		t.Errorf("error should mention unknown channel type, got: %q", err.Error())
	}
}

func TestHandleSellerAddKey_BlockedByEligibility(t *testing.T) {
	channelHit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/seller/eligibility":
			w.Write([]byte(eligibilityJSON(false)))
		case "/api/seller/channel":
			channelHit = true
			w.Write([]byte(`{"success":true,"data":{"id":1}}`))
		default:
			http.Error(w, "unexpected", 404)
		}
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	_, err := handleSellerAddKey(context.Background(), json.RawMessage(
		`{"name":"n","type":"openai","keys":["sk-1"],"models":"gpt-4o"}`))
	if err == nil {
		t.Fatal("expected eligibility error")
	}
	if !strings.Contains(err.Error(), "[ ] email verified") {
		t.Errorf("error should show the failing gate checklist, got: %q", err.Error())
	}
	if channelHit {
		t.Error("create endpoint was hit despite failing eligibility")
	}
}

func TestHandleSellerAddKey_EligibilityQueryErrorIsNonFatal(t *testing.T) {
	// A broken eligibility endpoint must NOT block the mount — the
	// backend re-checks every gate at submit. Only a failed GATE is
	// a stop; a failed QUERY falls through.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/seller/eligibility":
			http.Error(w, "boom", 500)
		case "/api/seller/channel":
			w.Write([]byte(`{"success":true,"data":{"id":8}}`))
		default:
			http.Error(w, "unexpected", 404)
		}
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	out, err := handleSellerAddKey(context.Background(), json.RawMessage(
		`{"name":"n","type":"openai","keys":["sk-1"],"models":"gpt-4o"}`))
	if err != nil {
		t.Fatalf("eligibility query failure should fall through to create: %v", err)
	}
	if !strings.Contains(out, "#8") {
		t.Errorf("expected mount success, got: %q", out)
	}
}

func TestHandleSellerAddKey_BackendErrorSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/seller/eligibility":
			w.Write([]byte(eligibilityJSON(true)))
		case "/api/seller/channel":
			w.Write([]byte(`{"success":false,"message":"channel type not allowed for marketplace"}`))
		default:
			http.Error(w, "unexpected", 404)
		}
	}))
	defer srv.Close()
	withCredentials(t, srv.URL, "tok")

	_, err := handleSellerAddKey(context.Background(), json.RawMessage(
		`{"name":"n","type":"openai","keys":["sk-1"],"models":"gpt-4o"}`))
	if err == nil || !strings.Contains(err.Error(), "not allowed for marketplace") {
		t.Errorf("backend message should surface verbatim, got: %v", err)
	}
}
