package edge

import (
	"errors"
	"flag"
	"strings"

	"github.com/everyapi-ai/everyapi-sdk/api"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
)

// edgeRename calls PUT /api/seller/edge/nodes/:id with name + location. Wraps the backend's UpdateEdgeNode endpoint — the same payload the dashboard's edit-form posts. The backend syncs the paired Channel.Name in the same transaction, so a rename here keeps the relay-router listing and the dashboard display in lockstep.
//
// At least one of --name / --country / --region / --clear-location must be supplied; invoking with no fields is a no-op the backend would accept but is almost certainly a mistake on the user's part, so we refuse it client-side with a clear error.
//
// Location semantics:
//   - --country and --region are paired; passing one without the other sends an empty string for the omitted half.
//   - --clear-location is the explicit "remove the supplier-declared location" affordance. Mutually exclusive with --country / --region (passing both is a user error we refuse up-front).
//   - Omitting all three location flags leaves the existing location untouched — the backend uses a tri-state on EdgeNodeUpdate.Location where nil = "don't change", non-nil = "replace with this".
func edgeRename(args []string) error {
	fs := flag.NewFlagSet("edge rename", flag.ContinueOnError)
	nodeFlag := fs.Int("node", 0, "Operate on this node id (default: active node)")
	name := fs.String("name", "", "New human-readable name. Empty leaves it unchanged.")
	country := fs.String("country", "", "Two-letter country code. Updates location if set.")
	region := fs.String("region", "", "Region label. Updates location if set.")
	clearLoc := fs.Bool("clear-location", false, "Explicitly remove the declared location. Mutually exclusive with --country / --region.")
	workloadsFlag := fs.String("workloads", "",
		"Comma-separated capability declaration to replace the current one (allowed: "+
			strings.Join(api.KnownEdgeWorkloads, ", ")+"). Empty leaves it unchanged.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectPositionals(fs); err != nil {
		return err
	}
	workloads, err := parseWorkloadsFlag(*workloadsFlag)
	if err != nil {
		return err
	}

	trimmedName := strings.TrimSpace(*name)
	if trimmedName == "" && *country == "" && *region == "" && !*clearLoc && len(workloads) == 0 {
		return errors.New(i18n.T("edge.rename.nothing_to_change"))
	}
	if *clearLoc && (*country != "" || *region != "") {
		return errors.New(i18n.T("edge.rename.clear_with_country"))
	}

	nodeID, err := resolveNodeID(*nodeFlag)
	if err != nil {
		return err
	}

	client, _, err := edgeClient()
	if err != nil {
		return err
	}

	req := api.EdgeNodeUpdate{Name: trimmedName, Workloads: workloads}
	switch {
	case *clearLoc:
		// Explicit clear: send an empty Location object so the backend's tri-state treats it as "replace with empty" not "leave unchanged."
		req.Location = &api.EdgeLoc{}
	case *country != "" || *region != "":
		req.Location = &api.EdgeLoc{
			CountryISO2: strings.ToUpper(*country),
			Region:      *region,
		}
	}

	if err := client.UpdateEdgeNode(cliout.WithCtx(), nodeID, req); err != nil {
		return err
	}

	// Mirror the change into on-disk meta so the next compose re-render (`edge start` / `edge update`) emits the same EVERYAPI_WORKLOADS and `edge status` shows the same name the backend now has. Persist when EITHER --name or --workloads changed: gating only on the name (the old behaviour) left meta.Workloads stale, so the next re-render kept shipping the old capability set. Best-effort: server is the source of truth, so a meta-save failure doesn't undo the rename, but surface it so the user knows the local cache is stale.
	if trimmedName != "" || len(workloads) > 0 {
		if meta, mErr := loadNodeMeta(nodeID); mErr == nil {
			if trimmedName != "" {
				meta.NodeName = trimmedName
			}
			if len(workloads) > 0 {
				meta.Workloads = workloads
			}
			if sErr := saveNodeMeta(nodeID, meta); sErr != nil {
				cliout.Printf(i18n.T("edge.rename.meta_save_warn"), sErr)
			}
		}
	}

	cliout.Printf(i18n.T("edge.rename.done"), nodeID)
	return nil
}
