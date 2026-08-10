package modelrouting

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func TestRunSetRejectsTrailingJSON(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	requireCredentials(t, "http://127.0.0.1:1")
	err := Run([]string{"set", "--format=json", `--payload={"mode":"automatic"}{"mode":"single"}`})
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("exactly one JSON value")) {
		t.Fatalf("error = %v", err)
	}
}

func requireCredentials(t *testing.T, base string) {
	t.Helper()
	if err := config.Save(&config.Credentials{APIBase: base, AccessToken: "test-token", UserID: 42}); err != nil {
		t.Fatal(err)
	}
}

func TestRunGetEmitsVersionedSecretFreeJSON(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/model-routing" || r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true,"data":{"mode":"automatic","providers":[{"kind_slug":"openai","name":"OpenAI","model":"gpt-5","latency_ms":210,"success_rate":0.996,"enabled":true,"available":true}]}}`))
	}))
	defer server.Close()
	if err := config.Save(&config.Credentials{APIBase: server.URL, AccessToken: "management-secret", UserID: 42}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	previous := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previous })

	if err := Run([]string{"get", "--format=json"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !bytes.Contains(out.Bytes(), []byte(`"version":1`)) || !bytes.Contains(out.Bytes(), []byte(`"mode":"automatic"`)) {
		t.Fatalf("output = %s", got)
	}
	if bytes.Contains(out.Bytes(), []byte("management-secret")) {
		t.Fatal("output leaked management token")
	}
}
