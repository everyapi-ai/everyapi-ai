package seller

import (
	"bufio"
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
)

func TestParseAddKeyArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    *addKeyArgs
		wantErr string // substring; empty = expect success
	}{
		{
			"happy path single key",
			[]string{"--type", "claude", "--name", "my-pro", "--key", "sk-xxx", "--models", "claude-3-opus,claude-3-sonnet"},
			&addKeyArgs{Type: "claude", Name: "my-pro", Keys: []string{"sk-xxx"}, Models: "claude-3-opus,claude-3-sonnet"},
			"",
		},
		{
			"channel-level remark passes through",
			[]string{"--type", "openai", "--name", "n", "--key", "k", "--models", "m", "--remark", "personal pro"},
			&addKeyArgs{Type: "openai", Name: "n", Keys: []string{"k"}, Models: "m", Remark: "personal pro"},
			"",
		},
		{
			"multi-key pool — repeated --key accumulates index-aligned",
			[]string{"--type", "openai", "--name", "pool", "--key", "sk-a", "--key", "sk-b", "--models", "m"},
			&addKeyArgs{Type: "openai", Name: "pool", Keys: []string{"sk-a", "sk-b"}, Models: "m"},
			"",
		},
		{
			"per-key remarks align by position",
			[]string{
				"--type", "openai", "--name", "pool", "--models", "m",
				"--key", "sk-a", "--key-remark", "primary",
				"--key", "sk-b", "--key-remark", "backup",
			},
			&addKeyArgs{
				Type: "openai", Name: "pool", Models: "m",
				Keys:    []string{"sk-a", "sk-b"},
				Remarks: []string{"primary", "backup"},
			},
			"",
		},
		{
			"fewer per-key remarks than keys is allowed (later slots get no label)",
			[]string{
				"--type", "openai", "--name", "pool", "--models", "m",
				"--key", "sk-a", "--key", "sk-b",
				"--key-remark", "primary",
			},
			&addKeyArgs{
				Type: "openai", Name: "pool", Models: "m",
				Keys:    []string{"sk-a", "sk-b"},
				Remarks: []string{"primary"},
			},
			"",
		},
		{
			"more per-key remarks than keys is a typo → error",
			[]string{
				"--type", "openai", "--name", "pool", "--models", "m",
				"--key", "sk-a",
				"--key-remark", "primary", "--key-remark", "stray",
			},
			nil, "2 --key-remark but only 1 --key",
		},
		{
			"missing type",
			[]string{"--name", "n", "--key", "k", "--models", "m"},
			nil, "--type",
		},
		{
			"missing models",
			[]string{"--type", "openai", "--name", "n", "--key", "k"},
			nil, "--models",
		},
		{
			"no --key at all → required",
			[]string{"--type", "openai", "--name", "n", "--models", "m"},
			nil, "--key",
		},
		{
			"all four missing — every name surfaces",
			nil,
			nil, "--type, --name, --key, --models",
		},
		{
			"unknown flag → flag package returns error",
			[]string{"--bogus", "x", "--type", "openai", "--name", "n", "--key", "k", "--models", "m"},
			nil, "bogus",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseAddKeyArgs(c.args)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil (parsed=%+v)", c.wantErr, got)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("error = %q, want substring %q", err.Error(), c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Compare with reflect.DeepEqual because addKeyArgs now holds slices — direct struct compare won't work with nil-vs-empty differences.
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parsed = %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestResolveSellerType covers the alias map + canonical-slug acceptance + case-insensitivity. The point of the alias map is that a user types "claude" and the CLI sends the backend's kind_slug ("anthropic"). Unknown inputs are rejected locally (no numeric passthrough — the backend retired the integer type contract).
func TestResolveSellerType(t *testing.T) {
	cases := []struct {
		in       string
		wantSlug string
		wantErr  bool
	}{
		{"openai", "openai", false},
		{"OpenAI", "openai", false}, // case-insensitive
		{"claude", "anthropic", false},
		{"anthropic", "anthropic", false}, // canonical slug accepted directly
		{"gemini", "gemini", false},
		{"codex", "codex", false},
		{"vertex", "vertex_ai", false},
		{"vertexai", "vertex_ai", false},
		{"vertex_ai", "vertex_ai", false},
		{"aws", "aws", false},
		{"bedrock", "aws", false},
		{"xai", "xai", false},
		{"grok", "xai", false},
		{"deepseek", "deepseek", false},
		{"  claude  ", "anthropic", false}, // whitespace tolerated
		{"42", "", true},                   // numeric no longer accepted
		{"unknown", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			slug, err := resolveSellerType(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if slug != c.wantSlug {
				t.Errorf("slug = %q, want %q", slug, c.wantSlug)
			}
		})
	}
}

// TestSellerTypeChoicesStable: the picker rendering relies on the order being deterministic — the wizard shows numbered options and a reshuffle would silently change "press 2" to mean a different provider between releases. Keep the explicit ordering covered.
func TestSellerTypeChoicesStable(t *testing.T) {
	got := sellerTypeChoices()
	want := []string{"openai", "claude", "gemini", "codex", "vertex", "aws", "xai", "deepseek"}
	if len(got) != len(want) {
		t.Fatalf("len = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("choices[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestChannelTypeLabel: when a slug maps to several aliases (anthropic→claude/anthropic, aws→aws/bedrock, …) the displayed label must be the marketing name we prefer, not whichever string sorts first. Without this guard the label could flip on a stdlib map iteration order change between Go versions.
func TestChannelTypeLabel(t *testing.T) {
	cases := []struct {
		slug string
		want string
	}{
		{"openai", "openai"},
		{"anthropic", "claude"}, // not "anthropic"
		{"aws", "aws"},          // not "bedrock"
		{"vertex_ai", "vertex"}, // not "vertexai"
		{"xai", "xai"},          // not "grok"
		{"gemini", "gemini"},
		{"deepseek", "deepseek"},
		{"codex", "codex"},
		{"mystery", "mystery"}, // unknown slug falls back to the raw slug
		{"", ""},
	}
	for _, c := range cases {
		got := channelTypeLabel(c.slug)
		if got != c.want {
			t.Errorf("channelTypeLabel(%q) = %q, want %q", c.slug, got, c.want)
		}
	}
}

func TestChannelStatusLabel(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{1, "enabled"},
		{2, "disabled (manual)"},
		{3, "disabled (auto)"},
		{99, "status=99"},
	}
	for _, c := range cases {
		got := channelStatusLabel(c.in)
		if got != c.want {
			t.Errorf("channelStatusLabel(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCollectSellerKeys exercises the wizard's multi-slot key prompt loop with a scripted stdin. The four cases catch:
//
//   - the single-key happy path (the most common; "no more keys")
//   - a multi-key pool (n→y→y→n) with index-aligned remarks
//   - slot-2 OAuth blob rejection: the blob must NOT land in keys, and the loop must re-prompt the same slot
//   - slot-1 OAuth blob: a complete single-key channel, no follow-up
//
// Each scripted line ends with "\n" so bufio.Reader returns it the same way a terminal would. Empty lines (\n) accept defaults; we use "n\n" / "y\n" for cliprompt.YesNo and bare values for cliprompt.Line.
func TestCollectSellerKeys(t *testing.T) {
	cases := []struct {
		name        string
		script      string
		wantKeys    []string
		wantRemarks []string
		wantErrIs   error
	}{
		{
			name: "single key, no backup",
			// Upstream API key, Remark for #1, Add another? → n
			script:      "sk-only\nprimary\nn\n",
			wantKeys:    []string{"sk-only"},
			wantRemarks: []string{"primary"},
		},
		{
			name: "two-key pool, remarks align",
			// slot 1 key, slot 1 remark, more? y, slot 2 key, slot 2 remark, more? n
			script:      "sk-a\nteam\ny\nsk-b\nbackup\nn\n",
			wantKeys:    []string{"sk-a", "sk-b"},
			wantRemarks: []string{"team", "backup"},
		},
		{
			name: "slot 2 OAuth blob → re-prompted, never lands in keys",
			// slot 1 plain + remark, more? y, slot 2 OAuth blob (rejected), retry slot 2 plain + remark, more? n
			script:      "sk-a\nteam\ny\n{\"type\":\"oauth\"}\nsk-b\nbackup\nn\n",
			wantKeys:    []string{"sk-a", "sk-b"},
			wantRemarks: []string{"team", "backup"},
		},
		{
			name: "slot 1 OAuth blob → single-key channel, no follow-up",
			// slot 1 blob, slot 1 remark — no "more?" prompt at all.
			script:      "{\"type\":\"oauth\",\"token\":\"x\"}\nclaude-pro\n",
			wantKeys:    []string{`{"type":"oauth","token":"x"}`},
			wantRemarks: []string{"claude-pro"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Redirect prompts' Out so the test isn't drowned in prompt-label echo + the OAuth-blob warning. io.Discard is the natural choice — Out is now typed as io.Writer.
			origOut := cliout.Out
			cliout.Out = io.Discard
			defer func() { cliout.Out = origOut }()

			keys, remarks, err := collectSellerKeys(bufio.NewReader(bytes.NewBufferString(c.script)))
			if c.wantErrIs != nil {
				if err != c.wantErrIs {
					t.Fatalf("err = %v, want %v", err, c.wantErrIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if !reflect.DeepEqual(keys, c.wantKeys) {
				t.Errorf("keys = %#v, want %#v", keys, c.wantKeys)
			}
			if !reflect.DeepEqual(remarks, c.wantRemarks) {
				t.Errorf("remarks = %#v, want %#v", remarks, c.wantRemarks)
			}
		})
	}
}

// TestSellerWithdrawRejectsExplicitZero pins that an explicit --quota <= 0 is rejected at the boundary rather than being overloaded as the "withdraw the full pending balance" sentinel (which an omitted flag means). The guard returns before any network call, so no client is needed. Regression for the audit finding where `--quota 0` swept the entire SellerQuota.
func TestSellerWithdrawRejectsExplicitZero(t *testing.T) {
	for _, arg := range []string{"0", "-5"} {
		err := sellerWithdraw([]string{"--quota", arg})
		if err == nil {
			t.Errorf("sellerWithdraw(--quota %s) = nil, want boundary rejection", arg)
			continue
		}
		if !strings.Contains(err.Error(), "positive amount") {
			t.Errorf("sellerWithdraw(--quota %s) error = %q, want a positive-amount rejection", arg, err.Error())
		}
	}
}
