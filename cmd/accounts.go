package cmd

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/style"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// Accounts dispatches `everyapi auth accounts <sub>` — the management surface for signing into more than one EveryAPI account from the same machine.
//
// The plural name is deliberate: `everyapi account` (singular) is the profile / 2FA / subscription namespace for the account you are signed in AS, while this is the set of credentials this machine holds. Folding these together would make `everyapi account switch` read as "switch my subscription".
//
// Picking WHICH account a one-off command runs as is not here — that is the global `--account <name>` flag, handled in main before dispatch.
func Accounts(args []string) error {
	if len(args) == 0 {
		return runAccountsList(nil)
	}
	switch args[0] {
	case "help", "--help", "-h":
		cliout.Println(i18n.T("accounts.usage"))
		return nil
	case "list", "ls":
		return runAccountsList(args[1:])
	case "switch", "use":
		return runAccountsSwitch(args[1:])
	case "rename":
		return runAccountsRename(args[1:])
	case "remove", "rm":
		return runAccountsRemove(args[1:])
	default:
		cliout.Println(i18n.T("accounts.usage"))
		return fmt.Errorf(i18n.T("accounts.unknown_sub"), args[0])
	}
}

// accountJSON is the machine shape of one stored account. It carries identity only — never the access token, relay key, or file path — so it is safe for a desktop sidecar to render.
type accountJSON struct {
	Name     string `json:"name"`
	Active   bool   `json:"active"`
	UserID   int    `json:"user_id,omitempty"`
	Username string `json:"username,omitempty"`
	APIBase  string `json:"api_base,omitempty"`
}

func runAccountsList(args []string) error {
	fs := flag.NewFlagSet("auth accounts list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "human", "output format (human or json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectFlagPositionals(fs); err != nil {
		return err
	}
	infos, err := config.ListAccounts()
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(*format)) {
	case "json":
		rows := make([]accountJSON, 0, len(infos))
		for _, info := range infos {
			rows = append(rows, accountJSON{Name: info.Name, Active: info.Active, UserID: info.UserID, Username: info.Username, APIBase: info.APIBase})
		}
		return json.NewEncoder(cliout.Out).Encode(rows)
	case "human":
	default:
		return fmt.Errorf("unsupported format %q", *format)
	}
	if len(infos) == 0 {
		cliout.Println(i18n.T("accounts.none"))
		return nil
	}
	width := 0
	for _, info := range infos {
		if len(info.Name) > width {
			width = len(info.Name)
		}
	}
	cliout.Println("")
	for _, info := range infos {
		marker := "  "
		name := info.Name
		if info.Active {
			marker = style.Bold("* ")
			name = style.Bold(info.Name)
		}
		pad := strings.Repeat(" ", width-len(info.Name))
		cliout.Printf("  %s%s%s  %s\n", marker, name, pad, accountDetail(info))
	}
	cliout.Println("")
	cliout.Println(i18n.T("accounts.list_hint"))
	return nil
}

// accountDetail renders the identifying half of a list row. Everything in it came from the backend at login time, so it goes through the sanitizer for the same reason `auth status` sanitizes the username it prints.
func accountDetail(info config.AccountInfo) string {
	parts := make([]string, 0, 2)
	if info.Username != "" {
		parts = append(parts, cliout.Sanitize(info.Username))
	} else if info.UserID > 0 {
		parts = append(parts, fmt.Sprintf("user %d", info.UserID))
	}
	if host := apiBaseLabel(info.APIBase); host != "" {
		parts = append(parts, host)
	}
	return strings.Join(parts, "  ·  ")
}

func apiBaseLabel(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	label := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	return cliout.Sanitize(strings.TrimRight(label, "/"))
}

func runAccountsSwitch(args []string) error {
	fs := flag.NewFlagSet("auth accounts switch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("usage: everyapi auth accounts switch [<name>]")
	}
	name := ""
	if fs.NArg() == 1 {
		name = fs.Arg(0)
	}
	if name == "" {
		picked, err := pickAccount(i18n.T("accounts.pick"))
		if err != nil {
			return err
		}
		name = picked
	}
	return switchToAccount(name)
}

// pickAccount renders the stored accounts and returns the chosen name. Off a TTY it refuses rather than picking for the user — silently switching the machine's active account is not a reasonable default for a script.
func pickAccount(prompt string) (string, error) {
	infos, err := config.ListAccounts()
	if err != nil {
		return "", err
	}
	if len(infos) == 0 {
		return "", errors.New(i18n.T("accounts.none"))
	}
	if !cliprompt.IsInteractive() {
		return "", errors.New(i18n.T("accounts.needs_name"))
	}
	labels := make([]string, len(infos))
	initial := 0
	width := 0
	for _, info := range infos {
		if len(info.Name) > width {
			width = len(info.Name)
		}
	}
	for i, info := range infos {
		if info.Active {
			initial = i
		}
		pad := strings.Repeat(" ", width-len(info.Name))
		labels[i] = style.Bold(info.Name) + pad + "  " + accountDetail(info)
	}
	idx, err := cliprompt.PickWithSelected(prompt, labels, initial)
	if err != nil {
		return "", err
	}
	return infos[idx].Name, nil
}

func switchToAccount(name string) error {
	unlock, err := acquireCredentialLock()
	if err != nil {
		return fmt.Errorf("lock credential cache: %w", err)
	}
	defer unlock()
	active, err := config.ActiveAccountName()
	if err != nil {
		return err
	}
	if name == active {
		cliout.Printf(i18n.T("accounts.already_active")+"\n", cliout.Sanitize(name))
		return nil
	}
	if err := config.SwitchAccount(name); err != nil {
		return accountsError(err)
	}
	cliout.Printf(i18n.T("accounts.switched")+"\n", style.Bold(cliout.Sanitize(name)))
	return nil
}

func runAccountsRename(args []string) error {
	fs := flag.NewFlagSet("auth accounts rename", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: everyapi auth accounts rename <old> <new>")
	}
	oldName, newName := fs.Arg(0), fs.Arg(1)
	unlock, err := acquireCredentialLock()
	if err != nil {
		return fmt.Errorf("lock credential cache: %w", err)
	}
	defer unlock()
	exists, err := config.AccountExists(oldName)
	if err != nil {
		return accountsError(err)
	}
	if !exists {
		return fmt.Errorf(i18n.T("accounts.unknown"), oldName)
	}
	if err := config.RenameAccount(oldName, newName); err != nil {
		return accountsError(err)
	}
	cliout.Printf(i18n.T("accounts.renamed")+"\n", cliout.Sanitize(oldName), style.Bold(cliout.Sanitize(newName)))
	return nil
}

func runAccountsRemove(args []string) error {
	fs := flag.NewFlagSet("auth accounts remove", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: everyapi auth accounts remove <name>")
	}
	name := fs.Arg(0)
	unlock, err := acquireCredentialLock()
	if err != nil {
		return fmt.Errorf("lock credential cache: %w", err)
	}
	defer unlock()
	exists, err := config.AccountExists(name)
	if err != nil {
		return accountsError(err)
	}
	if !exists {
		return fmt.Errorf(i18n.T("accounts.unknown"), name)
	}
	remaining, err := signOutAccount(name)
	if err != nil {
		return err
	}
	cliout.Printf(i18n.T("accounts.removed")+"\n", cliout.Sanitize(name))
	printRemainingAccounts(remaining)
	return nil
}

// signOutAccount deletes one account's credential and scrubs the per-tool homes. It returns the number of accounts still stored afterwards, so the caller can tell the user what is left.
//
// Signing out of the ACTIVE account deliberately leaves the machine signed out rather than promoting a remaining account into its place. An earlier revision promoted, on the reasoning that a machine holding live sessions should not report "not logged in" — but the next `everyapi use` after such a logout would silently relay, and bill, through an account the user did not choose. Whatever it costs in convenience, "not logged in" is the only honest answer to a logout, and a promotion is one `auth accounts switch` away. Reporting the truth also keeps EveryAPI Connect's sign-out honest: it shells out to this command and then tells its user they are signed out.
//
// Caller holds the credential lock, and the caller prints — logout and `accounts remove` word the sign-out line differently, and both print it before the "what is left" notice so the sequence reads in the order it happened.
func signOutAccount(name string) (int, error) {
	if err := config.DeleteAccount(name); err != nil {
		return 0, err
	}
	// The per-tool credential homes carry whichever account last ran `everyapi use`, and nothing on disk records which one that was. Scrub on every sign-out: regenerating them costs one launch, while leaving a billable relay key behind is exactly the failure logout exists to prevent.
	scrubToolCredentials()
	infos, err := config.ListAccounts()
	if err != nil {
		// The sign-out itself succeeded; failing here would report a failure for work that is already done. The caller just loses the trailing hint.
		return 0, nil
	}
	return len(infos), nil
}

// printRemainingAccounts points at the accounts a sign-out left behind. Silent when none are left (a plain signed-out machine needs no footnote) and when one is still active, which is the `--account other` case where nothing about the current session changed.
func printRemainingAccounts(remaining int) {
	if remaining == 0 {
		return
	}
	if name, err := config.ActiveAccountName(); err == nil && name != "" {
		return
	}
	cliout.Printf(i18n.T("accounts.remaining")+"\n", remaining)
}

// ApplyAccountSelection pins the process to the account named by the global --account flag (or EVERYAPI_ACCOUNT), translating the store's sentinels on the way out. It lives here rather than in main so the failure a user is most likely to hit — a typo in the account name — reads in their language, with the listing command that fixes it.
func ApplyAccountSelection(name string) error {
	if err := config.SelectAccount(name); err != nil {
		return accountsError(err)
	}
	return nil
}

// accountsError translates the store's sentinels into the localized, user-facing wording. Anything else passes through untouched so a real filesystem failure still reads like one.
func accountsError(err error) error {
	switch {
	case errors.Is(err, config.ErrNoSuchAccount):
		return fmt.Errorf(i18n.T("accounts.unknown"), trailingName(err))
	case errors.Is(err, config.ErrAccountExists):
		return fmt.Errorf(i18n.T("accounts.exists"), trailingName(err))
	case errors.Is(err, config.ErrInvalidAccountName):
		return fmt.Errorf(i18n.T("accounts.invalid_name"), trailingName(err), config.MaxAccountNameLen)
	default:
		return err
	}
}

// trailingName pulls the account name the store appended after ": " when wrapping its sentinel, so the localized message can name it without the sentinel's English prefix.
//
// The FIRST ": " is the separator, not the last: an invalid name is echoed back verbatim and a user who typed `--account 'a: b'` would otherwise be told the offending value was `b`.
func trailingName(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ": "); i >= 0 {
		return msg[i+2:]
	}
	return msg
}
