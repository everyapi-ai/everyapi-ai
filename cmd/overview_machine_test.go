package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/api"
)

type fakeOverviewClient struct {
	status       *api.StatusData
	self         *api.SelfData
	selfErr      error
	days         []api.QuotaDay
	logs         []api.LogEntry
	start        int64
	end          int64
	quotaStarted chan struct{}
	logsStarted  chan struct{}
	releaseReads chan struct{}
}

func (f *fakeOverviewClient) GetStatus(context.Context) (*api.StatusData, error) {
	return f.status, nil
}

func (f *fakeOverviewClient) GetSelf(context.Context) (*api.SelfData, error) {
	return f.self, f.selfErr
}

func (f *fakeOverviewClient) UserQuotaDates(_ context.Context, start, end int64) ([]api.QuotaDay, error) {
	f.start, f.end = start, end
	if f.quotaStarted != nil {
		close(f.quotaStarted)
		<-f.releaseReads
	}
	return f.days, nil
}

func (f *fakeOverviewClient) ListUserLogs(context.Context, api.LogFilter) ([]api.LogEntry, int, error) {
	if f.logsStarted != nil {
		close(f.logsStarted)
		<-f.releaseReads
	}
	return f.logs, len(f.logs), nil
}

func TestRunOverviewMachineFetchesIndependentUsageFeedsConcurrently(t *testing.T) {
	client := &fakeOverviewClient{
		status:       &api.StatusData{QuotaPerUnit: 500_000},
		self:         &api.SelfData{},
		quotaStarted: make(chan struct{}),
		logsStarted:  make(chan struct{}),
		releaseReads: make(chan struct{}),
	}
	done := make(chan error, 1)
	go func() {
		done <- runOverviewMachine(context.Background(), client, time.Now(), &bytes.Buffer{})
	}()
	for name, started := range map[string]<-chan struct{}{
		"quota": client.quotaStarted,
		"logs":  client.logsStarted,
	} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("%s request did not start while the other feed was blocked", name)
		}
	}
	close(client.releaseReads)
	if err := <-done; err != nil {
		t.Fatalf("runOverviewMachine: %v", err)
	}
}

func TestRunOverviewMachineKeepsLocalCalendarWindowInsideServerLimit(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.November, 2, 23, 30, 0, 0, location)
	client := &fakeOverviewClient{
		status: &api.StatusData{QuotaPerUnit: 500_000},
		self:   &api.SelfData{},
	}
	var out bytes.Buffer
	if err := runOverviewMachine(context.Background(), client, now, &out); err != nil {
		t.Fatalf("runOverviewMachine: %v", err)
	}
	if span := client.end - client.start; span > 30*24*60*60 {
		t.Fatalf("server query span = %s, exceeds 30-day limit", time.Duration(span)*time.Second)
	}
}

func TestRunOverviewMachineDoesNotCallTransportFailuresExpiredSessions(t *testing.T) {
	client := &fakeOverviewClient{
		status:  &api.StatusData{QuotaPerUnit: 500_000},
		selfErr: errors.New("temporary network failure"),
	}
	err := runOverviewMachine(context.Background(), client, time.Now(), &bytes.Buffer{})
	var statusErr *statusMachineError
	if !errors.As(err, &statusErr) || statusErr.code != "unavailable" {
		t.Fatalf("error = %#v, want unavailable machine status", err)
	}
}

func TestRunOverviewMachineDoesNotCallBusinessFailuresExpiredSessions(t *testing.T) {
	client := &fakeOverviewClient{
		status:  &api.StatusData{QuotaPerUnit: 500_000},
		selfErr: &api.EnvelopeError{Message: "database error"},
	}
	err := runOverviewMachine(context.Background(), client, time.Now(), &bytes.Buffer{})
	var statusErr *statusMachineError
	if !errors.As(err, &statusErr) || statusErr.code != "unavailable" {
		t.Fatalf("error = %#v, want unavailable machine status", err)
	}
}

func TestRunOverviewMachineRecognizesLegacyAuthRejection(t *testing.T) {
	client := &fakeOverviewClient{
		status:  &api.StatusData{QuotaPerUnit: 500_000},
		selfErr: &api.EnvelopeError{Message: "access token invalid"},
	}
	err := runOverviewMachine(context.Background(), client, time.Now(), &bytes.Buffer{})
	var statusErr *statusMachineError
	if !errors.As(err, &statusErr) || statusErr.code != "invalid_credentials" {
		t.Fatalf("error = %#v, want invalid_credentials machine status", err)
	}
}

func TestOverviewMachineKeepsCredentialReadFailuresUnavailable(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	if err := os.MkdirAll(filepath.Join(configHome, "everyapi", "credentials.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	err := OverviewMachine([]string{"--format=json"})
	var statusErr *statusMachineError
	if !errors.As(err, &statusErr) || statusErr.code != "unavailable" {
		t.Fatalf("error = %#v, want unavailable machine status", err)
	}
}

func TestRunOverviewMachineEmitsOnlineSecretFreeAccountUsage(t *testing.T) {
	local := time.FixedZone("UTC+9", 9*60*60)
	now := time.Date(2026, time.August, 13, 0, 30, 0, 0, local)
	client := &fakeOverviewClient{
		status: &api.StatusData{QuotaPerUnit: 500_000},
		self:   &api.SelfData{Quota: 2_500_000},
		days: []api.QuotaDay{
			{ModelName: "gpt-5", CreatedAt: now.Add(-24 * time.Hour).Unix(), Quota: 500_000, Count: 2, TokenUsed: 1200, CacheTokens: 300, CacheWriteTokens: 100},
			{ModelName: "claude-sonnet-4", CreatedAt: now.Unix(), Quota: 250_000, Count: 1, TokenUsed: 800, CacheTokens: 200},
			{ModelName: "gpt-5", CreatedAt: now.Unix(), Quota: 125_000, Count: 1, TokenUsed: 400},
		},
		logs: []api.LogEntry{{
			ID: 99, CreatedAt: now.Unix(), Type: 2, Content: "must-not-leak",
			TokenName: "secret-key-name", ModelName: "gpt-5", Quota: 125_000,
			PromptTokens: 300, CompletionTokens: 100, UseTime: 7,
			IP: "192.0.2.1", RequestID: "req-secret", Other: "private",
		}},
	}

	var out bytes.Buffer
	if err := runOverviewMachine(context.Background(), client, now, &out); err != nil {
		t.Fatalf("runOverviewMachine: %v", err)
	}
	var got overviewMachineOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got.Version != 1 || got.RangeDays != 30 || got.BalanceUSD != 5 {
		t.Fatalf("metadata = %+v", got)
	}
	if got.Totals.SpendUSD != 1.75 || got.Totals.Requests != 4 || got.Totals.Tokens != 3000 {
		t.Fatalf("totals = %+v", got.Totals)
	}
	if len(got.Trend) != 30 || got.Trend[28].SpendUSD != 1 || got.Trend[29].SpendUSD != 0.75 || got.Trend[28].Tokens != 1600 {
		t.Fatalf("trend = %+v", got.Trend)
	}
	if len(got.TopModels) != 2 || got.TopModels[0].Name != "gpt-5" || got.TopModels[0].SpendUSD != 1.25 {
		t.Fatalf("top models = %+v", got.TopModels)
	}
	if len(got.Recent) != 1 || got.Recent[0].Model != "gpt-5" || got.Recent[0].LatencyMS != 7000 {
		t.Fatalf("recent = %+v", got.Recent)
	}
	encoded := out.String()
	for _, secret := range []string{"must-not-leak", "secret-key-name", "192.0.2.1", "req-secret", "private"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("overview output leaked %q: %s", secret, encoded)
		}
	}
}
