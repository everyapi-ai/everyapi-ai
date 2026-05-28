package edge

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/everyapi-ai/everyapi-sdk/api"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

func edgeRegister(args []string) error {
	fs := flag.NewFlagSet("edge register", flag.ContinueOnError)
	name := fs.String("name", "", "Human-readable name (e.g. 'rtx-4090-tokyo'). Required.")
	country := fs.String("country", "", "Two-letter country code (optional; advertised to buyers for latency).")
	region := fs.String("region", "", "Region label (optional; freeform, e.g. 'us-west').")
	// attachTo wires the seller's "one channel, N machines" mode
	// from EDGE_NODE.md §5. Default 0 = unset, which the SDK maps to
	// omitting the field and the backend interprets as "create a
	// fresh channel for this node." A non-zero value validates
	// server-side: must be an edge-kind channel owned by the same
	// seller, otherwise the response is 422 errEdgeAttachInvalid.
	attachTo := fs.Int("attach-to-channel", 0,
		"Channel ID to attach this node to as a sibling (multi-node load balancing). "+
			"Omit to auto-create a fresh channel for this node.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	nodeName := strings.TrimSpace(*name)
	if nodeName == "" {
		// Prompt for the missing required flag on a TTY; off-TTY
		// (CI / piped) keep the original "--name is required"
		// error so scripted invocations stay deterministic.
		if !cliprompt.IsInteractive() {
			return errors.New(i18n.T("edge.register.name_required"))
		}
		in := bufio.NewReader(os.Stdin)
		v, perr := cliprompt.Line(in, i18n.T("edge.register.name_prompt"), "")
		if perr != nil {
			return perr
		}
		nodeName = strings.TrimSpace(v)
	}

	client, creds, err := edgeClient()
	if err != nil {
		return err
	}

	req := api.EdgeNodeCreate{Name: nodeName}
	if *country != "" || *region != "" {
		req.Location = &api.EdgeLoc{CountryISO2: strings.ToUpper(*country), Region: *region}
	}
	if *attachTo > 0 {
		req.AttachToChannelID = attachTo
	}

	reg, err := client.CreateEdgeNode(cliout.WithCtx(), req)
	if err != nil {
		return classifyRegisterErr(err)
	}

	// Persist meta BEFORE writing the active pointer — if disk write
	// fails after, the user can re-run register without ending up with
	// a dangling active pointer to a node whose token wasn't saved.
	dir, err := nodeDir(reg.Node.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	meta := nodeMeta{
		NodeID:            reg.Node.ID,
		NodeName:          reg.Node.Name,
		RegistrationToken: reg.RegistrationToken,
		Gateway:           gatewayURLFromAPIBase(creds.APIBase),
	}
	metaPath := filepath.Join(dir, "node.json")
	mb, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		// Should not happen with the small, fixed nodeMeta struct,
		// but swallowing this would silently write an empty file
		// and brick the next `everyapi edge start` (loadNodeMeta
		// would decode the zero JSON into a zero-valued meta with
		// no token, no gateway URL) with no visible root cause.
		return fmt.Errorf(i18n.T("edge.register.persist_meta_failed"), err)
	}
	if err := os.WriteFile(metaPath, mb, 0o600); err != nil {
		return fmt.Errorf(i18n.T("edge.register.persist_meta_failed"), err)
	}
	if err := setActiveNodeID(reg.Node.ID); err != nil {
		return fmt.Errorf(i18n.T("edge.register.set_active_failed"), err)
	}

	cliout.Printf(i18n.T("edge.register.registered"), reg.Node.ID, reg.Node.Name)
	cliout.Printf(i18n.T("edge.register.saved"), metaPath)
	cliout.Printf("%s", i18n.T("edge.register.set_active"))
	cliout.Printf("%s", i18n.T("edge.register.next"))
	return nil
}

// classifyRegisterErr surfaces the backend's structured error messages
// for the two register failures the user can act on:
//
//	"Channel marketplace is currently closed; node registration is disabled."
//	"Channel mount cap reached (10)."
//
// Pass-through for everything else so server-side messages reach the
// user verbatim — fine-grained mapping isn't worth the maintenance
// (every backend error string update would mean a CLI release).
func classifyRegisterErr(err error) error {
	return err
}

// loadNodeMeta reads node.json for the given id. Used by start/stop/
// logs to pick up the persisted token + gateway URL.
func loadNodeMeta(id int) (*nodeMeta, error) {
	dir, err := nodeDir(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "node.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(i18n.T("edge.register.no_local_data"), id)
		}
		return nil, err
	}
	var meta nodeMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return nil, fmt.Errorf(i18n.T("edge.register.decode_failed"), err)
	}
	return &meta, nil
}
