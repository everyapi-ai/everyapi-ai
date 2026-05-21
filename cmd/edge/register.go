package edge

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/everyapi-ai/everyapi-sdk/api"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
)

func edgeRegister(args []string) error {
	fs := flag.NewFlagSet("edge register", flag.ContinueOnError)
	name := fs.String("name", "", "Human-readable name (e.g. 'rtx-4090-tokyo'). Required.")
	country := fs.String("country", "", "Two-letter country code (optional; advertised to buyers for latency).")
	region := fs.String("region", "", "Region label (optional; freeform, e.g. 'us-west').")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*name) == "" {
		return errors.New("--name is required (e.g. --name 'rtx-4090-tokyo')")
	}

	client, creds, err := edgeClient()
	if err != nil {
		return err
	}

	req := api.EdgeNodeCreate{Name: strings.TrimSpace(*name)}
	if *country != "" || *region != "" {
		req.Location = &api.EdgeLoc{Country: strings.ToUpper(*country), Region: *region}
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
	mb, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(metaPath, mb, 0o600); err != nil {
		return fmt.Errorf("persist node metadata: %w", err)
	}
	if err := setActiveNodeID(reg.Node.ID); err != nil {
		return fmt.Errorf("set active node: %w", err)
	}

	cliout.Printf("✓ Registered node #%d (%s)\n", reg.Node.ID, reg.Node.Name)
	cliout.Printf("✓ Token + metadata saved to %s\n", metaPath)
	cliout.Printf("✓ Set as active node\n")
	cliout.Printf("\nNext: everyapi edge start\n")
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
			return nil, fmt.Errorf("no local data for node %d — was 'everyapi edge register' run on a different machine?", id)
		}
		return nil, err
	}
	var meta nodeMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return nil, fmt.Errorf("decode node.json: %w", err)
	}
	return &meta, nil
}
