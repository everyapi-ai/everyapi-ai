package menubar

import "errors"

// errConfirmDialogUnsupported surfaces when no native confirm-modal
// helper is available on the host (Linux without zenity/kdialog;
// other niche cases). Callers must translate this into a user-
// visible notification — silently auto-confirming would defeat the
// §4.7-7-5 Layer 3 anti-phishing modal, so the dialog primitive
// fails closed.
var errConfirmDialogUnsupported = errors.New("no zenity or kdialog on PATH — install one for the anti-phishing modal")

// This file holds the package-level indirection vars for every
// shell-out the menubar performs (text input dialogs, clipboard
// reads, confirm dialogs). The real implementations live in
// build-tagged files (textinput_darwin.go, dialog_unix.go, …); these
// vars let tests substitute stubs without spawning processes.
//
// Keeping the indirection in one file is intentional — when a new
// shell-out is added the visibility of "what can the test swap?" is
// in one place.

var (
	textPrompt     = realTextPrompt
	readClipboard  = realReadClipboard
	writeClipboard = realWriteClipboard
	confirmDialog  = realConfirmDialog
)
