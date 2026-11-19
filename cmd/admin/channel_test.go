package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

func TestPlanChannelModels(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		current, add, remove            []string
		wantAdded, wantRemoved, wantRes []string
		wantMissing                     []string
	}{
		{name: "add new", current: []string{"a", "b"}, add: []string{"c"}, wantAdded: []string{"c"}, wantRes: []string{"a", "b", "c"}},
		{name: "add existing is a no-op", current: []string{"a", "b"}, add: []string{"b"}, wantRes: []string{"a", "b"}},
		{name: "remove keeps order", current: []string{"a", "b", "c"}, remove: []string{"b"}, wantRemoved: []string{"b"}, wantRes: []string{"a", "c"}},
		{name: "remove absent is reported", current: []string{"a"}, remove: []string{"z"}, wantMissing: []string{"z"}, wantRes: []string{"a"}},
		{name: "add and remove together", current: []string{"a", "b"}, add: []string{"c"}, remove: []string{"a"}, wantAdded: []string{"c"}, wantRemoved: []string{"a"}, wantRes: []string{"b", "c"}},
		{name: "remove everything leaves nothing", current: []string{"a"}, remove: []string{"a"}, wantRemoved: []string{"a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := planChannelModels(tc.current, tc.add, tc.remove)
			for label, pair := range map[string][2][]string{
				"added":   {plan.added, tc.wantAdded},
				"removed": {plan.removed, tc.wantRemoved},
				"missing": {plan.missing, tc.wantMissing},
				"result":  {plan.result, tc.wantRes},
			} {
				if len(pair[0]) != 0 || len(pair[1]) != 0 {
					if !reflect.DeepEqual(pair[0], pair[1]) {
						t.Errorf("%s = %v, want %v", label, pair[0], pair[1])
					}
				}
			}
		})
	}
}

func TestSplitModelFlagTrimsAndDeduplicates(t *testing.T) {
	if got := splitModelFlag(" a, b ,,a "); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("splitModelFlag = %v", got)
	}
}

func TestAdminChannelModelsRejectsBadArguments(t *testing.T) {
	for name, args := range map[string][]string{
		"no id":          {"--add", "a"},
		"bad id":         {"abc", "--add", "a"},
		"no change":      {"7"},
		"add and remove": {"7", "--add", "a", "--remove", "a"},
		"stray argument": {"7", "--add", "a", "extra"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := adminChannelModels(args); err == nil {
				t.Fatalf("adminChannelModels(%v) succeeded", args)
			}
		})
	}
}

// fakeChannelAdmin serves one channel and records every write, so the tests can assert exactly which endpoint a models edit used and what it sent.
type fakeChannelAdmin struct {
	mu     sync.Mutex
	models string
	writes []string
}

func (f *fakeChannelAdmin) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/channel/7":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"id": 7, "name": "codex-a", "kind_slug": "codex", "status": 1, "models": f.models}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/channel/batch/models", r.Method == http.MethodPut && r.URL.Path == "/api/channel":
			body, _ := io.ReadAll(r.Body)
			f.writes = append(f.writes, r.Method+" "+r.URL.Path+" "+string(body))
			_, _ = io.WriteString(w, `{"success":true,"data":{"id":7}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

func withFakeChannelAdmin(t *testing.T, models string) *fakeChannelAdmin {
	t.Helper()
	fake := &fakeChannelAdmin{models: models}
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.Save(&config.Credentials{APIBase: srv.URL, AccessToken: "sk-everyapi-test"}); err != nil {
		t.Fatal(err)
	}
	return fake
}

func TestAdminChannelModelsAddUsesMergeEndpoint(t *testing.T) {
	fake := withFakeChannelAdmin(t, "gpt-6.2-sol,gpt-6.2-astra")
	if err := adminChannelModels([]string{"7", "--add", "gpt-image-2", "-y"}); err != nil {
		t.Fatal(err)
	}
	want := []string{`POST /api/channel/batch/models {"ids":[7],"models":"gpt-image-2"}`}
	if !reflect.DeepEqual(fake.writes, want) {
		t.Fatalf("writes = %v, want %v", fake.writes, want)
	}
}

func TestAdminChannelModelsRemoveReplacesListWithIDAndModelsOnly(t *testing.T) {
	fake := withFakeChannelAdmin(t, "gpt-6.2-sol,gpt-image-2,gpt-6.2-astra")
	if err := adminChannelModels([]string{"7", "--remove", "gpt-image-2", "--add", "gpt-image-1.5", "-y"}); err != nil {
		t.Fatal(err)
	}
	want := []string{`PUT /api/channel {"id":7,"models":"gpt-6.2-sol,gpt-6.2-astra,gpt-image-1.5"}`}
	if !reflect.DeepEqual(fake.writes, want) {
		t.Fatalf("writes = %v, want %v", fake.writes, want)
	}
}

func TestAdminChannelModelsWritesNothingWhenItShouldNot(t *testing.T) {
	for name, tc := range map[string]struct {
		models  string
		args    []string
		wantErr string
	}{
		"already present":           {models: "gpt-image-2", args: []string{"7", "--add", "gpt-image-2", "-y"}},
		"absent removal":            {models: "gpt-image-2", args: []string{"7", "--remove", "gpt-image-1", "-y"}},
		"removing every model":      {models: "gpt-image-2", args: []string{"7", "--remove", "gpt-image-2", "-y"}, wantErr: "#7"},
		"non-interactive without y": {models: "gpt-6.2-sol", args: []string{"7", "--add", "gpt-image-2"}, wantErr: "-y"},
	} {
		t.Run(name, func(t *testing.T) {
			fake := withFakeChannelAdmin(t, tc.models)
			err := adminChannelModels(tc.args)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
			if len(fake.writes) != 0 {
				t.Fatalf("writes = %v, want none", fake.writes)
			}
		})
	}
}

func TestAdminChannelListAndShowReadTheChannel(t *testing.T) {
	withFakeChannelAdmin(t, "gpt-image-2")
	if err := adminChannelShow([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	if err := adminChannelShow([]string{"0"}); err == nil {
		t.Fatal("show with id 0 must fail")
	}
	if err := adminChannelList([]string{"--status", "paused"}); err == nil {
		t.Fatal("list with an unknown --status must fail")
	}
}
