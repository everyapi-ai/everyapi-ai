package edge

import (
	"flag"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
)

// edgeList renders every node the authenticated user owns on the backend the CLI is currently pointed at. Mirrors the seller dashboard's `/seller/edge` view, but anchored to the CLI's own api_base + user — useful as a diagnostic when nodes show up in the CLI but not in the browser (or vice versa: confirms which backend / account the register call actually wrote to).
func edgeList(args []string) error {
	fs := flag.NewFlagSet("edge list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectPositionals(fs); err != nil {
		return err
	}

	client, creds, err := edgeClient()
	if err != nil {
		return err
	}

	// Header echoes the api_base + signed-in user. If the user is staring at an empty browser dashboard but a populated CLI list, the api_base printed here vs. the dashboard's host is the first thing to compare. Field-label width matches the per-node block below so the colon column lines up across the whole output.
	cliout.Printf("%-12s%s\n", "api_base:", creds.APIBase)
	if creds.Username != "" {
		cliout.Printf("%-12s%s (id=%d)\n\n", i18n.T("edge.field.user"), cliout.Sanitize(creds.Username), creds.UserID)
	} else {
		cliout.Printf("%-12sid=%d\n\n", i18n.T("edge.field.user"), creds.UserID)
	}

	nodes, err := client.ListEdgeNodes(cliout.WithCtx())
	if err != nil {
		return err
	}

	if len(nodes) == 0 {
		cliout.Println(i18n.T("edge.no_nodes"))
		cliout.Println(i18n.T("edge.register_hint"))
		return nil
	}

	active, _ := activeNodeID()

	for i, n := range nodes {
		if i > 0 {
			cliout.Println("")
		}
		marker := "  "
		if n.ID == active {
			marker = "* "
		}
		cliout.Printf("%s#%d %s\n", marker, n.ID, cliout.Sanitize(n.Name))
		cliout.Printf("  %-12s%s%s\n", i18n.T("edge.field.status"), cliout.Sanitize(n.Status), pauseSuffix(n.Paused))
		if n.ChannelID != nil {
			cliout.Printf("  %-12s%d\n", i18n.T("edge.field.channel"), *n.ChannelID)
		}
		if n.LastSeenAt > 0 {
			cliout.Printf(i18n.T("edge.list.last_seen_line"), i18n.T("edge.field.last_seen"),
				formatDuration(time.Since(time.Unix(n.LastSeenAt, 0))))
		}
		if len(n.Models) > 0 {
			cliout.Printf("  %-12s%s\n", i18n.T("edge.field.models"), cliout.Sanitize(strings.Join(n.Models, ", ")))
		}
		if len(n.Workloads) > 0 {
			cliout.Printf("  %-12s%s\n", i18n.T("edge.field.workloads"), cliout.Sanitize(strings.Join(n.Workloads, ", ")))
		}
	}
	cliout.Println("")
	cliout.Println(i18n.T("edge.list.active_marker"))
	return nil
}
