package cmd

import (
	"flag"

	"github.com/relaya-ai/relaya-ai/internal/version"
)

func Version(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	printf("relaya %s (commit %s)\n", version.Version, version.Commit)
	return nil
}
