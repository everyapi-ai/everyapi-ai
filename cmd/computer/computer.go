package computer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/computeruse"
)

const (
	maxTextBytes = 16 * 1024
	usageText    = `Usage: everyapi computer <command> [flags]

Commands:
  capabilities                  Show provider and supported operations
  permissions                   Show or request Accessibility and Screen Recording status
  list-apps                     List running desktop applications
  list-windows                  List windows for --app
  get-app-state                 Read the accessibility tree
  screenshot                    Capture a window's own pixels as PNG
  click                         Click an element or window-local point
  set-value                     Set an editable element value
  type-text                     Type into the focused receiver
  paste-text                    Paste text through the native clipboard
  press-key                     Press one key
  hotkey                        Press a modifier chord
  scroll                        Scroll at an element or point
  drag                          Drag between elements or points
  perform-secondary-action      Run a listed accessibility action

Use 'everyapi computer <command> --help' for command flags. Add --json for machine-readable output.
`
	jsonHelpFlags = `  --json                 Print a machine-readable envelope
`
	appHelpFlags = `  --app <selector>       App name, bundle ID, or pid:<number> (required)
`
	targetHelpFlags = `  --app <selector>       App name, bundle ID, or pid:<number> (required)
  --window-index <n>     Select an index from list-windows
  --window-id <id>       Select a window by id from list-windows (instead of --window-index)
  --session <id>         Namespace the element-index cache for a concurrent workflow (letters, digits, '-', '_', '.')
`
	actionOutputHelpFlags = `  --restore-window       Bring the target window forward first; do not fail the action if that is not possible
  --no-screenshot        Skip the window screenshot normally attached to the result
  --json                 Print a machine-readable envelope
`
	subcommandHelpFormat = `Usage: everyapi computer %s [flags]

Flags:
%s`
)

type commandService interface {
	Capabilities(context.Context) (computeruse.Capabilities, error)
	Permissions(context.Context) (computeruse.PermissionStatus, error)
	RequestPermission(context.Context, string) error
	ListApps(context.Context) ([]computeruse.App, error)
	ListWindows(context.Context, string) ([]computeruse.Window, error)
	GetAppState(context.Context, computeruse.StateRequest) (computeruse.State, error)
	Perform(context.Context, computeruse.ActionRequest) (computeruse.State, error)
	Screenshot(context.Context, computeruse.StateRequest) ([]byte, error)
}

type envelope struct {
	OK     bool               `json:"ok"`
	Result any                `json:"result,omitempty"`
	Error  *computeruse.Error `json:"error,omitempty"`
}

type optionalInt struct {
	set   bool
	value int
}

func (v *optionalInt) String() string {
	if !v.set {
		return ""
	}
	return fmt.Sprint(v.value)
}

func (v *optionalInt) Set(raw string) error {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return err
	}
	v.value = value
	v.set = true
	return nil
}

func (v *optionalInt) pointer() *int {
	if !v.set {
		return nil
	}
	value := v.value
	return &value
}

func Run(args []string) error {
	service, err := computeruse.NewDefaultService()
	if err != nil {
		return err
	}
	ctx, stop := cliout.SignalCtx()
	defer stop()
	return run(ctx, args, service, os.Stdin, os.Stdout)
}

func run(ctx context.Context, args []string, service commandService, in io.Reader, out io.Writer) error {
	if len(args) == 0 || isHelp(args[0]) {
		_, err := fmt.Fprint(out, usageText)
		return err
	}
	if helpRequested(args[1:]) {
		return writeSubcommandHelp(out, args[0])
	}
	result, jsonOutput, err := dispatch(ctx, args[0], args[1:], service, in)
	if jsonOutput {
		response := envelope{OK: err == nil, Result: result}
		if err != nil {
			response.Result = nil
			response.Error = asCodedError(err)
		}
		if encodeErr := json.NewEncoder(out).Encode(response); encodeErr != nil {
			return encodeErr
		}
	} else if err == nil {
		if renderErr := renderPlain(out, args[0], result); renderErr != nil {
			return renderErr
		}
	}
	return err
}

func writeSubcommandHelp(out io.Writer, command string) error {
	var flags string
	switch command {
	case "capabilities", "list-apps":
		flags = jsonHelpFlags
	case "permissions":
		flags = `  --request <permission> Request accessibility or screen-recording through macOS
` + jsonHelpFlags
	case "list-windows":
		flags = appHelpFlags + jsonHelpFlags
	case "get-app-state":
		flags = targetHelpFlags + `  --no-screenshot        Skip the window screenshot normally attached to the result
` + jsonHelpFlags
	case "screenshot":
		flags = targetHelpFlags + `  --out <path>           Write the PNG to this file
` + jsonHelpFlags
	case "click", "set-value", "type-text", "paste-text", "press-key", "hotkey", "scroll", "drag", "perform-secondary-action":
		flags = targetHelpFlags
		switch command {
		case "click":
			flags += `  --element-index <n>    Element from the latest state
  --x <n> --y <n>       Window-local point instead of an element
  --mouse-button <btn>   left (default), right, or middle
  --click-count <n>      Clicks to synthesize, e.g. 2 for a double-click
  --modifiers <chord>    Modifier keys held only for this click, e.g. cmd or cmd+shift
`
		case "set-value":
			flags += `  --element-index <n>    Editable element from the latest state
  --value <text>         Value to set
  --value-stdin          Read the value from stdin
`
		case "type-text":
			flags += `  --text <text>          Text for the focused receiver
  --text-stdin           Read text from stdin
`
		case "paste-text":
			flags += `  --text <text>          Text to paste at the focused receiver
  --text-stdin           Read text from stdin
`
		case "press-key", "hotkey":
			flags += `  --key <key>            Key name or modifier chord
`
		case "scroll":
			flags += `  --element-index <n>    Element from the latest state
  --x <n> --y <n>       Window-local point instead of an element
  --direction <dir>      Scroll up, down, left, or right
  --amount <pixels>      Scroll distance (default 600)
`
		case "drag":
			flags += `  --from-element-index <n> --to-element-index <n>
                         Drag between elements
  --from-x <n> --from-y <n> --to-x <n> --to-y <n>
                         Drag between window-local points
`
		case "perform-secondary-action":
			flags += `  --element-index <n>    Element from the latest state
  --action <AXAction>    Advertised accessibility action
`
		}
		flags += actionOutputHelpFlags
	default:
		return invalid(fmt.Sprintf("unknown computer command %q", command))
	}
	_, err := fmt.Fprintf(out, subcommandHelpFormat, command, flags)
	return err
}

func dispatch(ctx context.Context, command string, args []string, service commandService, in io.Reader) (any, bool, error) {
	switch command {
	case "capabilities":
		jsonOutput, err := parseJSONOnly(command, args)
		if err != nil {
			return nil, jsonOutput, err
		}
		result, err := service.Capabilities(ctx)
		return result, jsonOutput, err
	case "permissions":
		fs := newFlagSet(command)
		request := fs.String("request", "", "request accessibility or screen-recording through macOS")
		jsonOutput := fs.Bool("json", false, "print JSON")
		if err := parseFlags(fs, args); err != nil {
			return nil, jsonRequested(args), err
		}
		if *request != "" {
			if err := service.RequestPermission(ctx, *request); err != nil {
				return nil, *jsonOutput, err
			}
		}
		result, err := service.Permissions(ctx)
		return result, *jsonOutput, err
	case "list-apps":
		jsonOutput, err := parseJSONOnly(command, args)
		if err != nil {
			return nil, jsonOutput, err
		}
		result, err := service.ListApps(ctx)
		return result, jsonOutput, err
	case "list-windows":
		fs := newFlagSet(command)
		app := fs.String("app", "", "application name, bundle ID, or pid:<number>")
		jsonOutput := fs.Bool("json", false, "print JSON")
		if err := parseFlags(fs, args); err != nil {
			return nil, jsonRequested(args), err
		}
		if strings.TrimSpace(*app) == "" {
			return nil, *jsonOutput, invalid("list-windows requires --app")
		}
		result, err := service.ListWindows(ctx, *app)
		return result, *jsonOutput, err
	case "get-app-state":
		fs := newFlagSet(command)
		app, windowIndex, windowID, session := addTargetFlags(fs)
		noScreenshot := fs.Bool("no-screenshot", false, "skip the window screenshot normally attached to the result")
		jsonOutput := fs.Bool("json", false, "print JSON")
		if err := parseFlags(fs, args); err != nil {
			return nil, jsonRequested(args), err
		}
		if err := validateTargetFlags(*app, windowIndex, windowID); err != nil {
			return nil, *jsonOutput, err
		}
		result, err := service.GetAppState(ctx, computeruse.StateRequest{App: *app, WindowIndex: windowIndex.pointer(), WindowID: windowID.pointer(), SessionID: *session, NoScreenshot: *noScreenshot})
		return result, *jsonOutput, err
	case "screenshot":
		return dispatchScreenshot(ctx, args, service)
	case "click", "set-value", "type-text", "paste-text", "press-key", "hotkey", "scroll", "drag", "perform-secondary-action":
		return dispatchAction(ctx, command, args, service, in)
	default:
		return nil, jsonRequested(args), invalid(fmt.Sprintf("unknown computer command %q", command))
	}
}

// screenshotResult carries either a file path (when --out was given) or the
// raw PNG bytes base64-encoded for --json output — never both, and never
// raw bytes in plain-text mode, since writing arbitrary binary image data to
// a terminal is not a usable result for a human running the command bare.
type screenshotResult struct {
	Bytes int    `json:"bytes"`
	Path  string `json:"path,omitempty"`
	PNG   string `json:"png,omitempty"`
}

func dispatchScreenshot(ctx context.Context, args []string, service commandService) (any, bool, error) {
	fs := newFlagSet("screenshot")
	app, windowIndex, windowID, session := addTargetFlags(fs)
	jsonOutput := fs.Bool("json", false, "print JSON")
	outPath := fs.String("out", "", "write the PNG to this file path")
	if err := parseFlags(fs, args); err != nil {
		return nil, jsonRequested(args), err
	}
	if err := validateTargetFlags(*app, windowIndex, windowID); err != nil {
		return nil, *jsonOutput, err
	}
	if strings.TrimSpace(*outPath) == "" && !*jsonOutput {
		return nil, *jsonOutput, invalid("screenshot requires --out <path> unless --json is used")
	}
	png, err := service.Screenshot(ctx, computeruse.StateRequest{App: *app, WindowIndex: windowIndex.pointer(), WindowID: windowID.pointer(), SessionID: *session})
	if err != nil {
		return nil, *jsonOutput, err
	}
	result := screenshotResult{Bytes: len(png)}
	if strings.TrimSpace(*outPath) != "" {
		if writeErr := os.WriteFile(*outPath, png, 0o600); writeErr != nil {
			return nil, *jsonOutput, invalid("write screenshot file: " + writeErr.Error())
		}
		result.Path = *outPath
	} else {
		result.PNG = base64.StdEncoding.EncodeToString(png)
	}
	return result, *jsonOutput, nil
}

func dispatchAction(ctx context.Context, command string, args []string, service commandService, in io.Reader) (any, bool, error) {
	fs := newFlagSet(command)
	app, windowIndex, windowID, session := addTargetFlags(fs)
	jsonOutput := fs.Bool("json", false, "print JSON")
	var elementIndex, fromElementIndex, toElementIndex, clickCount optionalInt
	var x, y, fromX, fromY, toX, toY optionalInt
	fs.Var(&elementIndex, "element-index", "element index from the latest state")
	fs.Var(&fromElementIndex, "from-element-index", "drag source element index")
	fs.Var(&toElementIndex, "to-element-index", "drag destination element index")
	fs.Var(&x, "x", "window-local x coordinate")
	fs.Var(&y, "y", "window-local y coordinate")
	fs.Var(&fromX, "from-x", "drag source window-local x coordinate")
	fs.Var(&fromY, "from-y", "drag source window-local y coordinate")
	fs.Var(&toX, "to-x", "drag destination window-local x coordinate")
	fs.Var(&toY, "to-y", "drag destination window-local y coordinate")
	value := fs.String("value", "", "value for set-value")
	valueStdin := fs.Bool("value-stdin", false, "read set-value input from stdin")
	text := fs.String("text", "", "text to type")
	textStdin := fs.Bool("text-stdin", false, "read type-text input from stdin")
	key := fs.String("key", "", "key name or modifier chord")
	direction := fs.String("direction", "", "scroll direction: up, down, left, right")
	amount := fs.Int("amount", 600, "scroll distance in pixels")
	secondaryAction := fs.String("action", "", "accessibility action name")
	mouseButton := fs.String("mouse-button", "", "mouse button: left, right, or middle")
	fs.Var(&clickCount, "click-count", "number of clicks, e.g. 2 for a double-click")
	modifiers := fs.String("modifiers", "", "modifier chord held for the click, e.g. cmd or cmd+shift")
	restoreWindow := fs.Bool("restore-window", false, "bring the target window forward first; do not fail the action if that is not possible")
	noScreenshot := fs.Bool("no-screenshot", false, "skip the window screenshot normally attached to the result")
	if err := parseFlags(fs, args); err != nil {
		return nil, jsonRequested(args), err
	}
	if err := rejectIrrelevantActionFlags(fs, command); err != nil {
		return nil, *jsonOutput, err
	}
	if err := validateTargetFlags(*app, windowIndex, windowID); err != nil {
		return nil, *jsonOutput, err
	}
	request := computeruse.ActionRequest{App: *app, WindowIndex: windowIndex.pointer(), WindowID: windowID.pointer(), ElementIndex: elementIndex.pointer(), FromElementIndex: fromElementIndex.pointer(), ToElementIndex: toElementIndex.pointer(), X: x.pointer(), Y: y.pointer(), FromX: fromX.pointer(), FromY: fromY.pointer(), ToX: toX.pointer(), ToY: toY.pointer(), Key: *key, Direction: *direction, Amount: *amount, SecondaryAction: *secondaryAction, MouseButton: *mouseButton, ClickCount: clickCount.pointer(), Modifiers: *modifiers, RestoreWindow: *restoreWindow, SessionID: *session, NoScreenshot: *noScreenshot}
	switch command {
	case "click":
		request.Kind = computeruse.ActionClick
		if (request.ElementIndex == nil) == (request.X == nil || request.Y == nil) {
			return nil, *jsonOutput, invalid("click requires exactly one of --element-index or --x with --y")
		}
	case "set-value":
		request.Kind = computeruse.ActionSetValue
		payload, err := resolveTextInput(*value, flagWasSet(fs, "value"), *valueStdin, true, "value", in)
		if err != nil {
			return nil, *jsonOutput, err
		}
		request.Text = payload
	case "type-text":
		request.Kind = computeruse.ActionTypeText
		payload, err := resolveTextInput(*text, flagWasSet(fs, "text"), *textStdin, false, "text", in)
		if err != nil {
			return nil, *jsonOutput, err
		}
		request.Text = payload
	case "paste-text":
		request.Kind = computeruse.ActionPasteText
		payload, err := resolveTextInput(*text, flagWasSet(fs, "text"), *textStdin, false, "text", in)
		if err != nil {
			return nil, *jsonOutput, err
		}
		request.Text = payload
	case "press-key":
		request.Kind = computeruse.ActionPressKey
	case "hotkey":
		request.Kind = computeruse.ActionHotkey
	case "scroll":
		request.Kind = computeruse.ActionScroll
	case "drag":
		request.Kind = computeruse.ActionDrag
	case "perform-secondary-action":
		request.Kind = computeruse.ActionSecondary
	}
	if err := validateActionFlags(request); err != nil {
		return nil, *jsonOutput, err
	}
	result, err := service.Perform(ctx, request)
	return result, *jsonOutput, err
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("computer "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func parseJSONOnly(name string, args []string) (bool, error) {
	fs := newFlagSet(name)
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := parseFlags(fs, args); err != nil {
		return jsonRequested(args), err
	}
	return *jsonOutput, nil
}

func rejectIrrelevantActionFlags(fs *flag.FlagSet, command string) error {
	allowed := map[string]bool{"app": true, "window-index": true, "window-id": true, "json": true, "restore-window": true, "no-screenshot": true, "session": true}
	var commandFlags []string
	switch command {
	case "click":
		commandFlags = []string{"element-index", "x", "y", "mouse-button", "click-count", "modifiers"}
	case "set-value":
		commandFlags = []string{"element-index", "value", "value-stdin"}
	case "type-text":
		commandFlags = []string{"text", "text-stdin"}
	case "paste-text":
		commandFlags = []string{"text", "text-stdin"}
	case "press-key", "hotkey":
		commandFlags = []string{"key"}
	case "scroll":
		commandFlags = []string{"element-index", "x", "y", "direction", "amount"}
	case "drag":
		commandFlags = []string{"from-element-index", "to-element-index", "from-x", "from-y", "to-x", "to-y"}
	case "perform-secondary-action":
		commandFlags = []string{"element-index", "action"}
	}
	for _, name := range commandFlags {
		allowed[name] = true
	}
	var unexpected string
	fs.Visit(func(value *flag.Flag) {
		if unexpected == "" && !allowed[value.Name] {
			unexpected = value.Name
		}
	})
	if unexpected != "" {
		return invalid(fmt.Sprintf("%s does not accept --%s", command, unexpected))
	}
	return nil
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return invalid(err.Error())
	}
	if fs.NArg() != 0 {
		return invalid("unexpected arguments: " + strings.Join(fs.Args(), " "))
	}
	return nil
}

func addTargetFlags(fs *flag.FlagSet) (*string, *optionalInt, *optionalInt, *string) {
	app := fs.String("app", "", "application name, bundle ID, or pid:<number>")
	windowIndex := &optionalInt{}
	fs.Var(windowIndex, "window-index", "window index from list-windows")
	windowID := &optionalInt{}
	fs.Var(windowID, "window-id", "window id from list-windows")
	session := fs.String("session", "", "namespace the element-index cache for a concurrent workflow")
	return app, windowIndex, windowID, session
}

func validateTargetFlags(app string, windowIndex, windowID *optionalInt) error {
	if strings.TrimSpace(app) == "" {
		return invalid("--app is required")
	}
	if windowIndex.set && windowIndex.value < 0 {
		return invalid("--window-index must be zero or greater")
	}
	if windowID.set && windowIndex.set {
		return invalid("--window-id and --window-index are mutually exclusive")
	}
	return nil
}

func validateActionFlags(request computeruse.ActionRequest) error {
	switch request.Kind {
	case computeruse.ActionClick:
		if request.MouseButton != "" && request.MouseButton != "left" && request.MouseButton != "right" && request.MouseButton != "middle" {
			return invalid("--mouse-button must be left, right, or middle")
		}
		if request.ClickCount != nil && *request.ClickCount <= 0 {
			return invalid("--click-count must be positive")
		}
	case computeruse.ActionSetValue:
		if request.ElementIndex == nil {
			return invalid("set-value requires --element-index")
		}
	case computeruse.ActionPressKey, computeruse.ActionHotkey:
		if strings.TrimSpace(request.Key) == "" {
			return invalid(string(request.Kind) + " requires --key")
		}
	case computeruse.ActionScroll:
		if request.Direction != "up" && request.Direction != "down" && request.Direction != "left" && request.Direction != "right" {
			return invalid("scroll requires --direction up, down, left, or right")
		}
		if (request.ElementIndex == nil) == (request.X == nil || request.Y == nil) {
			return invalid("scroll requires exactly one of --element-index or --x with --y")
		}
	case computeruse.ActionDrag:
		elements := request.FromElementIndex != nil && request.ToElementIndex != nil
		coordinates := request.FromX != nil && request.FromY != nil && request.ToX != nil && request.ToY != nil
		if elements == coordinates {
			return invalid("drag requires either --from-element-index with --to-element-index or all four coordinate flags")
		}
	case computeruse.ActionSecondary:
		if request.ElementIndex == nil || strings.TrimSpace(request.SecondaryAction) == "" {
			return invalid("perform-secondary-action requires --element-index and --action")
		}
	}
	return nil
}

func resolveTextInput(inline string, inlineProvided, fromStdin, allowEmpty bool, label string, in io.Reader) (string, error) {
	if fromStdin && inlineProvided {
		return "", invalid("--" + label + " and --" + label + "-stdin are mutually exclusive")
	}
	if !fromStdin {
		if !inlineProvided {
			return "", invalid("--" + label + " or --" + label + "-stdin is required")
		}
		if inline == "" && !allowEmpty {
			return "", invalid("--" + label + " must not be empty")
		}
		return inline, nil
	}
	data, err := io.ReadAll(io.LimitReader(in, maxTextBytes+1))
	if err != nil {
		return "", invalid("read stdin: " + err.Error())
	}
	if len(data) > maxTextBytes {
		return "", invalid(fmt.Sprintf("stdin exceeds %d bytes", maxTextBytes))
	}
	if len(data) == 0 && !allowEmpty {
		return "", invalid("stdin is empty")
	}
	return string(data), nil
}

func flagWasSet(fs *flag.FlagSet, wanted string) bool {
	set := false
	fs.Visit(func(value *flag.Flag) {
		if value.Name == wanted {
			set = true
		}
	})
	return set
}

func renderPlain(out io.Writer, command string, result any) error {
	switch value := result.(type) {
	case screenshotResult:
		_, err := fmt.Fprintf(out, "wrote %d bytes to %s\n", value.Bytes, value.Path)
		return err
	case computeruse.Capabilities, computeruse.PermissionStatus:
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(data))
		return err
	case []computeruse.App:
		for _, app := range value {
			if _, err := fmt.Fprintf(out, "%s\tbundle=%s\tpid=%d\twindows=%d\tfrontmost=%t\n", cliout.Sanitize(app.Name), cliout.Sanitize(app.BundleID), app.PID, app.WindowCount, app.Frontmost); err != nil {
				return err
			}
		}
		return nil
	case []computeruse.Window:
		for _, window := range value {
			if _, err := fmt.Fprintf(out, "[%d]\tid=%d\t%s\tframe=%.0f,%.0f %.0fx%.0f\tfocused=%t\n", window.Index, window.ID, cliout.Sanitize(window.Title), window.Frame.X, window.Frame.Y, window.Frame.Width, window.Frame.Height, window.Focused); err != nil {
				return err
			}
		}
		return nil
	case computeruse.State:
		if value.Snapshot.TreeText != "" {
			if _, err := fmt.Fprintln(out, value.Snapshot.TreeText); err != nil {
				return err
			}
		}
		if value.RefreshError != nil {
			_, err := fmt.Fprintf(out, "Action completed; refreshed state unavailable: %s\n", cliout.Sanitize(value.RefreshError.Message))
			return err
		}
		if value.Screenshot != nil {
			if _, err := fmt.Fprintf(out, "screenshot: %s (%dx%d)\n", value.Screenshot.Path, value.Screenshot.Width, value.Screenshot.Height); err != nil {
				return err
			}
		} else if value.ScreenshotError != nil {
			if _, err := fmt.Fprintf(out, "screenshot unavailable: %s\n", cliout.Sanitize(value.ScreenshotError.Message)); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("computer %s returned unsupported result type %T", command, result)
	}
}

func invalid(message string) error {
	return computeruse.NewError(computeruse.CodeInvalidArgument, message, nil)
}

func asCodedError(err error) *computeruse.Error {
	var coded *computeruse.Error
	if errors.As(err, &coded) {
		return coded
	}
	return computeruse.NewError(computeruse.ErrorCode(err), err.Error(), err)
}

func jsonRequested(args []string) bool {
	enabled := false
	visitStandaloneArguments(args, func(arg string) {
		if arg == "--json" || arg == "-json" {
			enabled = true
			return
		}
		name, value, hasValue := strings.Cut(arg, "=")
		if (name != "--json" && name != "-json") || !hasValue {
			return
		}
		parsed, err := strconv.ParseBool(value)
		if err == nil {
			enabled = parsed
		}
	})
	return enabled
}

func isHelp(value string) bool {
	return value == "help" || value == "--help" || value == "-h"
}

func helpRequested(args []string) bool {
	return standaloneArgumentRequested(args, func(arg string) bool {
		return arg == "--help" || arg == "-h"
	})
}

func standaloneArgumentRequested(args []string, requested func(string) bool) bool {
	found := false
	visitStandaloneArguments(args, func(arg string) {
		if requested(arg) {
			found = true
		}
	})
	return found
}

func visitStandaloneArguments(args []string, visit func(string)) {
	valueFlags := map[string]bool{
		"--app": true, "--window-index": true, "--window-id": true, "--element-index": true, "--from-element-index": true, "--to-element-index": true,
		"--x": true, "--y": true, "--from-x": true, "--from-y": true, "--to-x": true, "--to-y": true,
		"--value": true, "--text": true, "--key": true, "--direction": true, "--amount": true, "--action": true,
		"--mouse-button": true, "--click-count": true, "--modifiers": true, "--out": true, "--session": true,
	}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			return
		}
		visit(arg)
		canonical := arg
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
			canonical = "--" + strings.TrimPrefix(arg, "-")
		}
		name, _, hasInlineValue := strings.Cut(canonical, "=")
		if valueFlags[name] && !hasInlineValue {
			index++
		}
	}
}
