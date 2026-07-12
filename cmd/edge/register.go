package edge

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	workloadsFlag := fs.String("workloads", "",
		"Comma-separated capability declaration (allowed: "+strings.Join(api.KnownEdgeWorkloads, ", ")+"). "+
			"Defaults to 'chat'. Routing prefers nodes that declared the request's workload.")
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

	req := api.EdgeNodeCreate{Name: nodeName, Workloads: workloads}
	if *country != "" || *region != "" {
		req.Location = &api.EdgeLoc{CountryISO2: strings.ToUpper(*country), Region: *region}
	}
	if *attachTo > 0 {
		// Pre-validate the channel id against the seller's own
		// node listing — fail fast with a usable hint instead of
		// the backend's terse 422 "attach_to_channel_id is not a
		// valid edge channel you own". Listing returns at most the
		// seller's own ~10 nodes (the per-seller channel cap), so
		// the extra round-trip is cheap and the error message
		// names the wrong id explicitly + suggests `edge list`.
		if vErr := validateAttachToChannel(client, *attachTo); vErr != nil {
			return vErr
		}
		req.AttachToChannelID = attachTo
	}

	reg, err := client.CreateEdgeNode(cliout.WithCtx(), req)
	if err != nil {
		return classifyRegisterErr(err)
	}

	// Persist meta BEFORE writing the active pointer — if disk write
	// fails after, the user can re-run register without ending up with
	// a dangling active pointer to a node whose token wasn't saved.
	//
	// The backend node row already exists and stores only the token's
	// sha256, so reg.RegistrationToken is the ONLY copy of the raw secret
	// from here on. Resolve the gateway + intended node.json path up front
	// so EVERY post-registration failure below — including nodeDir() and
	// MkdirAll(), not just the later marshal/write — can echo the one-time
	// token via printTokenRescue instead of losing it forever.
	gateway := gatewayURLFromAPIBase(creds.APIBase)
	dir, err := nodeDir(reg.Node.ID)
	if err != nil {
		printTokenRescue(reg.Node.ID, gateway, reg.RegistrationToken, "")
		return err
	}
	metaPath := filepath.Join(dir, "node.json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		printTokenRescue(reg.Node.ID, gateway, reg.RegistrationToken, metaPath)
		return err
	}
	meta := nodeMeta{
		NodeID:            reg.Node.ID,
		NodeName:          reg.Node.Name,
		RegistrationToken: reg.RegistrationToken,
		Gateway:           gateway,
		// Server-normalized declaration (defaulted to ["chat"] when the
		// flag was omitted) — persisted so `edge start` renders the same
		// EVERYAPI_WORKLOADS the row carries.
		Workloads: reg.Node.Workloads,
	}
	mb, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		// Should not happen with the small, fixed nodeMeta struct, but
		// swallowing this would silently write an empty file and brick the
		// next `everyapi edge start` (loadNodeMeta would decode the zero
		// JSON into a zero-valued meta with no token, no gateway URL) with
		// no visible root cause.
		printTokenRescue(reg.Node.ID, gateway, reg.RegistrationToken, metaPath)
		return fmt.Errorf(i18n.T("edge.register.persist_meta_failed"), err)
	}
	if err := atomicWriteNodeJSON(metaPath, mb); err != nil {
		// Disk write failed (ENOSPC / EACCES / disk-full). The raw token
		// lives nowhere else, so echo it to stdout first so the operator
		// can hand-create node.json and still start the node. The atomic
		// temp-then-rename write guarantees a failure here leaves no
		// truncated node.json behind.
		printTokenRescue(reg.Node.ID, gateway, reg.RegistrationToken, metaPath)
		return fmt.Errorf(i18n.T("edge.register.persist_meta_failed"), err)
	}
	if err := setActiveNodeID(reg.Node.ID); err != nil {
		return fmt.Errorf(i18n.T("edge.register.set_active_failed"), err)
	}

	cliout.Printf(i18n.T("edge.register.registered"), reg.Node.ID, cliout.Sanitize(reg.Node.Name))
	cliout.Printf(i18n.T("edge.register.saved"), metaPath)
	cliout.Printf("%s", i18n.T("edge.register.set_active"))
	cliout.Printf("%s", i18n.T("edge.register.next"))
	return nil
}

// printTokenRescue echoes the one-time registration token (plus the
// node id, gateway URL and the path node.json should live at) to stdout
// when persisting node.json fails. The backend only keeps the token's
// hash, so this print is the operator's last chance to capture the raw
// secret before it's gone for good.
func printTokenRescue(nodeID int, gateway, token, metaPath string) {
	if metaPath == "" {
		// nodeDir() couldn't resolve the edge data dir (e.g. no HOME); give
		// a generic location hint so the rescue message still reads cleanly.
		metaPath = fmt.Sprintf("<edge data dir>/%d/node.json", nodeID)
	}
	cliout.Printf(i18n.T("edge.register.token_rescue"), nodeID, gateway, token, metaPath)
}

// parseWorkloadsFlag splits and validates the --workloads value
// against the SDK's mirror of the backend enum. Local validation so a
// typo'd value fails with the allowed list in the message instead of
// a round-trip to the server's 422. Empty input returns nil — the
// backend defaults that to ["chat"].
func parseWorkloadsFlag(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	known := map[string]bool{}
	for _, k := range api.KnownEdgeWorkloads {
		known[k] = true
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		w := strings.ToLower(strings.TrimSpace(part))
		if w == "" {
			continue
		}
		if !known[w] {
			return nil, fmt.Errorf(i18n.T("edge.register.unknown_workload"), w, strings.Join(api.KnownEdgeWorkloads, ", "))
		}
		out = append(out, w)
	}
	return out, nil
}

// validateAttachToChannel checks that the supplied channel id
// appears as channel_id on one of the seller's existing nodes. The
// backend's POST /api/seller/edge/nodes rejects an invalid
// attach_to_channel_id with a single sentinel error
// (errEdgeAttachInvalid) that doesn't enumerate the IDs the seller
// owns — pre-validating client-side lets us emit a hint that names
// the existing channels and points at `everyapi edge list`.
//
// Best-effort: any list error (network, auth) skips the check and
// lets the server's validation run. The intent here is help on the
// happy-path typo, not a hard gate.
//
// Edge case deliberately deferred to the backend: a seller may own
// an edge channel that has zero nodes attached (created via
// dashboard, then all nodes removed). Our proxy here is "channel
// has a node owned by me", so that channel won't appear in `owned`.
// Rather than mis-classify it as "no edge channels", we only emit
// the unknown-channel hint when we have at least one known id; for
// the empty case we silently defer to backend validation. Trade-off:
// the user sees the terser backend message in that rare path, but
// avoids the worse "you don't own any channels" lie that would push
// them to drop the flag and create a duplicate.
func validateAttachToChannel(client *api.Client, channelID int) error {
	nodes, err := client.ListEdgeNodes(cliout.WithCtx())
	if err != nil {
		return nil
	}
	owned := map[int]bool{}
	for _, n := range nodes {
		if n.ChannelID != nil {
			owned[*n.ChannelID] = true
		}
	}
	if owned[channelID] {
		return nil
	}
	if len(owned) == 0 {
		// Defer to backend — we can't distinguish "no channels at all"
		// from "channels exist but have no nodes attached".
		return nil
	}
	ids := make([]int, 0, len(owned))
	for id := range owned {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	idStrs := make([]string, len(ids))
	for i, id := range ids {
		idStrs[i] = fmt.Sprintf("%d", id)
	}
	return fmt.Errorf(i18n.T("edge.register.attach_unknown_channel"), channelID, strings.Join(idStrs, ", "))
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
