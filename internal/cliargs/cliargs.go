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
