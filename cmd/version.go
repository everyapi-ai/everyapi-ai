package cmd

import (
	"flag"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/version"
)

func Version(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectFlagPositionals(fs); err != nil {
		return err
	}
	ver, commit := version.Resolve()
	cliout.Printf("everyapi %s (commit %s)\n", ver, commit)
	return nil
}
