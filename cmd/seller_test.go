package cmd

import (
	"bufio"
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
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
			// Compare with reflect.DeepEqual because addKeyArgs now
			// holds slices — direct struct compare won't work with
			// nil-vs-empty differences.
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parsed = %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestResolveSellerType covers the alias map + the numeric escape hatch
// + case-insensitivity. The point of the alias map is that a user types
// "claude" rather than "6"; the numeric passthrough exists for future
// types we haven't aliased yet, so we don't have to ship a new CLI for
// an operator to support a new provider.
func TestResolveSellerType(t *testing.T) {
	cases := []struct {
		in      string
		wantID  int
		wantErr bool
	}{
		{"openai", 1, false},
		{"OpenAI", 1, false}, // case-insensitive
		{"claude", 6, false},
		{"anthropic", 6, false}, // both aliases hit the same id
		{"gemini", 13, false},
		{"codex", 42, false},
		{"vertex", 26, false},
		{"vertexai", 26, false},
		{"aws", 18, false},
		{"bedrock", 18, false},
		{"xai", 33, false},
		{"grok", 33, false},
		{"deepseek", 28, false},
		{"  claude  ", 6, false}, // whitespace tolerated
		{"42", 42, false},        // numeric passthrough
		{"99", 99, false},        // unknown numeric: pass through, backend will 422 later
		{"unknown", 0, true},
		{"-1", 0, true}, // negative ids never valid
		{"0", 0, true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			id, err := resolveSellerType(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if id != c.wantID {
				t.Errorf("id = %d, want %d", id, c.wantID)
			}
		})
	}
}

// TestSellerTypeChoicesStable: the picker rendering relies on the
// order being deterministic — the wizard shows numbered options and a
// reshuffle would silently change "press 2" to mean a different
// provider between releases. Keep the explicit ordering covered.
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

// TestChannelTypeLabel: when two aliases share an id (claude/anthropic,
// aws/bedrock, …) the displayed label must be the marketing name we
// prefer, not whichever string sorts first. Without this guard the
// label could flip on a stdlib map iteration order change between Go
// versions.
func TestChannelTypeLabel(t *testing.T) {
	cases := []struct {
		id   int
		want string
	}{
		{1, "openai"},
		{6, "claude"},     // not "anthropic"
		{18, "aws"},       // not "bedrock"
		{26, "vertex"},    // not "vertexai"
		{33, "xai"},       // not "grok"
		{13, "gemini"},
		{28, "deepseek"},
		{42, "codex"},
		{999, "type=999"}, // unknown id falls back to raw integer
	}
	for _, c := range cases {
		got := channelTypeLabel(c.id)
		if got != c.want {
			t.Errorf("channelTypeLabel(%d) = %q, want %q", c.id, got, c.want)
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

// TestCollectSellerKeys exercises the wizard's multi-slot key prompt
// loop with a scripted stdin. The four cases catch:
//
//   - the single-key happy path (the most common; "no more keys")
//   - a multi-key pool (n→y→y→n) with index-aligned remarks
//   - slot-2 OAuth blob rejection: the blob must NOT land in keys,
//     and the loop must re-prompt the same slot
//   - slot-1 OAuth blob: a complete single-key channel, no follow-up
//
// Each scripted line ends with "\n" so bufio.Reader returns it the
// same way a terminal would. Empty lines (\n) accept defaults; we use
// "n\n" / "y\n" for promptYesNo and bare values for promptLine.
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
			// slot 1 plain + remark, more? y,
			// slot 2 OAuth blob (rejected), retry slot 2 plain + remark, more? n
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
			// Redirect prompts' Out so the test isn't drowned in
			// prompt-label echo + the OAuth-blob warning. io.Discard
			// is the natural choice — Out is now typed as io.Writer.
			origOut := Out
			Out = io.Discard
			defer func() { Out = origOut }()

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
