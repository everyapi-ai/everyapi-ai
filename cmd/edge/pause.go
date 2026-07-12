package edge

import (
	"flag"

	"github.com/everyapi-ai/everyapi-sdk/api"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

// edgeSetStatus is the shared implementation for `edge pause` and
// `edge resume`. Takes the SDK action constant + the i18n key for
// the success line directly from the dispatcher — avoids the
// pause-bool ↔ action-enum inversion the original implementation
// had (pause=true mapping to ActionDisable was the kind of inversion
// that breeds bugs the next time the file gets touched). A future
// third verb (drain, etc.) just adds another dispatcher arm.
//
// Pause is sticky: the paired channel goes to ManuallyDisabled and
// the connect/disconnect auto-flips in the handshake no-op while
// status stays there. Resume clears it back to AutoDisabled (relay
// re-routes once the next reconnect's flip fires) or Enabled
// immediately if a live session is registered in the Hub — both
// branches handled server-side; the CLI just dispatches the action.
func edgeSetStatus(args []string, verb string, action api.EdgeNodeAction, successKey string) error {
	fs := flag.NewFlagSet("edge "+verb, flag.ContinueOnError)
	nodeFlag := fs.Int("node", 0, "Operate on this node id (default: active node)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectPositionals(fs); err != nil {
		return err
	}

	nodeID, err := resolveNodeID(*nodeFlag)
	if err != nil {
		return err
	}

	client, _, err := edgeClient()
	if err != nil {
		return err
	}

	if err := client.SetEdgeNodeStatus(cliout.WithCtx(), nodeID, action); err != nil {
		return err
	}

	cliout.Printf(i18n.T(successKey), nodeID)
	return nil
}
