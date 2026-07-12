package seller

import (
	"flag"
	"fmt"
)

func rejectPositionals(fs *flag.FlagSet) error {
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	return nil
}
