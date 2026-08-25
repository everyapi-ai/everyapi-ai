// Package docs wires `everyapi docs …` — the EveryAPI handbook, embedded in the binary and rendered in the terminal.
//
// Why the text ships inside the CLI rather than on a website: the questions this answers ("which env var does codex read", "what does 'no available channel' mean", "what is the base URL") come up exactly when the user is at a prompt, often on a machine with no browser, sometimes behind the network problem they are trying to diagnose. An offline handbook also gives the AI clients `everyapi use` launches a first-party source to read instead of guessing at the platform's surface.
//
// The pages live under ./topics as plain markdown so they read correctly on GitHub too; ./render.go turns that subset into terminal output. Adding a page is: drop the .md in, add its slug to topicOrder.
package docs

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/style"
)

// SiteURL is the hosted handbook, generated from these same topic files and deployed by infra/docs-site. `docs open` opens exactly this — it deliberately does NOT build a per-topic deep link, because a wrong deep link 404s where the index never does.
//
// This constant was removed once, when the host did not resolve and the CLI was advertising a DNS error under every page. It is back because the site is deployed and answering; TestSiteURLMatchesDeployment in infra/docs-site keeps it pinned to the domain that deployment actually claims, so the two cannot drift apart again silently.
const SiteURL = "https://docs.everyapi.ai/"

func Run(args []string) error {
	return run(args, cliprompt.OpenBrowser)
}

// run keeps the browser launcher injectable so command-dispatch tests never
// hand a real URL to the host OS. Production always enters through Run.
func run(args []string, openBrowser func(string) error) error {
	if len(args) == 0 {
		// Bare `everyapi docs` on a TTY → the topic picker, which IS this command's menu (a four-row list/search/open sub-menu would bury the actual content). Piped or scripted → the list, so it stays useful in a non-interactive shell.
		if cliprompt.IsInteractive() {
			return runPicker(openBrowser)
		}
		return runList()
	}
	switch args[0] {
	case "help", "--help", "-h":
		cliout.Println(i18n.T("docs.usage"))
		return nil
	case "list", "ls":
		return runList()
	case "search", "find":
		return runSearch(args[1:])
	case "open":
		return runOpen(openBrowser)
	default:
		// Anything else is read as a topic name — `everyapi docs billing` is the common case and should not need a `show` verb in front of it.
		return runShow(args[0])
	}
}

// runList prints every topic with its one-line summary, in reading order.
func runList() error {
	all, err := topics()
	if err != nil {
		return err
	}
	width := 0
	for _, t := range all {
		if w := style.Width(t.Slug); w > width {
			width = w
		}
	}
	cliout.Printf("%s\n\n", style.Bold(fmt.Sprintf(i18n.T("docs.list_header"), len(all))))
	for _, t := range all {
		pad := strings.Repeat(" ", width-style.Width(t.Slug))
		cliout.Printf("  %s%s  %s\n", style.Bold(t.Slug), pad, inline(t.Title))
	}
	cliout.Println(i18n.T("docs.list_hint"))
	return nil
}

// runShow renders one topic.
func runShow(query string) error {
	t, err := resolve(query)
	if err != nil {
		return err
	}
	printTopic(t)
	return nil
}

func printTopic(t Topic) {
	cliout.Printf("%s", render(t.Body, terminalWidth()))
	cliout.Printf("\n%s\n", style.Dim(fmt.Sprintf(i18n.T("docs.footer"), SiteURL)))
}

// runSearch scans every topic for a substring. With no query on a TTY it asks for one — the interactive picker dispatches here with empty args, and a bare usage error would be a dead end for a user who picked "search" from a menu.
func runSearch(args []string) error {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		if !cliprompt.IsInteractive() {
			return errors.New(i18n.T("docs.search_usage"))
		}
		entered, err := cliprompt.Line(bufio.NewReader(os.Stdin), i18n.T("docs.search_prompt"), "")
		if err != nil {
			return err
		}
		query = strings.TrimSpace(entered)
		if query == "" {
			return errors.New(i18n.T("docs.search_usage"))
		}
	}
	hits, err := search(query)
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		cliout.Printf("%s\n", fmt.Sprintf(i18n.T("docs.search_none"), query))
		return nil
	}
	cliout.Printf("%s\n", style.Bold(fmt.Sprintf(i18n.T("docs.search_header"), len(hits), query)))
	for _, h := range hits {
		cliout.Printf("\n%s  %s\n", style.Bold(h.Topic.Slug), style.Dim(fmt.Sprintf(i18n.T("docs.search_count"), h.Count)))
		for _, l := range h.Lines {
			// Sanitize: the sampled line is our own embedded text, but it flows to the terminal verbatim and the cost of routing it through the same guard every other printed line uses is nil.
			cliout.Printf("    %s %s\n", style.Dim(fmt.Sprintf("%d:", l.Number)), cliout.Sanitize(truncate(l.Text, terminalWidth()-8)))
		}
		if h.Count > len(h.Lines) {
			cliout.Printf("    %s\n", style.Dim(fmt.Sprintf(i18n.T("docs.search_more"), h.Count-len(h.Lines))))
		}
	}
	cliout.Printf("\n%s\n", i18n.T("docs.search_hint"))
	return nil
}

// truncate cuts s to at most n display columns, appending an ellipsis when it cut. Rune-wise so a multi-byte character is never split in half.
func truncate(s string, n int) string {
	if n < 8 {
		n = 8
	}
	if style.Width(s) <= n {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && style.Width(string(r))+1 > n {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// runOpen hands the hosted handbook to the OS browser. A launcher that isn't there (headless Linux, no xdg-open) is not an error: the URL is already on screen to copy.
func runOpen(openBrowser func(string) error) error {
	cliout.Printf("%s\n", fmt.Sprintf(i18n.T("docs.opening"), SiteURL))
	if err := openBrowser(SiteURL); err != nil {
		cliout.Println(i18n.T("common.browser_open_failed"))
	}
	return nil
}

// runPicker is the interactive entry point: pick a topic, read it, come back. Esc / Ctrl-C returns ErrPickCancelled, which main and the launcher both read as "up one level".
func runPicker(openBrowser func(string) error) error {
	all, err := topics()
	if err != nil {
		return err
	}
	width := 0
	for _, t := range all {
		if w := style.Width(t.Slug); w > width {
			width = w
		}
	}
	labels := make([]string, 0, len(all)+2)
	for _, t := range all {
		labels = append(labels, style.Bold(t.Slug)+strings.Repeat(" ", width-style.Width(t.Slug))+"  "+inline(t.Title))
	}
	searchIdx := len(labels)
	labels = append(labels, i18n.T("docs.pick_search"))
	openIdx := len(labels)
	labels = append(labels, i18n.T("docs.pick_open"))

	last := 0
	for {
		idx, err := cliprompt.PickWithSelected(i18n.T("docs.pick_topic"), labels, last)
		if err != nil {
			return err
		}
		last = idx
		switch idx {
		case searchIdx:
			err = runSearch(nil)
		case openIdx:
			err = runOpen(openBrowser)
		default:
			printTopic(all[idx])
			err = nil
		}
		// Same rule as the launcher's sub-picker: a failed action prints and the menu re-renders, so one bad search doesn't eject the reader.
		if err != nil && !errors.Is(err, cliprompt.ErrPickCancelled) {
			fmt.Fprintf(os.Stderr, "%s: %s\n", i18n.T("common.error_prefix"), cliout.Sanitize(err.Error()))
		}
		cliout.Println("")
	}
}
