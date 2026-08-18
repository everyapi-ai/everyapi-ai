// Package feedback wires `everyapi feedback` — send a bug report / feature request straight to the operator's team chat.
//
// Two callers, one command. A human runs it with flags and reads a sentence back. EveryAPI Connect runs it as a sidecar with --stdin --format=json: the desktop renderer holds no credential (see clients/desktop/README.md), so every authenticated call it makes goes through this binary, and it needs a machine-readable outcome to turn into a toast.
//
// --stdin exists because a report body is user text of unbounded sensitivity — someone pasting a stack trace can easily paste a token with it — and process arguments are not private: /proc/<pid>/cmdline is world-readable on Linux and `ps` shows them elsewhere. `auth diagnostic-chat` carries its user text the same way for the same reason.
//
// Nothing on the server persists the report — the chat is the inbox — so a failure here means it reached nobody. Both output modes say so plainly rather than implying it was queued for retry.
package feedback

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliargs"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// submit is a package var so tests can exercise the command without a server.
var submit = func(client *api.Client, req api.FeedbackSubmit) error {
	return client.SubmitFeedback(cliout.WithCtx(), req)
}

func Run(args []string) error {
	if len(args) > 0 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		cliout.Println(i18n.T("feedback.usage"))
		return nil
	}
	fs := flag.NewFlagSet("feedback", flag.ContinueOnError)
	kind := fs.String("kind", string(api.FeedbackKindBug), "bug | feature | other")
	content := fs.String("content", "", "what to report (required unless --stdin)")
	contact := fs.String("contact", "", "how to reach you, if not your account email")
	pageURL := fs.String("page-url", "", "where you were when you filed it")
	fromStdin := fs.Bool("stdin", false, "read the whole submission as JSON on stdin, keeping the report out of the process arguments")
	asJSON := fs.Bool("json", false, "machine-readable outcome on stdout")
	format := fs.String("format", "", "output format; \"json\" matches --json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}

	machine := *asJSON || strings.EqualFold(*format, "json")
	if *fromStdin {
		decoded, err := decodeSubmission(stdin)
		if err != nil {
			return report(machine, codeBadInput, err)
		}
		// An omitted category keeps the flag's documented default rather than failing as "invalid_kind": --stdin changes where the fields come from, not what an absent one means.
		if strings.TrimSpace(decoded.Kind) == "" {
			decoded.Kind = *kind
		}
		kind, content, contact, pageURL = &decoded.Kind, &decoded.Content, &decoded.Contact, &decoded.PageURL
	}
	req, code, err := buildSubmission(*kind, *content, *contact, *pageURL)
	if err != nil {
		return report(machine, code, err)
	}

	client, err := newClient()
	if err != nil {
		return report(machine, codeNotSignedIn, err)
	}
	if err := submit(client, req); err != nil {
		failure, code := classifyErr(err)
		return report(machine, code, failure)
	}

	if machine {
		cliout.Println(`{"ok":true}`)
		return nil
	}
	cliout.Println(i18n.T("feedback.sent"))
	return nil
}

// buildSubmission validates locally so an obvious mistake costs a round trip and, more importantly, does not spend one of the submitter's few per-window sends. The server re-checks everything; these limits mirror it.
func buildSubmission(kind, content, contact, pageURL string) (api.FeedbackSubmit, string, error) {
	k := api.FeedbackKind(strings.TrimSpace(kind))
	if !k.Valid() {
		return api.FeedbackSubmit{}, api.FeedbackCodeInvalidKind, fmt.Errorf(i18n.T("feedback.bad_kind"), joinKinds())
	}
	body := strings.TrimSpace(content)
	if body == "" {
		return api.FeedbackSubmit{}, api.FeedbackCodeContentEmpty, errors.New(i18n.T("feedback.content_required"))
	}
	if utf8.RuneCountInString(body) > api.FeedbackContentMax {
		return api.FeedbackSubmit{}, api.FeedbackCodeContentTooLong, fmt.Errorf(i18n.T("feedback.content_too_long"), api.FeedbackContentMax)
	}
	who := strings.TrimSpace(contact)
	if utf8.RuneCountInString(who) > api.FeedbackContactMax {
		return api.FeedbackSubmit{}, api.FeedbackCodeContactTooLong, fmt.Errorf(i18n.T("feedback.contact_too_long"), api.FeedbackContactMax)
	}
	return api.FeedbackSubmit{Kind: k, Content: body, Contact: who, PageURL: strings.TrimSpace(pageURL)}, "", nil
}

// stdinSubmission is the --stdin payload. Deliberately the same field names as the HTTP body so the desktop side has one shape to build, not two.
type stdinSubmission struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
	Contact string `json:"contact"`
	PageURL string `json:"page_url"`
}

// stdin is a package var so tests can feed the command a payload without a real pipe.
var stdin io.Reader = os.Stdin

// decodeSubmission reads exactly one JSON object. The reader is bounded: stdin here is a pipe from a GUI that already caps the field, and an unbounded decode would let a wedged writer grow this process without limit.
func decodeSubmission(r io.Reader) (stdinSubmission, error) {
	var payload stdinSubmission
	decoder := json.NewDecoder(io.LimitReader(r, maxStdinBytes))
	if err := decoder.Decode(&payload); err != nil {
		return stdinSubmission{}, errors.New(i18n.T("feedback.bad_stdin"))
	}
	return payload, nil
}

// maxStdinBytes is generous next to the server's 2000-rune content cap (4 bytes per rune at worst, plus the other fields) and small enough that a runaway writer cannot exhaust memory.
const maxStdinBytes = 64 << 10

// Codes with no server-side counterpart: the CLI stops before it can ask. Connect maps not_signed_in to "sign in again" rather than to a delivery problem, and collapses bad_input — which only a caller that built the --stdin payload wrong can trigger — to its generic sentence.
const (
	codeNotSignedIn = "not_signed_in"
	codeBadInput    = "bad_input"
)

func joinKinds() string {
	names := make([]string, 0, len(api.FeedbackKinds))
	for _, k := range api.FeedbackKinds {
		names = append(names, string(k))
	}
	return strings.Join(names, " | ")
}

// report emits the failure in whichever shape the caller asked for. In JSON mode the outcome still goes to stdout — Connect parses stdout and would otherwise have only a redacted stderr blob to show — and the error is still returned so the exit code stays non-zero.
//
// The `code` matters more than the message for Connect: its Rust layer refuses to pass sidecar text into the webview, so the renderer localizes off the code. Locally-detected problems carry a code too, so the desktop never has to distinguish "the server said no" from "we did not bother asking".
func report(machine bool, code string, err error) error {
	if err == nil {
		return nil
	}
	if machine {
		if fromServer := api.FeedbackErrorCode(err); fromServer != "" {
			code = fromServer
		}
		cliout.Println(jsonFailure(code, err.Error()))
	}
	return err
}

// jsonFailure hand-builds the envelope rather than pulling in a marshaller for three fields. BOTH values are escaped: the code looks like it comes from a fixed set, but report() overwrites it with the server's own `code` field verbatim, so a backend answering `code: "x\",\"ok\":true,\"z\":\""` would otherwise let a refused report parse on the desktop side as {"ok":true} — a "delivered" toast for a submission nobody received.
func jsonFailure(code, message string) string {
	var b strings.Builder
	b.WriteString(`{"ok":false,"code":"`)
	writeJSONString(&b, code)
	b.WriteString(`","message":"`)
	writeJSONString(&b, message)
	b.WriteString(`"}`)
	return b.String()
}

// writeJSONString escapes one JSON string body into b, without the surrounding quotes.
func writeJSONString(b *strings.Builder, value string) {
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
}

func newClient() (*api.Client, error) {
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return nil, errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return nil, err
	}
	return api.ForCredentials(creds), nil
}

// classifyErr settles the sentence a human reads and the code Connect branches on together, because they are one decision. An expired token arrives as a 401-equivalent (the backend answers HTTP 200 + code:"unauthorized" and api.do promotes it); rewriting it into a plain error without also settling the code would hide it from api.FeedbackErrorCode in report(), so the outcome would go out as "delivery_failed" and Connect would tell someone whose session is gone to try again in a moment, forever.
func classifyErr(err error) (error, string) {
	if err == nil {
		return nil, ""
	}
	if api.IsUnauthorized(err) {
		return errors.New(i18n.T("auth.session_expired")), codeNotSignedIn
	}
	return err, api.FeedbackCodeDeliveryFailed
}
