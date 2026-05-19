package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetSellerEligibility(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" || r.URL.Path != "/api/seller/eligibility" {
				t.Errorf("got %s %s, want GET /api/seller/eligibility", r.Method, r.URL.Path)
			}
			if got := r.Header.Get("EveryAPI-User-Id"); got != "7" {
				t.Errorf("EveryAPI-User-Id = %q, want 7", got)
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"success":true,"data":{
				"eligible":true,"marketplace_enabled":true,
				"account_active":true,"email_verified":true,
				"account_age_ok":true,"min_age_days":7,
				"has_consume_log":true,
				"channel_count":2,"channel_cap":10,"under_cap":true}}`)
		}))
		defer srv.Close()

		got, err := New(srv.URL, "acc").WithUserID(7).GetSellerEligibility(context.Background())
		if err != nil {
			t.Fatalf("GetSellerEligibility: %v", err)
		}
		if !got.Eligible || got.ChannelCap != 10 || got.MinAgeDays != 7 {
			t.Errorf("unexpected payload: %+v", got)
		}
	})

	// `success:false` at 200 must surface the message — the dashboard
	// fronts this same endpoint and renders that message inline, the
	// CLI mirrors that behaviour.
	t.Run("success:false bubbles up", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"success":false,"message":"db error","data":{}}`)
		}))
		defer srv.Close()
		_, err := New(srv.URL, "acc").WithUserID(7).GetSellerEligibility(context.Background())
		if err == nil || !strings.Contains(err.Error(), "db error") {
			t.Fatalf("want error mentioning 'db error', got %v", err)
		}
	})
}

func TestCreateSellerChannel(t *testing.T) {
	t.Run("happy path returns id", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" || r.URL.Path != "/api/seller/channel" {
				t.Errorf("got %s %s, want POST /api/seller/channel", r.Method, r.URL.Path)
			}
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			// Body shape: backend reads name/type/keys/models — assert
			// the CLI didn't drop one of those fields, AND that `keys`
			// is the new array form (a regression to the old single
			// `key` field would break the backend's per-key state
			// invariants from #186).
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			for _, k := range []string{"name", "type", "keys", "models"} {
				if _, ok := body[k]; !ok {
					t.Errorf("request body missing %q (got %v)", k, body)
				}
			}
			if _, ok := body["key"]; ok {
				t.Errorf("request body must use keys[], not legacy `key` field — got %v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"success":true,"data":{"id":314,"name":"x"}}`)
		}))
		defer srv.Close()

		id, err := New(srv.URL, "acc").WithUserID(7).CreateSellerChannel(context.Background(), SellerChannelCreate{
			Name:   "x",
			Type:   1,
			Keys:   []string{"sk-test"},
			Models: "gpt-4",
		})
		if err != nil {
			t.Fatalf("CreateSellerChannel: %v", err)
		}
		if id != 314 {
			t.Errorf("id = %d, want 314", id)
		}
	})

	// The eligibility 403 surfaces the gate message — `everyapi seller
	// add-key` re-renders that string verbatim, so any masking here
	// would erase the actionable hint.
	t.Run("403 surfaces server message", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"success":false,"message":"account is not active"}`)
		}))
		defer srv.Close()
		_, err := New(srv.URL, "acc").WithUserID(7).CreateSellerChannel(context.Background(), SellerChannelCreate{
			Name: "x", Type: 1, Keys: []string{"k"}, Models: "m",
		})
		if err == nil || !strings.Contains(err.Error(), "account is not active") {
			t.Fatalf("want error mentioning the server message, got %v", err)
		}
	})

	// Multi-key body: keys[] + key_remarks[] must serialize index-aligned.
	// The backend pairs them by position to carry per-key state, so a
	// silent reorder during JSON marshal would attribute a remark to
	// the wrong credential.
	t.Run("multi-key keys and remarks are index-aligned on the wire", func(t *testing.T) {
		var captured struct {
			Keys       []string `json:"keys"`
			KeyRemarks []string `json:"key_remarks"`
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decode: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"success":true,"data":{"id":1}}`)
		}))
		defer srv.Close()

		_, err := New(srv.URL, "acc").WithUserID(7).CreateSellerChannel(context.Background(), SellerChannelCreate{
			Name: "pool", Type: 1, Models: "m",
			Keys:       []string{"sk-a", "sk-b", "sk-c"},
			KeyRemarks: []string{"primary", "", "fallback"},
		})
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		wantKeys := []string{"sk-a", "sk-b", "sk-c"}
		wantRemarks := []string{"primary", "", "fallback"}
		for i := range wantKeys {
			if captured.Keys[i] != wantKeys[i] {
				t.Errorf("keys[%d] = %q, want %q", i, captured.Keys[i], wantKeys[i])
			}
			if captured.KeyRemarks[i] != wantRemarks[i] {
				t.Errorf("key_remarks[%d] = %q, want %q", i, captured.KeyRemarks[i], wantRemarks[i])
			}
		}
	})
}
