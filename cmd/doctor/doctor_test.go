package doctor

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
)

// captureOut redirects cliout for the duration of fn.
func captureOut(t *testing.T, fn func()) string {
	t.Helper()
	previous := cliout.Out
	var buffer bytes.Buffer
	cliout.Out = &buffer
	defer func() { cliout.Out = previous }()
	fn()
	return buffer.String()
}

func TestRunRejectsAnUnknownToolName(t *testing.T) {
	if err := Run([]string{"definitely-not-a-client"}); err == nil {
		t.Fatal("doctor accepted an unknown tool name")
	}
}

// The single positional is the tool to narrow to; a second one is a typo, not a second tool, and must not silently run the full sweep instead.
func TestRunRejectsTwoPositionalArguments(t *testing.T) {
	if err := Run([]string{"claude", "codex"}); err == nil {
		t.Fatal("doctor accepted two positional arguments")
	}
}

func TestRunRejectsAnUnsupportedFormat(t *testing.T) {
	if err := Run([]string{"--format=xml"}); err == nil {
		t.Fatal("doctor accepted an unsupported format")
	}
}

func TestRunHelpBeforeArgumentValidation(t *testing.T) {
	for _, help := range []string{"help", "--help", "-h"} {
		if err := Run([]string{help}); err != nil {
			t.Fatalf("Run(%q) returned error: %v", help, err)
		}
	}
}

// A half-written JSON document is not parseable, so machine mode must collect every row and emit once — nothing may reach the stream as it goes.
func TestMachineModeEmitsOneDocumentAndPrintsNothingEarly(t *testing.T) {
	report := newReport(true)

	streamed := captureOut(t, func() {
		report.section("Account")
		report.run("credentials cached", func() (string, string, error) {
			return "user_id=4", "", nil
		})
		report.run("sanitizer proxy", func() (string, string, error) {
			return "not running", "start with 'everyapi proxy start'", errSoft("dial refused")
		})
		report.summarize()
	})
	if streamed != "" {
		t.Fatalf("machine mode wrote %q before the document", streamed)
	}

	encoded := captureOut(t, func() {
		if err := report.finish(); err != nil {
			t.Fatalf("finish returned error: %v", err)
		}
	})

	var document MachineReport
	if err := json.Unmarshal([]byte(encoded), &document); err != nil {
		t.Fatalf("machine output is not JSON: %v (%q)", err, encoded)
	}
	if document.Version != machineProtocolVersion {
		t.Fatalf("version = %d, want %d", document.Version, machineProtocolVersion)
	}
	if document.Status != statusWarn {
		t.Fatalf("status = %q, want %q", document.Status, statusWarn)
	}
	if len(document.Checks) != 2 {
		t.Fatalf("got %d checks, want 2", len(document.Checks))
	}
	if got := document.Checks[0]; got.Section != "Account" || got.Status != statusOK || got.Detail != "user_id=4" {
		t.Fatalf("first check = %+v", got)
	}
	if got := document.Checks[1]; got.Status != statusWarn || got.Hint == "" {
		t.Fatalf("second check = %+v, want a warn carrying its hint", got)
	}
}

// A hint only makes sense next to something that went wrong; carrying one on a passing row would have the UI offer a fix for a check that just succeeded.
func TestPassingChecksCarryNoHint(t *testing.T) {
	report := newReport(true)
	report.section("Account")
	report.run("credentials cached", func() (string, string, error) {
		return "user_id=4", "run 'everyapi auth login' first", nil
	})

	if hint := report.machineReport().Checks[0].Hint; hint != "" {
		t.Fatalf("passing check carried hint %q", hint)
	}
}

func TestMachineStatusIsTheWorstCheck(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		run   func(*report)
		want  string
		fails bool
	}{
		{
			name: "all passing",
			run: func(r *report) {
				r.run("a", func() (string, string, error) { return "", "", nil })
			},
			want: statusOK,
		},
		{
			name: "a warning alone",
			run: func(r *report) {
				r.run("a", func() (string, string, error) { return "", "", nil })
				r.run("b", func() (string, string, error) { return "", "", errSoft("advisory") })
			},
			want: statusWarn,
		},
		{
			name: "a failure outranks warnings",
			run: func(r *report) {
				r.run("a", func() (string, string, error) { return "", "", errSoft("advisory") })
				r.run("b", func() (string, string, error) { return "", "", errors.New("hard") })
			},
			want:  statusFail,
			fails: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			report := newReport(true)
			report.section("Account")
			testCase.run(report)

			if got := report.machineReport().Status; got != testCase.want {
				t.Fatalf("status = %q, want %q", got, testCase.want)
			}
			// The exit code has to agree with the document, or a script that branches on one and a human reading the other disagree.
			if gotErr := report.err() != nil; gotErr != testCase.fails {
				t.Fatalf("err() non-nil = %v, want %v", gotErr, testCase.fails)
			}
		})
	}
}

// A failing check reports the error text; the caller's detail is not the story.
func TestFailingChecksReportTheError(t *testing.T) {
	report := newReport(true)
	report.section("Gateway")
	report.run("backend reachable", func() (string, string, error) {
		return "ignored detail", "", errors.New("dial tcp: connection refused")
	})

	check := report.machineReport().Checks[0]
	if check.Detail != "dial tcp: connection refused" {
		t.Fatalf("detail = %q, want the error text", check.Detail)
	}
}

// Human mode keeps streaming as checks complete, so a hanging probe is visible.
func TestHumanModeStreamsAsItGoes(t *testing.T) {
	report := newReport(false)

	out := captureOut(t, func() {
		report.section("Account")
		report.run("credentials cached", func() (string, string, error) {
			return "user_id=4", "", nil
		})
	})
	if !strings.Contains(out, "credentials cached") || !strings.Contains(out, "user_id=4") {
		t.Fatalf("human row missing from %q", out)
	}

	// And it emits no JSON document at the end.
	tail := captureOut(t, func() {
		if err := report.finish(); err != nil {
			t.Fatalf("finish returned error: %v", err)
		}
	})
	if tail != "" {
		t.Fatalf("human mode emitted %q at finish", tail)
	}
}

func TestMachineRequestedRecognisesBothFlagSpellings(t *testing.T) {
	for _, args := range [][]string{
		{"--format=json"},
		{"-format=json"},
		{"--format", "json"},
		{"claude", "--format=json"},
	} {
		if !machineRequested(args) {
			t.Fatalf("machineRequested(%q) = false", args)
		}
	}
	for _, args := range [][]string{
		{},
		{"claude"},
		{"--format=human"},
		{"--format"},
	} {
		if machineRequested(args) {
			t.Fatalf("machineRequested(%q) = true", args)
		}
	}
}

// Under --format=json a usage error must not put flag prose on the stream the caller is parsing.
func TestMachineParseErrorsStayOffTheStream(t *testing.T) {
	out := captureOut(t, func() {
		if err := Run([]string{"--format=json", "--nope"}); err == nil {
			t.Fatal("doctor accepted an unknown flag")
		}
	})
	if out != "" {
		t.Fatalf("parse error wrote %q to the output stream", out)
	}
}

var _ io.Writer = (*bytes.Buffer)(nil)
