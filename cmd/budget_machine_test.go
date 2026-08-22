package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/api"
)

type budgetClientStub struct {
	status  api.StatusData
	token   api.Token
	budget  api.TokenBudget
	updated *api.TokenUpdate
}

func (c *budgetClientStub) GetStatus(context.Context) (*api.StatusData, error) { return &c.status, nil }
func (c *budgetClientStub) GetToken(context.Context, int) (*api.Token, error)  { return &c.token, nil }
func (c *budgetClientStub) GetTokenBudget(context.Context, int) (*api.TokenBudget, error) {
	return &c.budget, nil
}
func (c *budgetClientStub) UpdateToken(_ context.Context, request api.TokenUpdate) (*api.Token, error) {
	c.updated = &request
	c.budget.BudgetEnabled = request.BudgetEnabled
	c.budget.DailyBudget = request.DailyBudget
	c.budget.MonthlyBudget = request.MonthlyBudget
	c.budget.AlertThreshold = request.BudgetAlertThreshold
	return &c.token, nil
}

func TestParseBudgetMachineArgsRequiresACompleteBoundedSet(t *testing.T) {
	if got, err := parseBudgetMachineArgs([]string{"--format=json"}); err != nil || got != nil {
		t.Fatalf("read args = %+v, %v", got, err)
	}
	got, err := parseBudgetMachineArgs([]string{
		"--format=json", "--enabled=true", "--daily-usd=5.5",
		"--monthly-usd=100", "--alert-threshold=80",
	})
	if err != nil || !got.Enabled || got.DailyUSD != 5.5 || got.AlertThreshold != 80 {
		t.Fatalf("settings = %+v, %v", got, err)
	}
	for _, args := range [][]string{
		{"--format=json", "--enabled=true"},
		{"--format=json", "--enabled=", "--enabled=true", "--daily-usd=1", "--monthly-usd=1"},
		{"--format=json", "--enabled=true", "--daily-usd=-1", "--monthly-usd=1", "--alert-threshold=80"},
		{"--format=json", "--enabled=true", "--daily-usd=1", "--monthly-usd=1", "--alert-threshold=101"},
	} {
		if _, err := parseBudgetMachineArgs(args); err == nil {
			t.Fatalf("args %v unexpectedly accepted", args)
		}
	}
}

func TestRunBudgetMachineReadsAndUpdatesTheAutoTokenWithoutLosingPolicy(t *testing.T) {
	client := &budgetClientStub{
		status: api.StatusData{QuotaPerUnit: 500_000},
		token:  api.Token{ID: 7, Name: "Auto", Status: 1, Group: "auto", UnlimitedQuota: true, Scopes: "relay", SupplierStrategy: "allow"},
		budget: api.TokenBudget{DailySpent: 250_000, MonthlySpent: 1_000_000},
	}
	var out bytes.Buffer
	err := runBudgetMachine(context.Background(), client, 7, &budgetMachineSettings{
		Enabled: true, DailyUSD: 2, MonthlyUSD: 20, AlertThreshold: 75,
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if client.updated == nil || client.updated.DailyBudget != 1_000_000 ||
		client.updated.MonthlyBudget != 10_000_000 || client.updated.Scopes != "relay" ||
		client.updated.SupplierStrategy != "allow" {
		t.Fatalf("update = %+v", client.updated)
	}
	var report budgetMachineOutput
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Available || !report.Enabled || report.DailySpentUSD != 0.5 || report.DailyBudgetUSD != 2 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunBudgetMachineReportsNoAutoTokenWithoutMutating(t *testing.T) {
	client := &budgetClientStub{status: api.StatusData{QuotaPerUnit: 500_000}}
	var out bytes.Buffer
	if err := runBudgetMachine(context.Background(), client, 0, nil, &out); err != nil {
		t.Fatal(err)
	}
	var report budgetMachineOutput
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Available {
		t.Fatalf("report = %+v, err = %v", report, err)
	}
}

func TestAutoBudgetTokenSkipsSystemManagedAndDisabledKeys(t *testing.T) {
	tokens := []api.TokenSummary{
		{ID: 1, Status: api.TokenStatusDisabled, Group: api.GroupAuto},
		{ID: 2, Status: api.TokenStatusEnabled, Group: api.GroupAuto, SystemManaged: true},
		{ID: 3, Status: api.TokenStatusEnabled, Group: api.GroupAuto},
	}
	if got := autoBudgetTokenID(tokens); got != 3 {
		t.Fatalf("token id = %d", got)
	}
}
