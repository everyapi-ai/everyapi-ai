package cmd

import (
	"flag"
	"fmt"
)

func rejectFlagPositionals(fs *flag.FlagSet) error {
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	return nil
}
