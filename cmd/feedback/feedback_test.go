package feedback

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// captureOut redirects cliout for one test and returns what the command printed.
func captureOut(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	previous := cliout.Out
	cliout.Out = buf
	t.Cleanup(func() { cliout.Out = previous })
	return buf
}

// stubSubmit replaces the network call and records what the command decided to send.
func stubSubmit(t *testing.T, err error) *api.FeedbackSubmit {
	t.Helper()
	var got api.FeedbackSubmit
	previous := submit
	submit = func(_ *api.Client, req api.FeedbackSubmit) error {
		got = req
		return err
	}
	t.Cleanup(func() { submit = previous })
	return &got
}

// feedStdin points the command's stdin at a fixed payload for one test.
func feedStdin(t *testing.T, payload string) {
	t.Helper()
	previous := stdin
	stdin = strings.NewReader(payload)
	t.Cleanup(func() { stdin = previous })
}

// signedIn gives the command a credential to load so it reaches the submit stub instead of stopping at "not logged in".
func signedIn(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.Save(&config.Credentials{AccessToken: "test-token", UserID: 42}); err != nil {
		t.Fatalf("save credentials: %v", err)
	}
}

func TestRunSendsTheTrimmedSubmission(t *testing.T) {
	signedIn(t)
	out := captureOut(t)
	got := stubSubmit(t, nil)

	if err := Run([]string{
		"--kind", "feature",
		"--content", "  streaming stops after 30s  ",
		"--contact", " @tester ",
		"--page-url", "connect://targets",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got.Kind != api.FeedbackKindFeature {
		t.Errorf("kind = %q, want feature", got.Kind)
	}
	if got.Content != "streaming stops after 30s" {
		t.Errorf("content = %q, want it trimmed", got.Content)
	}
	if got.Contact != "@tester" || got.PageURL != "connect://targets" {
		t.Errorf("contact/page = %q/%q", got.Contact, got.PageURL)
	}
	if !strings.Contains(out.String(), "delivered") {
		t.Errorf("human output should confirm delivery, got %q", out.String())
	}
}

func TestClientUserAgentIdentifiesTheFeedbackSource(t *testing.T) {
	if got := clientUserAgent("cli", "1.2.3"); got != "everyapi-cli/1.2.3" {
		t.Errorf("CLI User-Agent = %q", got)
	}
	if got := clientUserAgent("connect", "1.2.3"); got != "everyapi-connect/1.2.3" {
		t.Errorf("Connect User-Agent = %q", got)
	}
	if got := clientUserAgent("untrusted", "1.2.3"); got != "everyapi-cli/1.2.3" {
		t.Errorf("unknown source must fall back to CLI, got %q", got)
	}
}

// Connect parses stdout, so the machine mode has to put the outcome there in both directions — and still exit non-zero on failure so a shell caller notices.
func TestRunJSONModeReportsBothOutcomesOnStdout(t *testing.T) {
	signedIn(t)

	out := captureOut(t)
	stubSubmit(t, nil)
	if err := Run([]string{"--content", "works", "--format", "json"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(out.String()) != `{"ok":true}` {
		t.Errorf("success payload = %q", out.String())
	}

	out = captureOut(t)
	stubSubmit(t, errors.New(`upstream said "no"`))
	err := Run([]string{"--content", "works", "--json"})
	if err == nil {
		t.Fatal("a failed send must still return an error so the exit code is non-zero")
	}
	payload := strings.TrimSpace(out.String())
	if !strings.HasPrefix(payload, `{"ok":false,"code":"`+api.FeedbackCodeDeliveryFailed+`"`) {
		t.Errorf("failure payload = %q", payload)
	}
	// The message carries a server-side quote; it has to survive as valid JSON.
	if !strings.Contains(payload, `upstream said \"no\"`) {
		t.Errorf("failure payload must escape quotes, got %q", payload)
	}
}

// Connect pipes the submission instead of passing it as arguments: a report body can carry a token the user pasted, and process arguments are readable by other local processes.
func TestRunReadsTheSubmissionFromStdin(t *testing.T) {
	signedIn(t)
	captureOut(t)
	got := stubSubmit(t, nil)
	feedStdin(t, `{"kind":"feature","content":"  piped body  ","contact":" @me ","page_url":"connect://targets"}`)

	if err := Run([]string{"--stdin", "--format", "json"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got.Kind != api.FeedbackKindFeature || got.Content != "piped body" {
		t.Errorf("submission = %+v", *got)
	}
	if got.Contact != "@me" || got.PageURL != "connect://targets" {
		t.Errorf("contact/page = %q/%q", got.Contact, got.PageURL)
	}
}

// Nothing sensitive should have to appear in argv, so --stdin must not need --content alongside it.
func TestRunStdinIgnoresTheContentFlag(t *testing.T) {
	signedIn(t)
	captureOut(t)
	got := stubSubmit(t, nil)
	feedStdin(t, `{"kind":"bug","content":"from the pipe"}`)

	if err := Run([]string{"--stdin", "--content", "from argv"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got.Content != "from the pipe" {
		t.Errorf("content = %q, want the piped body to win", got.Content)
	}
}

func TestRunRejectsUnreadableStdin(t *testing.T) {
	signedIn(t)
	out := captureOut(t)
	got := stubSubmit(t, nil)
	feedStdin(t, "not json")

	if err := Run([]string{"--stdin", "--format", "json"}); err == nil {
		t.Fatal("want an error on a malformed payload")
	}
	if got.Content != "" {
		t.Error("must not send anything from an unreadable payload")
	}
	if !strings.Contains(out.String(), `"ok":false`) {
		t.Errorf("payload = %q", out.String())
	}
}

func TestRunRejectsBadInputBeforeSending(t *testing.T) {
	signedIn(t)
	got := stubSubmit(t, nil)

	cases := map[string][]string{
		"unknown kind":  {"--kind", "spam", "--content", "hi"},
		"empty content": {"--content", "   "},
		"long content":  {"--content", strings.Repeat("x", api.FeedbackContentMax+1)},
		"long contact":  {"--content", "hi", "--contact", strings.Repeat("x", api.FeedbackContactMax+1)},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			captureOut(t)
			if err := Run(args); err == nil {
				t.Fatal("want a validation error")
			}
		})
	}
	if got.Content != "" {
		t.Errorf("a rejected submission must never be sent, got %+v", *got)
	}
}

// The limit counts runes, so a CJK report of the same visible length as an accepted ASCII one is accepted too.
func TestRunCountsContentInRunes(t *testing.T) {
	signedIn(t)
	captureOut(t)
	got := stubSubmit(t, nil)

	if err := Run([]string{"--content", strings.Repeat("反", api.FeedbackContentMax)}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Content == "" {
		t.Fatal("submission should have been sent")
	}
}

func TestRunRequiresCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	captureOut(t)
	got := stubSubmit(t, nil)

	if err := Run([]string{"--content", "hi"}); err == nil {
		t.Fatal("want an error when signed out")
	}
	if got.Content != "" {
		t.Error("must not attempt a send without credentials")
	}
}

func TestRunHelpDoesNotSend(t *testing.T) {
	out := captureOut(t)
	got := stubSubmit(t, nil)

	if err := Run([]string{"--help"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "everyapi feedback") {
		t.Errorf("help should print usage, got %q", out.String())
	}
	if got.Content != "" {
		t.Error("help must not send anything")
	}
}

// Connect branches on the code, so a locally-rejected submission has to carry one too — otherwise the desktop cannot tell "we did not send it" from "the server refused".
func TestRunJSONModeCarriesACodeForLocalRejections(t *testing.T) {
	signedIn(t)
	out := captureOut(t)
	stubSubmit(t, nil)

	if err := Run([]string{"--kind", "spam", "--content", "hi", "--json"}); err == nil {
		t.Fatal("want a validation error")
	}
	if !strings.Contains(out.String(), `"code":"`+api.FeedbackCodeInvalidKind+`"`) {
		t.Errorf("payload = %q", out.String())
	}
}

// A server refusal's own code wins over the caller's fallback: "too frequent" must not reach Connect as a generic delivery failure.
func TestRunJSONModePrefersTheServerCode(t *testing.T) {
	signedIn(t)
	out := captureOut(t)
	stubSubmit(t, &api.FeedbackError{Code: api.FeedbackCodeRateLimited, Message: "slow down"})

	if err := Run([]string{"--content", "hi", "--json"}); err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(out.String(), `"code":"`+api.FeedbackCodeRateLimited+`"`) {
		t.Errorf("payload = %q", out.String())
	}
}

// An expired token is the ordinary way a signed-in Connect user fails, and the desktop can only offer "sign in again" when the code says so — a generic delivery_failed sends them round a retry loop that cannot succeed.
func TestRunJSONModeReportsAnExpiredSessionAsNotSignedIn(t *testing.T) {
	signedIn(t)
	out := captureOut(t)
	stubSubmit(t, &api.APIError{StatusCode: http.StatusUnauthorized, Message: "unauthorized"})

	if err := Run([]string{"--content", "hi", "--json"}); err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(out.String(), `"code":"`+codeNotSignedIn+`"`) {
		t.Errorf("payload = %q, want the session-expired code", out.String())
	}
}

// --stdin changes where the fields come from, not what an absent one means, so an omitted category still takes the flag's documented default.
func TestRunStdinDefaultsTheOmittedKind(t *testing.T) {
	signedIn(t)
	captureOut(t)
	got := stubSubmit(t, nil)
	feedStdin(t, `{"content":"no category given"}`)

	if err := Run([]string{"--stdin"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Kind != api.FeedbackKindBug {
		t.Errorf("kind = %q, want the bug default", got.Kind)
	}
}

// The code stops being a fixed set the moment report() adopts the server's own value, and the desktop reads a parsed {"ok":true} as "your report was delivered".
func TestJSONFailureEscapesTheCode(t *testing.T) {
	got := jsonFailure(`x","ok":true,"z":"`, "refused")
	want := `{"ok":false,"code":"x\",\"ok\":true,\"z\":\"","message":"refused"}`
	if got != want {
		t.Errorf("jsonFailure = %s, want %s", got, want)
	}
	var decoded struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("payload must stay valid JSON: %v", err)
	}
	if decoded.OK {
		t.Error("a forged code must not be able to flip ok to true")
	}
}

func TestJSONFailureEscapesControlCharacters(t *testing.T) {
	got := jsonFailure(api.FeedbackCodeDeliveryFailed, "line\nbreak\ttab\\slash")
	want := `{"ok":false,"code":"delivery_failed","message":"line\nbreak\ttab\\slash"}`
	if got != want {
		t.Errorf("jsonFailure = %s, want %s", got, want)
	}
}
