package edge

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
)

// Mode is the inferred (or operator-overridden) hardware/OS profile. It picks which docker-compose template variant to render and which ollama image tag to pull. Values are lowercase strings so they round- trip through the --mode flag without case wrangling.
type Mode string

const (
	ModeAuto   Mode = "auto"   // detect at start time
	ModeNVIDIA Mode = "nvidia" // CUDA / NVIDIA Container Toolkit
	ModeROCm   Mode = "rocm"   // AMD ROCm 5.7+
	ModeMacOS  Mode = "macos"  // Apple Silicon, native ollama (host.docker.internal)
	ModeCPU    Mode = "cpu"    // fallback; chat throughput too low for prod
)

// detectMode probes the host for a usable GPU + OS pairing. Returns the most specific mode first (nvidia > rocm > macos > cpu). Caller can override via --mode if the detection is wrong (e.g. a CUDA- capable machine without nvidia-container-toolkit installed yet).
func detectMode() Mode {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return ModeMacOS
	}
	if hasBinary("nvidia-smi") && nvidiaQueryOK() {
		return ModeNVIDIA
	}
	if hasBinary("rocminfo") {
		return ModeROCm
	}
	return ModeCPU
}

// nvidiaQueryOK runs `nvidia-smi -L` and returns true if the binary reports at least one GPU. nvidia-smi being on PATH isn't enough — machines with WSL2 + Windows driver shim leave the binary present but failing. The -L flag is the cheapest sanity query.
func nvidiaQueryOK() bool {
	// Bound the probe: a wedged driver (WSL2 shim, hung GPU) can make nvidia-smi block indefinitely, which would hang `edge start`. On timeout we treat it as "no usable GPU" and fall through to rocm/cpu.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nvidia-smi", "-L")
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return false
	}
	return true
}

// hasBinary checks PATH lookup without invoking the program.
func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// parseMode converts a user-supplied --mode flag to a Mode, treating the empty string and "auto" the same. Returns an error for unknown values so a typo doesn't silently fall back to CPU.
func parseMode(s string) (Mode, error) {
	switch s {
	case "", "auto":
		return ModeAuto, nil
	case "nvidia":
		return ModeNVIDIA, nil
	case "rocm":
		return ModeROCm, nil
	case "macos":
		return ModeMacOS, nil
	case "cpu":
		return ModeCPU, nil
	default:
		return "", errors.New(i18n.T("edge.detect.invalid_mode"))
	}
}

// resolveMode collapses ModeAuto into a concrete mode via detection. Other modes pass through unchanged — operator override always wins.
func resolveMode(m Mode) Mode {
	if m == "" || m == ModeAuto {
		return detectMode()
	}
	return m
}

func memoryGBForMode(mode Mode) int {
	if mode != ModeMacOS {
		return 0
	}
	output, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	totalBytes, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0
	}
	return unifiedMemoryGB(totalBytes)
}

func unifiedMemoryGB(totalBytes int64) int {
	if totalBytes <= 0 {
		return 0
	}
	const gib = int64(1024 * 1024 * 1024)
	return int((totalBytes + gib - 1) / gib)
}
