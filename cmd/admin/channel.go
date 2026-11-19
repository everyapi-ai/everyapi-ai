package admin

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/style"
	"github.com/everyapi-ai/everyapi-sdk/api"
)

// adminChannelList pages the operator channel list. --keyword and --model switch to the search endpoint; every other filter is applied server-side on the plain list.
//
//	everyapi admin channel list [--kind K] [--keyword S] [--model M] [--group G] [--status enabled|disabled] [--page P] [--limit N]
func adminChannelList(args []string) error {
	fs := flag.NewFlagSet("admin channel list", flag.ContinueOnError)
	kind := fs.String("kind", "", "channel kind slug (e.g. codex, openai, anthropic)")
	keyword := fs.String("keyword", "", "match channel name, id, or key fragment")
	model := fs.String("model", "", "only channels serving this model")
	group := fs.String("group", "", "routing group id")
	status := fs.String("status", "", "enabled or disabled (default all)")
	page := fs.Int("page", 0, "1-based page")
	limit := fs.Int("limit", 20, "page size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectPositionals(fs); err != nil {
		return err
	}
	switch *status {
	case "", "enabled", "disabled":
	default:
		return fmt.Errorf("--status must be enabled or disabled, got %q", *status)
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, total, err := client.AdminListChannels(cliout.WithCtx(), api.AdminChannelFilter{
		Keyword:  strings.TrimSpace(*keyword),
		Model:    strings.TrimSpace(*model),
		Kind:     strings.TrimSpace(*kind),
		Group:    strings.TrimSpace(*group),
		Status:   *status,
		Page:     *page,
		PageSize: *limit,
	})
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println(i18n.T("admin.channel.no_matches"))
		return nil
	}
	cliout.Printf(i18n.T("admin.common.rows_total")+"\n", len(rows), total)
	printChannelRows(rows)
	return nil
}

// adminChannelShow prints one channel, including its full model list. The backend's detail endpoint never returns the credential.
func adminChannelShow(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin channel show <id>")
	}
	if err := requireExactPositionals(args, 1); err != nil {
		return err
	}
	id, err := parseChannelID(args[0])
	if err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	ch, err := client.AdminGetChannel(cliout.WithCtx(), id)
	if err != nil {
		return classifyErr(err)
	}
	printChannelDetail(ch)
	return nil
}

// adminChannelModels adds and/or removes models on one channel. A pure addition goes through the backend's merge endpoint, which is idempotent and touches no other column; a removal has to replace the list, so it reads the current list first and writes back only id + models.
//
//	everyapi admin channel models <id> [--add m1,m2] [--remove m3] [-y]
func adminChannelModels(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New(i18n.T("admin.channel.models_usage"))
	}
	id, err := parseChannelID(args[0])
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("admin channel models", flag.ContinueOnError)
	add := fs.String("add", "", "comma-separated models to add")
	remove := fs.String("remove", "", "comma-separated models to remove")
	yes := fs.Bool("y", false, "skip confirmation")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if err := rejectPositionals(fs); err != nil {
		return err
	}
	additions, removals := splitModelFlag(*add), splitModelFlag(*remove)
	if len(additions) == 0 && len(removals) == 0 {
		return errors.New(i18n.T("admin.channel.models_usage"))
	}
	for _, m := range additions {
		if containsModel(removals, m) {
			return fmt.Errorf("model %q is in both --add and --remove", m)
		}
	}

	client, err := newClient()
	if err != nil {
		return err
	}
	ch, err := client.AdminGetChannel(cliout.WithCtx(), id)
	if err != nil {
		return classifyErr(err)
	}
	plan := planChannelModels(ch.ModelList(), additions, removals)
	if len(plan.missing) > 0 {
		cliout.Printf(style.Color(i18n.T("admin.channel.models_not_present"), style.ToneYellow)+"\n", id, strings.Join(plan.missing, ", "))
	}
	if len(plan.added) == 0 && len(plan.removed) == 0 {
		cliout.Printf(mutedText("admin.channel.models_nothing")+"\n", id)
		return nil
	}
	if len(plan.result) == 0 {
		return fmt.Errorf(i18n.T("admin.channel.models_remove_all"), id)
	}

	if !*yes {
		if !cliprompt.IsInteractive() {
			return errors.New(i18n.T("admin.channel.models_needs_confirm"))
		}
		ok, err := cliprompt.YesNo(bufio.NewReader(os.Stdin), fmt.Sprintf(i18n.T("admin.channel.models_confirm"), id, sanitize(ch.Name), dashIfEmpty(plan.added), dashIfEmpty(plan.removed)), false)
		if err != nil {
			return err
		}
		if !ok {
			cliout.Println(mutedText("common.canceled"))
			return nil
		}
	}

	if len(plan.removed) == 0 {
		err = client.AdminAddChannelModels(cliout.WithCtx(), []int{id}, plan.added)
	} else {
		_, err = client.AdminSetChannelModels(cliout.WithCtx(), id, plan.result)
	}
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(okText("admin.channel.models_updated")+"\n", id, len(plan.result))
	return nil
}

// channelModelPlan is the effect of an --add/--remove request on a channel's current list: what actually changes, what the list becomes, and which removals named a model the channel does not serve.
type channelModelPlan struct {
	added, removed, missing, result []string
}

// planChannelModels keeps the current order, appends new models at the end (the order the backend's merge uses), and drops removals.
func planChannelModels(current, additions, removals []string) channelModelPlan {
	var plan channelModelPlan
	for _, m := range current {
		if containsModel(removals, m) {
			plan.removed = append(plan.removed, m)
			continue
		}
		plan.result = append(plan.result, m)
	}
	for _, m := range additions {
		if !containsModel(plan.result, m) {
			plan.added = append(plan.added, m)
			plan.result = append(plan.result, m)
		}
	}
	for _, m := range removals {
		if !containsModel(current, m) {
			plan.missing = append(plan.missing, m)
		}
	}
	return plan
}

func splitModelFlag(raw string) []string {
	var out []string
	for _, m := range strings.Split(raw, ",") {
		if m = strings.TrimSpace(m); m != "" && !containsModel(out, m) {
			out = append(out, m)
		}
	}
	return out
}

func containsModel(list []string, model string) bool {
	for _, m := range list {
		if m == model {
			return true
		}
	}
	return false
}

func dashIfEmpty(models []string) string {
	if len(models) == 0 {
		return "-"
	}
	return sanitize(strings.Join(models, ", "))
}

func parseChannelID(raw string) (int, error) {
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid channel id %q", raw)
	}
	return id, nil
}

func channelStatusLabel(status int) string {
	switch status {
	case 1:
		return style.Color(i18n.T("admin.channel.status_enabled"), style.ToneGreen)
	case 2:
		return style.Color(i18n.T("admin.channel.status_manual"), style.ToneGray)
	case 3:
		return style.Color(i18n.T("admin.channel.status_auto"), style.ToneRed)
	case 4:
		return style.Dim(i18n.T("admin.channel.status_retired"))
	default:
		return fmt.Sprintf("status=%d", status)
	}
}

func channelOwnerLabel(ch api.AdminChannel) string {
	if ch.OwnerUserID == 0 {
		return i18n.T("admin.channel.owner_platform")
	}
	if ch.OwnerUsername != "" {
		return "@" + sanitize(ch.OwnerUsername)
	}
	return fmt.Sprintf("#%d", ch.OwnerUserID)
}

// printChannelRows renders the list view. The model list is summarised as a count because one channel can serve dozens; `channel show` prints it in full.
func printChannelRows(rows []api.AdminChannel) {
	cols := []tcol{
		{"ID", true},
		{i18n.T("admin.channel.f_kind"), false},
		{i18n.T("admin.channel.f_status"), false},
		{i18n.T("admin.channel.f_models"), true},
		{i18n.T("admin.channel.f_owner"), false},
	}
	cells := make([][]string, len(rows))
	trailing := make([]string, len(rows))
	for i, ch := range rows {
		cells[i] = []string{fmt.Sprintf("#%d", ch.ID), sanitize(ch.KindSlug), channelStatusLabel(ch.Status), strconv.Itoa(len(ch.ModelList())), channelOwnerLabel(ch)}
		trailing[i] = sanitize(ch.Name)
	}
	printTable(cols, cells, trailing)
}

func printChannelDetail(ch *api.AdminChannel) {
	var d detail
	d.add("admin.channel.f_name", sanitize(ch.Name))
	d.add("admin.channel.f_kind", sanitize(ch.KindSlug))
	d.add("admin.channel.f_status", channelStatusLabel(ch.Status))
	auth := i18n.T("admin.channel.auth_key")
	if ch.OAuthEnabled {
		auth = i18n.T("admin.channel.auth_oauth")
	}
	d.add("admin.channel.f_auth", auth)
	d.add("admin.channel.f_owner", channelOwnerLabel(*ch))
	d.add("admin.channel.f_group", sanitize(ch.Group))
	if ch.Tag != nil {
		d.add("admin.channel.f_tag", sanitize(*ch.Tag))
	}
	if ch.Priority != nil {
		d.add("admin.channel.f_priority", strconv.FormatInt(*ch.Priority, 10))
	}
	if ch.Weight != nil {
		d.add("admin.channel.f_weight", strconv.FormatUint(uint64(*ch.Weight), 10))
	}
	if ch.TestTime > 0 {
		d.add("admin.channel.f_tested", fmt.Sprintf("%s (%dms)", time.Unix(ch.TestTime, 0).Format("2006-01-02 15:04"), ch.ResponseTime))
	}
	models := ch.ModelList()
	d.add("admin.channel.f_models", fmt.Sprintf("%d", len(models)))
	printDetail("admin.channel.detail_title", ch.ID, d)
	for _, m := range models {
		cliout.Printf("    %s\n", sanitize(m))
	}
}
