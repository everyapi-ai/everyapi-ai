package mcp

// Edge BYO-GPU tools — surface the read-and-delete slice of the
// /api/seller/edge/nodes API to the AI agent. Start / stop / logs /
// register stay CLI-only on purpose:
//
//   - start / stop / logs run docker-compose against a local workdir
//     the MCP server process doesn't have a useful view of (the AI
//     client spawns mcp via stdio, not in the user's shell).
//   - register mints a single-use registration token that's shown
//     ONCE. Surfacing one-shot secrets through an AI chat is a UX
//     and security regression — type it at a real terminal.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/api"
)

// ---- everyapi_edge_list ----------------------------------------------

func toolEdgeList() Tool {
	return Tool{
		Name: "everyapi_edge_list",
		Description: "List the user's registered BYO-GPU edge nodes on EveryAPI " +
			"(id, name, online/offline status, paired channel id, last-seen time, " +
			"installed model list). Use when the user asks 'what edge nodes do I have', " +
			"'are my GPU nodes online', or wants an overview of their BYO-GPU fleet.",
		InputSchema: emptyObjectSchema,
		Handler:     handleEdgeList,
	}
}

func handleEdgeList(ctx context.Context, _ json.RawMessage) (string, error) {
	creds, err := loadCreds()
	if err != nil {
		return "", err
	}
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)
	nodes, err := client.ListEdgeNodes(ctx)
	if err != nil {
		return "", classifyAPIErr(err)
	}
	if len(nodes) == 0 {
		return "No edge nodes registered. Visit " +
			api.WebOriginFromBase(creds.APIBase) +
			"/seller/edge — or run `everyapi edge register --name <node-name>` " +
			"in a terminal — to add one.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d edge node(s):\n", len(nodes))
	for _, n := range nodes {
		fmt.Fprintf(&b, "  [#%d] %s — status=%s%s",
			n.ID, cliout.Sanitize(n.Name), cliout.Sanitize(n.Status), pausedSuffix(n.Paused))
		if n.ChannelID != nil {
			fmt.Fprintf(&b, " channel=#%d", *n.ChannelID)
		}
		if n.LastSeenAt > 0 {
			fmt.Fprintf(&b, " last_seen=%s ago",
				humanDuration(time.Since(time.Unix(n.LastSeenAt, 0))))
		}
		b.WriteByte('\n')
		if len(n.Models) > 0 {
			fmt.Fprintf(&b, "        models: %s\n", cliout.Sanitize(strings.Join(n.Models, ", ")))
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// ---- everyapi_edge_status --------------------------------------------

type edgeStatusArgs struct {
	NodeID int `json:"node_id"`
}

var edgeStatusSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "node_id": {
      "type": "integer",
      "description": "Integer node id, as shown by everyapi_edge_list (the [#N] prefix).",
      "minimum": 1
    }
  },
  "required": ["node_id"],
  "additionalProperties": false
}`)

func toolEdgeStatus() Tool {
	return Tool{
		Name: "everyapi_edge_status",
		Description: "Get one edge node's current state by id: name, online/offline, " +
			"paused flag, paired channel id, agent version, hardware (GPU model / count / VRAM), " +
			"installed models, last-seen timestamp. Use after everyapi_edge_list when the user " +
			"asks about a specific node ('is node 7 online', 'what models are on my edge node').",
		InputSchema: edgeStatusSchema,
		Handler:     handleEdgeStatus,
	}
}

func handleEdgeStatus(ctx context.Context, raw json.RawMessage) (string, error) {
	var args edgeStatusArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if args.NodeID <= 0 {
		return "", fmt.Errorf("node_id is required (positive integer; see everyapi_edge_list)")
	}
	creds, err := loadCreds()
	if err != nil {
		return "", err
	}
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)
	node, err := client.GetEdgeNode(ctx, args.NodeID)
	if err != nil {
		return "", classifyAPIErr(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Edge node #%d: %s\n", node.ID, cliout.Sanitize(node.Name))
	fmt.Fprintf(&b, "  status:    %s%s\n", cliout.Sanitize(node.Status), pausedSuffix(node.Paused))
	if node.ChannelID != nil {
		fmt.Fprintf(&b, "  channel:   #%d\n", *node.ChannelID)
	}
	if node.LastSeenAt > 0 {
		fmt.Fprintf(&b, "  last seen: %s (%s ago)\n",
			time.Unix(node.LastSeenAt, 0).Format(time.RFC3339),
			humanDuration(time.Since(time.Unix(node.LastSeenAt, 0))))
	}
	if node.AgentVer != "" {
		fmt.Fprintf(&b, "  agent ver: %s\n", cliout.Sanitize(node.AgentVer))
	}
	if len(node.Models) > 0 {
		fmt.Fprintf(&b, "  models:    %s\n", cliout.Sanitize(strings.Join(node.Models, ", ")))
	}
	if node.Hardware != nil && node.Hardware.GPUModel != "" {
		count := node.Hardware.GPUCount
		if count < 1 {
			count = 1
		}
		fmt.Fprintf(&b, "  hardware:  %s × %d (%dGB VRAM)\n",
			cliout.Sanitize(node.Hardware.GPUModel), count, node.Hardware.VRAMTotalGB)
	}
	if node.Location != nil && (node.Location.CountryISO2 != "" || node.Location.Region != "") {
		fmt.Fprintf(&b, "  location:  %s/%s\n", cliout.Sanitize(node.Location.CountryISO2), cliout.Sanitize(node.Location.Region))
	}
	// Live telemetry — nil pointers mean "no heartbeat yet" or
	// "offline"; only render if the agent actually reported. A 0%
	// util reading IS meaningful for an idle online node, so we
	// gate display on the pointer rather than the value.
	if node.GPUUtilPct != nil || node.VRAMUsedGB != nil || node.ActiveRequests != nil {
		fmt.Fprintf(&b, "  live:      ")
		sep := ""
		if node.GPUUtilPct != nil {
			fmt.Fprintf(&b, "%sGPU %d%%", sep, *node.GPUUtilPct)
			sep = ", "
		}
		if node.VRAMUsedGB != nil {
			fmt.Fprintf(&b, "%sVRAM %.1fGB", sep, *node.VRAMUsedGB)
			sep = ", "
		}
		if node.ActiveRequests != nil {
			fmt.Fprintf(&b, "%s%d active req", sep, *node.ActiveRequests)
		}
		fmt.Fprintln(&b)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// ---- everyapi_edge_remove --------------------------------------------

// Same confirm-friction pattern as everyapi_seller_withdraw: a
// destructive op authorised by the access token alone is one
// credential leak away from a silent fleet wipe. Force the AI to
// surface intent.

type edgeRemoveArgs struct {
	NodeID  int    `json:"node_id"`
	Confirm string `json:"confirm"`
}

const edgeRemoveConfirmToken = "yes"

var edgeRemoveSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "node_id": {
      "type": "integer",
      "description": "Integer node id to remove. From everyapi_edge_list's [#N] prefix.",
      "minimum": 1
    },
    "confirm": {
      "type": "string",
      "description": "Required confirmation token. Must be the literal string \"yes\". Friction step so a credential-stealing process can't silently delete the user's BYO-GPU fleet.",
      "const": "yes"
    }
  },
  "required": ["node_id", "confirm"],
  "additionalProperties": false
}`)

func toolEdgeRemove() Tool {
	return Tool{
		Name: "everyapi_edge_remove",
		Description: "Delete an edge node from the user's account (and its paired " +
			"marketplace channel if this is the last node attached). REQUIRED: pass " +
			"`confirm: \"yes\"`. The AI MUST surface the deletion to the user before " +
			"calling this tool — credential leakage on the user's machine could otherwise " +
			"silently wipe their fleet. Use only when the user explicitly asks to remove " +
			"or delete a specific node.",
		InputSchema: edgeRemoveSchema,
		Handler:     handleEdgeRemove,
	}
}

func handleEdgeRemove(ctx context.Context, raw json.RawMessage) (string, error) {
	creds, err := loadCreds()
	if err != nil {
		return "", err
	}
	var args edgeRemoveArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if args.NodeID <= 0 {
		return "", fmt.Errorf("node_id is required (positive integer; see everyapi_edge_list)")
	}
	if args.Confirm != edgeRemoveConfirmToken {
		return "", fmt.Errorf("everyapi_edge_remove requires confirm: %q. "+
			"This is a destructive tool — surface the deletion to the user before "+
			"retrying with confirm set.", edgeRemoveConfirmToken)
	}
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)
	if err := client.DeleteEdgeNode(ctx, args.NodeID); err != nil {
		return "", classifyAPIErr(err)
	}
	return fmt.Sprintf("Deleted edge node #%d. The paired channel is also dropped "+
		"if no sibling node was attached to it; otherwise the channel keeps serving "+
		"through its remaining nodes.", args.NodeID), nil
}

// ---- helpers --------------------------------------------------------

func pausedSuffix(paused bool) string {
	if paused {
		return " (paused by seller)"
	}
	return ""
}

// humanDuration is a coarse "Ns / Nm / Nh / Nd" so AI tool output
// stays compact. Sub-second precision wouldn't survive the chat-
// rendered roundtrip anyway and would just be noise.
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0 // a future / clock-skewed last_seen would otherwise render "-3s"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
