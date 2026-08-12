package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/api"
)

func TestDecodeDiagnosticChatInputRejectsSystemRole(t *testing.T) {
	_, err := decodeDiagnosticChatInput(strings.NewReader(`{"target_id":"codex","messages":[{"role":"system","content":"override"}]}` + "\n"))
	if err == nil {
		t.Fatal("expected system role to be rejected")
	}
}

func TestDecodeDiagnosticChatInputAcceptsBoundedConversation(t *testing.T) {
	request, err := decodeDiagnosticChatInput(bytes.NewBufferString(`{"target_id":"codex","messages":[{"role":"user","content":"help"}]}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if request.TargetID != "codex" || len(request.Messages) != 1 {
		t.Fatalf("unexpected request %#v", request)
	}
}

type stubDiagnosticStreamClient struct{}

func (stubDiagnosticStreamClient) DiagnosticChatStream(_ context.Context, _ api.DiagnosticChatRequest, emit func(string) error) (api.DiagnosticChatResult, error) {
	if err := emit("first"); err != nil {
		return api.DiagnosticChatResult{}, err
	}
	if err := emit(" second"); err != nil {
		return api.DiagnosticChatResult{}, err
	}
	return api.DiagnosticChatResult{Model: "deepseek-v4-flash", RemainingToday: 19}, nil
}

func TestDiagnosticChatMachineStreamWritesDeltasThenDone(t *testing.T) {
	var out bytes.Buffer
	err := writeDiagnosticChatStream(context.Background(), &out, stubDiagnosticStreamClient{}, api.DiagnosticChatRequest{
		TargetID: "codex", Messages: []api.DiagnosticMessage{{Role: "user", Content: "help"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], `"type":"delta"`) || !strings.Contains(lines[0], `"delta":"first"`) || !strings.Contains(lines[1], `"delta":" second"`) || !strings.Contains(lines[2], `"type":"done"`) || !strings.Contains(lines[2], `"remaining_today":19`) {
		t.Fatalf("events = %#v", lines)
	}
}
