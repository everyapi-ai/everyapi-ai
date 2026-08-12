package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const diagnosticChatInputLimit = 32 * 1024

type diagnosticChatMachineOutput struct {
	Version        int    `json:"version"`
	Message        string `json:"message"`
	Model          string `json:"model"`
	RemainingToday int    `json:"remaining_today"`
}

type diagnosticChatStreamClient interface {
	DiagnosticChatStream(context.Context, api.DiagnosticChatRequest, func(string) error) (api.DiagnosticChatResult, error)
}

type diagnosticChatStreamEvent struct {
	Version        int    `json:"version"`
	Type           string `json:"type"`
	Delta          string `json:"delta,omitempty"`
	Model          string `json:"model,omitempty"`
	RemainingToday *int   `json:"remaining_today,omitempty"`
}

type diagnosticChatMachineError struct {
	code string
	err  error
}

func (e *diagnosticChatMachineError) Error() string {
	return fmt.Sprintf("EVERYAPI_DIAGNOSTIC_CHAT_ERROR:%s: %v", e.code, e.err)
}

func (e *diagnosticChatMachineError) Unwrap() error { return e.err }

func decodeDiagnosticChatInput(reader io.Reader) (api.DiagnosticChatRequest, error) {
	line, err := bufio.NewReader(io.LimitReader(reader, diagnosticChatInputLimit+2)).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return api.DiagnosticChatRequest{}, err
	}
	if len(line) == 0 || len(line) > diagnosticChatInputLimit+1 {
		return api.DiagnosticChatRequest{}, errors.New("request too large or empty")
	}
	var request api.DiagnosticChatRequest
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return api.DiagnosticChatRequest{}, err
	}
	if request.TargetID == "" || len(request.Messages) == 0 {
		return api.DiagnosticChatRequest{}, errors.New("target and messages are required")
	}
	for _, message := range request.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			return api.DiagnosticChatRequest{}, errors.New("invalid message role")
		}
	}
	return request, nil
}

func DiagnosticChatMachine(args []string) error {
	fs := flag.NewFlagSet("auth diagnostic-chat", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "json", "output format")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || (*format != "json" && *format != "ndjson") {
		return &diagnosticChatMachineError{code: "invalid_request", err: errors.New("invalid arguments")}
	}
	request, err := decodeDiagnosticChatInput(os.Stdin)
	if err != nil {
		return &diagnosticChatMachineError{code: "invalid_request", err: err}
	}
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return &diagnosticChatMachineError{code: "not_logged_in", err: err}
	}
	if err != nil {
		return &diagnosticChatMachineError{code: "unavailable", err: err}
	}
	client := api.ForCredentials(creds)
	if *format == "ndjson" {
		return mapDiagnosticChatError(writeDiagnosticChatStream(cliout.WithCtx(), cliout.Out, client, request))
	}
	result, err := client.DiagnosticChat(cliout.WithCtx(), request)
	if err != nil {
		return mapDiagnosticChatError(err)
	}
	return json.NewEncoder(cliout.Out).Encode(diagnosticChatMachineOutput{
		Version: 1, Message: result.Message, Model: result.Model, RemainingToday: result.RemainingToday,
	})
}

func writeDiagnosticChatStream(ctx context.Context, out io.Writer, client diagnosticChatStreamClient, request api.DiagnosticChatRequest) error {
	encoder := json.NewEncoder(out)
	emitEvent := func(event diagnosticChatStreamEvent) error {
		if err := encoder.Encode(event); err != nil {
			return err
		}
		if flusher, ok := out.(interface{ Flush() error }); ok {
			return flusher.Flush()
		}
		return nil
	}
	result, err := client.DiagnosticChatStream(ctx, request, func(delta string) error {
		return emitEvent(diagnosticChatStreamEvent{Version: 2, Type: "delta", Delta: delta})
	})
	if err != nil {
		return err
	}
	return emitEvent(diagnosticChatStreamEvent{Version: 2, Type: "done", Model: result.Model, RemainingToday: &result.RemainingToday})
}

func mapDiagnosticChatError(err error) error {
	code := "unavailable"
	var streamError *api.DiagnosticChatStreamError
	if errors.As(err, &streamError) && streamError.Code == "daily_limit_reached" {
		code = "daily_limit_reached"
	}
	var apiError *api.APIError
	if errors.As(err, &apiError) {
		switch apiError.StatusCode {
		case http.StatusUnauthorized:
			code = "not_logged_in"
		case http.StatusTooManyRequests:
			code = "daily_limit_reached"
		}
	}
	return &diagnosticChatMachineError{code: code, err: err}
}
