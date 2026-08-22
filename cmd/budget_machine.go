package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

type budgetMachineSettings struct {
	Enabled        bool
	DailyUSD       float64
	MonthlyUSD     float64
	AlertThreshold int
}

type budgetMachineOutput struct {
	Version          int     `json:"version"`
	Available        bool    `json:"available"`
	Enabled          bool    `json:"enabled"`
	DailySpentUSD    float64 `json:"daily_spent_usd"`
	DailyBudgetUSD   float64 `json:"daily_budget_usd"`
	MonthlySpentUSD  float64 `json:"monthly_spent_usd"`
	MonthlyBudgetUSD float64 `json:"monthly_budget_usd"`
	AlertThreshold   int     `json:"alert_threshold"`
}

type budgetMachineClient interface {
	GetStatus(context.Context) (*api.StatusData, error)
	GetToken(context.Context, int) (*api.Token, error)
	GetTokenBudget(context.Context, int) (*api.TokenBudget, error)
	UpdateToken(context.Context, api.TokenUpdate) (*api.Token, error)
}

func parseBudgetMachineArgs(args []string) (*budgetMachineSettings, error) {
	if len(args) == 1 && args[0] == "--format=json" {
		return nil, nil
	}
	if len(args) != 5 || args[0] != "--format=json" {
		return nil, errors.New("budget accepts --format=json and a complete settings set")
	}
	values := map[string]string{}
	for _, arg := range args[1:] {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			return nil, errors.New("invalid budget setting")
		}
		if _, duplicate := values[key]; duplicate {
			return nil, errors.New("duplicate budget setting")
		}
		values[key] = value
	}
	if len(values) != 4 {
		return nil, errors.New("incomplete budget settings")
	}
	enabled, err := strconv.ParseBool(values["--enabled"])
	if err != nil {
		return nil, errors.New("invalid enabled value")
	}
	daily, err := strconv.ParseFloat(values["--daily-usd"], 64)
	if err != nil || !validBudgetUSD(daily) {
		return nil, errors.New("invalid daily budget")
	}
	monthly, err := strconv.ParseFloat(values["--monthly-usd"], 64)
	if err != nil || !validBudgetUSD(monthly) {
		return nil, errors.New("invalid monthly budget")
	}
	threshold, err := strconv.Atoi(values["--alert-threshold"])
	if err != nil || threshold < 0 || threshold > 100 {
		return nil, errors.New("invalid alert threshold")
	}
	return &budgetMachineSettings{Enabled: enabled, DailyUSD: daily, MonthlyUSD: monthly, AlertThreshold: threshold}, nil
}

func validBudgetUSD(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1_000_000_000
}

func quotaFromUSD(value, perUnit float64) (int, error) {
	quota := math.Round(value * perUnit)
	if quota < 0 || quota > float64(math.MaxInt) {
		return 0, errors.New("budget is outside the gateway quota range")
	}
	return int(quota), nil
}

func autoBudgetTokenID(tokens []api.TokenSummary) int {
	for _, token := range tokens {
		if token.Status == api.TokenStatusEnabled && token.Group == api.GroupAuto && !token.SystemManaged {
			return token.ID
		}
	}
	return 0
}

func BudgetMachine(args []string) error {
	settings, err := parseBudgetMachineArgs(args)
	if err != nil {
		return machineStatusError("invalid_request", err)
	}
	unlock, err := acquireCredentialLock()
	if err != nil {
		return machineStatusError("unavailable", err)
	}
	creds, err := config.Load()
	unlock()
	if err != nil {
		return credentialLoadMachineError(err)
	}
	if creds.OAuthClientID != "" {
		return machineStatusError("unsupported_session", errors.New("budgets require an account session"))
	}
	client := api.ForCredentials(creds)
	tokens, err := client.ListEnabledTokens(cliout.WithCtx())
	if err != nil {
		return accountMachineError(err)
	}
	tokenID := autoBudgetTokenID(tokens)
	if tokenID == 0 && settings != nil {
		if _, err = api.SelectAutoRelayKey(cliout.WithCtx(), creds); err != nil {
			return machineStatusError("unavailable", err)
		}
		tokenID = creds.RelayKeyTokenID
	}
	return runBudgetMachine(cliout.WithCtx(), client, tokenID, settings, cliout.Out)
}

func runBudgetMachine(ctx context.Context, client budgetMachineClient, tokenID int, settings *budgetMachineSettings, out io.Writer) error {
	status, err := client.GetStatus(ctx)
	if err != nil {
		return machineStatusError("unavailable", err)
	}
	perUnit := status.QuotaPerUnit
	if perUnit <= 0 || math.IsNaN(perUnit) || math.IsInf(perUnit, 0) {
		perUnit = 1
	}
	if tokenID <= 0 {
		return json.NewEncoder(out).Encode(budgetMachineOutput{Version: 1})
	}
	if settings != nil {
		current, err := client.GetToken(ctx, tokenID)
		if err != nil {
			return accountMachineError(err)
		}
		daily, err := quotaFromUSD(settings.DailyUSD, perUnit)
		if err != nil {
			return machineStatusError("invalid_request", err)
		}
		monthly, err := quotaFromUSD(settings.MonthlyUSD, perUnit)
		if err != nil {
			return machineStatusError("invalid_request", err)
		}
		request := current.UpdateRequest()
		request.BudgetEnabled = settings.Enabled
		request.DailyBudget = daily
		request.MonthlyBudget = monthly
		request.BudgetAlertThreshold = settings.AlertThreshold
		if _, err = client.UpdateToken(ctx, request); err != nil {
			return accountMachineError(err)
		}
	}
	budget, err := client.GetTokenBudget(ctx, tokenID)
	if err != nil {
		return accountMachineError(err)
	}
	report := budgetMachineOutput{
		Version: 1, Available: true, Enabled: budget.BudgetEnabled,
		DailySpentUSD:    float64(budget.DailySpent) / perUnit,
		DailyBudgetUSD:   float64(budget.DailyBudget) / perUnit,
		MonthlySpentUSD:  float64(budget.MonthlySpent) / perUnit,
		MonthlyBudgetUSD: float64(budget.MonthlyBudget) / perUnit,
		AlertThreshold:   budget.AlertThreshold,
	}
	if err := json.NewEncoder(out).Encode(report); err != nil {
		return machineStatusError("unavailable", fmt.Errorf("encode budget: %w", err))
	}
	return nil
}
