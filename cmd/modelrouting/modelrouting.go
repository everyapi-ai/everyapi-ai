package modelrouting

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/internal/cliargs"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const protocolVersion = 1
const maxPayloadBytes = 64 * 1024

type machineView struct {
	Version   int                        `json:"version"`
	Mode      string                     `json:"mode"`
	Providers []api.ModelRoutingProvider `json:"providers"`
}

func Run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi account routing <get|set> --format=json")
	}
	creds, err := config.Load()
	if err != nil {
		return err
	}
	client := api.ForCredentials(creds)
	var view *api.ModelRoutingView
	switch args[0] {
	case "get":
		fs := flag.NewFlagSet("account routing get", flag.ContinueOnError)
		format := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if err := cliargs.RejectPositionals(fs); err != nil {
			return err
		}
		if *format != "json" {
			return errors.New("account routing get requires --format=json")
		}
		view, err = client.GetModelRouting(cliout.WithCtx())
	case "set":
		fs := flag.NewFlagSet("account routing set", flag.ContinueOnError)
		format := fs.String("format", "", "output format")
		payload := fs.String("payload", "", "routing JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if err := cliargs.RejectPositionals(fs); err != nil {
			return err
		}
		if *format != "json" || *payload == "" || len(*payload) > maxPayloadBytes {
			return errors.New("account routing set requires bounded --payload and --format=json")
		}
		var setting api.ModelRoutingSetting
		decoder := json.NewDecoder(strings.NewReader(*payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&setting); err != nil {
			return fmt.Errorf("invalid routing payload: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return errors.New("invalid routing payload: expected exactly one JSON value")
		}
		view, err = client.UpdateModelRouting(cliout.WithCtx(), setting)
	default:
		return fmt.Errorf("unknown account routing action %q", args[0])
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(cliout.Out).Encode(machineView{
		Version: protocolVersion, Mode: view.Mode, Providers: view.Providers,
	})
}
