package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const overviewMachineProtocolVersion = 1
const overviewRangeDays = 30
const overviewRecentLimit = 8
const overviewTopModelLimit = 5
const overviewServerMaxSpan = 30 * 24 * time.Hour

type overviewClient interface {
	GetStatus(context.Context) (*api.StatusData, error)
	GetSelf(context.Context) (*api.SelfData, error)
	UserQuotaDates(context.Context, int64, int64) ([]api.QuotaDay, error)
	ListUserLogs(context.Context, api.LogFilter) ([]api.LogEntry, int, error)
}

type overviewMachineOutput struct {
	Version     int                    `json:"version"`
	RangeDays   int                    `json:"range_days"`
	GeneratedAt int64                  `json:"generated_at"`
	BalanceUSD  float64                `json:"balance_usd"`
	Totals      overviewMachineTotals  `json:"totals"`
	Trend       []overviewMachineDay   `json:"trend"`
	TopModels   []overviewMachineModel `json:"top_models"`
	Recent      []overviewMachineLog   `json:"recent"`
}

type overviewMachineTotals struct {
	SpendUSD float64 `json:"spend_usd"`
	Requests int64   `json:"requests"`
	Tokens   int64   `json:"tokens"`
}

type overviewMachineDay struct {
	Date     string  `json:"date"`
	SpendUSD float64 `json:"spend_usd"`
	Requests int64   `json:"requests"`
	Tokens   int64   `json:"tokens"`
}

type overviewMachineModel struct {
	Name     string  `json:"name"`
	SpendUSD float64 `json:"spend_usd"`
	Requests int64   `json:"requests"`
	Tokens   int64   `json:"tokens"`
}

type overviewMachineLog struct {
	CreatedAt        int64   `json:"created_at"`
	Model            string  `json:"model"`
	SpendUSD         float64 `json:"spend_usd"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	LatencyMS        int64   `json:"latency_ms"`
}

func OverviewMachine(args []string) error {
	if len(args) != 1 || args[0] != "--format=json" {
		return machineStatusError("invalid_request", errors.New("overview accepts only --format=json"))
	}
	unlock, err := acquireCredentialLock()
	if err != nil {
		return machineStatusError("unavailable", fmt.Errorf("lock credential cache: %w", err))
	}
	creds, err := config.Load()
	// The loaded value owns all data needed below. Release the file lock before network I/O so the desktop's concurrent balance refresh does not wait for all four overview requests to finish.
	unlock()
	if err != nil {
		return machineStatusError("invalid_credentials", err)
	}
	if creds.OAuthClientID != "" {
		return machineStatusError("unsupported_session", errors.New("online usage requires an account session"))
	}
	return runOverviewMachine(cliout.WithCtx(), api.ForCredentials(creds), time.Now(), cliout.Out)
}

func runOverviewMachine(ctx context.Context, client overviewClient, now time.Time, out io.Writer) error {
	status, err := client.GetStatus(ctx)
	if err != nil {
		return machineStatusError("unavailable", err)
	}
	self, err := client.GetSelf(ctx)
	if err != nil {
		var envelopeError *api.EnvelopeError
		if api.IsUnauthorized(err) || errors.As(err, &envelopeError) {
			return machineStatusError("invalid_credentials", err)
		}
		return machineStatusError("unavailable", err)
	}
	perUnit := status.QuotaPerUnit
	if perUnit <= 0 {
		perUnit = 1
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start := today.AddDate(0, 0, -(overviewRangeDays - 1))
	queryStart := start
	if minimum := now.Add(-overviewServerMaxSpan); queryStart.Before(minimum) {
		queryStart = minimum
	}
	type usageResult struct {
		days []api.QuotaDay
		err  error
	}
	type logsResult struct {
		logs []api.LogEntry
		err  error
	}
	usageDone := make(chan usageResult, 1)
	logsDone := make(chan logsResult, 1)
	go func() {
		days, err := client.UserQuotaDates(ctx, queryStart.Unix(), now.Unix())
		usageDone <- usageResult{days: days, err: err}
	}()
	go func() {
		logs, _, err := client.ListUserLogs(ctx, api.LogFilter{
			Type: 2, Start: queryStart.Unix(), End: now.Unix(), Page: 1, PageSize: overviewRecentLimit,
		})
		logsDone <- logsResult{logs: logs, err: err}
	}()
	usage := <-usageDone
	logsResponse := <-logsDone
	if usage.err != nil {
		return machineStatusError("unavailable", usage.err)
	}
	if logsResponse.err != nil {
		return machineStatusError("unavailable", logsResponse.err)
	}
	days, logs := usage.days, logsResponse.logs

	report := overviewMachineOutput{
		Version: overviewMachineProtocolVersion, RangeDays: overviewRangeDays,
		GeneratedAt: now.Unix(), BalanceUSD: float64(self.Quota) / perUnit,
		Trend:     make([]overviewMachineDay, overviewRangeDays),
		TopModels: []overviewMachineModel{}, Recent: []overviewMachineLog{},
	}
	byDate := make(map[string]*overviewMachineDay, overviewRangeDays)
	for i := range overviewRangeDays {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		report.Trend[i].Date = date
		byDate[date] = &report.Trend[i]
	}
	byModel := map[string]*overviewMachineModel{}
	for _, row := range days {
		date := time.Unix(row.CreatedAt, 0).In(now.Location()).Format("2006-01-02")
		day := byDate[date]
		if day == nil {
			continue
		}
		spend := float64(row.Quota) / perUnit
		requests := int64(row.Count)
		tokens := int64(row.TokenUsed) + int64(row.CacheTokens) + int64(row.CacheWriteTokens)
		day.SpendUSD += spend
		day.Requests += requests
		day.Tokens += tokens
		report.Totals.SpendUSD += spend
		report.Totals.Requests += requests
		report.Totals.Tokens += tokens
		name := strings.TrimSpace(cliout.Sanitize(row.ModelName))
		if name == "" {
			name = "unknown"
		}
		model := byModel[name]
		if model == nil {
			model = &overviewMachineModel{Name: name}
			byModel[name] = model
		}
		model.SpendUSD += spend
		model.Requests += requests
		model.Tokens += tokens
	}
	for _, model := range byModel {
		report.TopModels = append(report.TopModels, *model)
	}
	sort.Slice(report.TopModels, func(i, j int) bool {
		if report.TopModels[i].SpendUSD == report.TopModels[j].SpendUSD {
			return report.TopModels[i].Name < report.TopModels[j].Name
		}
		return report.TopModels[i].SpendUSD > report.TopModels[j].SpendUSD
	})
	if len(report.TopModels) > overviewTopModelLimit {
		report.TopModels = report.TopModels[:overviewTopModelLimit]
	}
	if len(logs) > overviewRecentLimit {
		logs = logs[:overviewRecentLimit]
	}
	for _, row := range logs {
		model := strings.TrimSpace(cliout.Sanitize(row.ModelName))
		if model == "" {
			model = "unknown"
		}
		report.Recent = append(report.Recent, overviewMachineLog{
			CreatedAt: row.CreatedAt, Model: model, SpendUSD: float64(row.Quota) / perUnit,
			PromptTokens: int64(row.PromptTokens), CompletionTokens: int64(row.CompletionTokens),
			LatencyMS: int64(row.UseTime) * 1_000,
		})
	}
	if err := json.NewEncoder(out).Encode(report); err != nil {
		return machineStatusError("unavailable", err)
	}
	return nil
}
