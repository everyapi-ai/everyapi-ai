package cliargs

import (
	"flag"
	"fmt"
)

func RejectPositionals(fs *flag.FlagSet) error {
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	return nil
}

func RequireExact(args []string, n int) error {
	if len(args) != n {
		return fmt.Errorf("expected %d positional argument(s), got %d", n, len(args))
	}
	return nil
}

// IsHelp reports whether the first argument is a conventional help token.
// Leaf commands call this before interpreting a required positional value so
// `<command> --help` never reaches validation, authentication, or an API call.
func IsHelp(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "help", "--help", "-h":
		return true
	default:
		return false
	}
}
