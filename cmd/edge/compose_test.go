package edge

import (
	"strings"
	"testing"
)

func TestRenderComposeNVIDIA(t *testing.T) {
	out, err := renderCompose(composeData{
		NodeID:            42,
		NodeName:          "rtx-4090-tokyo",
		Mode:              ModeNVIDIA,
		Gateway:           "wss://api.everyapi.ai",
		RegistrationToken: "rt_abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"container_name: everyapi-edge-42-ollama",
		"container_name: everyapi-edge-42-agent",
		"driver: nvidia",
		"capabilities: [gpu]",
		`image: "ghcr.io/everyapi-ai/everyapi-edge:latest"`,
		`image: "ollama/ollama:latest"`,
		`EVERYAPI_GATEWAY: "wss://api.everyapi.ai"`,
		`EVERYAPI_NODE_ID: "42"`,
		`EVERYAPI_REGISTRATION_TOKEN: "rt_abc123"`,
		`EVERYAPI_NODE_NAME: "rtx-4090-tokyo"`,
		"OLLAMA_URL: http://ollama:11434",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in rendered compose:\n%s", want, s)
		}
	}
	// rocm-specific bits MUST NOT appear in nvidia output.
	for _, bad := range []string{"ollama/ollama:rocm", "/dev/kfd"} {
		if strings.Contains(s, bad) {
			t.Errorf("unexpected %q in nvidia render", bad)
		}
	}
}

func TestRenderComposeROCm(t *testing.T) {
	out, err := renderCompose(composeData{
		NodeID:            7,
		Mode:              ModeROCm,
		Gateway:           "wss://api.everyapi.ai",
		RegistrationToken: "rt_x",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`image: "ollama/ollama:rocm"`,
		"/dev/kfd",
		"/dev/dri",
		"group_add",
		"- video",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in rocm render", want)
		}
	}
	if strings.Contains(s, "driver: nvidia") {
		t.Errorf("nvidia block leaked into rocm render")
	}
}

func TestRenderComposeMacOS(t *testing.T) {
	out, err := renderCompose(composeData{
		NodeID:            3,
		Mode:              ModeMacOS,
		MemoryGB:          48,
		Gateway:           "wss://api.everyapi.ai",
		RegistrationToken: "rt_m",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"OLLAMA_URL: http://host.docker.internal:11434",
		"host.docker.internal:host-gateway",
		"container_name: everyapi-edge-3-agent",
		`EVERYAPI_GPU_MODEL: "Apple Silicon"`,
		`EVERYAPI_VRAM_GB: "48"`,
		`EVERYAPI_PLATFORM: "darwin/arm64"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in macos render", want)
		}
	}
	// No ollama sidecar in compose for macOS.
	if strings.Contains(s, "container_name: everyapi-edge-3-ollama") {
		t.Errorf("macOS render unexpectedly includes ollama sidecar")
	}
}

func TestUnifiedMemoryGBRoundsUpHostBytes(t *testing.T) {
	const bytes = int64(48*1024*1024*1024 - 1)
	if got := unifiedMemoryGB(bytes); got != 48 {
		t.Fatalf("unifiedMemoryGB(%d) = %d, want 48", bytes, got)
	}
	if got := unifiedMemoryGB(0); got != 0 {
		t.Fatalf("unifiedMemoryGB(0) = %d, want 0", got)
	}
}

func TestRenderComposeCPU(t *testing.T) {
	out, err := renderCompose(composeData{
		NodeID:            1,
		Mode:              ModeCPU,
		Gateway:           "wss://api.everyapi.ai",
		RegistrationToken: "rt_c",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// CPU mode = ollama sidecar but NO GPU passthrough block.
	if !strings.Contains(s, `image: "ollama/ollama:latest"`) {
		t.Errorf("cpu mode should still spin up ollama sidecar")
	}
	for _, bad := range []string{"driver: nvidia", "/dev/kfd", "host.docker.internal"} {
		if strings.Contains(s, bad) {
			t.Errorf("unexpected %q in cpu render", bad)
		}
	}
}

// TestRenderComposeYAMLInjectionResistant ensures user/server-controlled
// strings (NodeName, RegistrationToken) get quoted so a stray `:`,
// `#`, or newline can't desync the YAML or smuggle keys. This is the
// reason renderCompose() runs every env value through strconv.Quote.
func TestRenderComposeYAMLInjectionResistant(t *testing.T) {
	out, err := renderCompose(composeData{
		NodeID: 1,
		NodeName: `evil: # hi
extra_key: pwn`,
		Mode:              ModeNVIDIA,
		Gateway:           "wss://api.everyapi.ai",
		RegistrationToken: `rt"with:weird\stuff`,
		// Operator-supplied image tags must be quoted too, or a newline
		// in --agent-image / --ollama-image injects a service key.
		AgentImage:  "ghcr.io/x/y:latest\n    privileged: true",
		OllamaImage: "ollama/ollama:latest\n    privileged: true",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// Quoted form must escape both " and \ — strconv.Quote handles
	// this. The dangerous standalone "extra_key: pwn" line MUST NOT
	// appear as its own YAML key.
	if strings.Contains(s, "\nextra_key:") {
		t.Errorf("node name newline-injection leaked into YAML:\n%s", s)
	}
	// The image-tag newline must not break out into a standalone
	// `privileged: true` line on either service.
	if strings.Contains(s, "\n    privileged:") {
		t.Errorf("image-tag newline-injection leaked into YAML:\n%s", s)
	}
	for _, want := range []string{
		`EVERYAPI_NODE_NAME: "evil: # hi\nextra_key: pwn"`,
		`EVERYAPI_REGISTRATION_TOKEN: "rt\"with:weird\\stuff"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing escaped form %q in:\n%s", want, s)
		}
	}
}

// TestRenderComposeDollarInterpolationResistant ensures a `$` in a baked
// scalar is doubled to `$$` so Docker Compose does NOT substitute it from
// the operator's shell env at `up`/`pull` time (leaking host state, or
// silently emptying the value). YAML double-quoting alone does not stop
// Compose interpolation — only `$$` does.
func TestRenderComposeDollarInterpolationResistant(t *testing.T) {
	out, err := renderCompose(composeData{
		NodeID:            1,
		NodeName:          "gpu-${HOME}-a$b",
		Mode:              ModeNVIDIA,
		Gateway:           "wss://api.everyapi.ai",
		RegistrationToken: "tok$ign",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// Every `$` must be doubled to `$$` so Compose does not interpolate.
	// The exact escaped forms below are unambiguous — a single-`$` leak
	// would render `gpu-${HOME}` / `tok$ign` and fail these.
	for _, want := range []string{
		`EVERYAPI_NODE_NAME: "gpu-$${HOME}-a$$b"`,
		`EVERYAPI_REGISTRATION_TOKEN: "tok$$ign"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing $$-escaped form %q in:\n%s", want, s)
		}
	}
}

func TestParseMode(t *testing.T) {
	cases := map[string]Mode{
		"":       ModeAuto,
		"auto":   ModeAuto,
		"nvidia": ModeNVIDIA,
		"rocm":   ModeROCm,
		"macos":  ModeMacOS,
		"cpu":    ModeCPU,
	}
	for in, want := range cases {
		got, err := parseMode(in)
		if err != nil {
			t.Errorf("parseMode(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseMode(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := parseMode("gpu"); err == nil {
		t.Error("parseMode('gpu') should reject — only the 5 known modes")
	}
}

func TestGatewayURLFromAPIBase(t *testing.T) {
	cases := map[string]string{
		"https://api.everyapi.ai":     "wss://api.everyapi.ai",
		"http://localhost:8787":       "ws://localhost:8787",
		"https://staging.example.com": "wss://staging.example.com",
		"":                            "",
	}
	for in, want := range cases {
		if got := gatewayURLFromAPIBase(in); got != want {
			t.Errorf("gatewayURLFromAPIBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderComposeWorkloads(t *testing.T) {
	// Declared workloads render as a comma-joined EVERYAPI_WORKLOADS
	// env in both the sidecar and macOS (host-native ollama) branches;
	// an empty declaration omits the line entirely so older agents'
	// compose files stay byte-identical.
	withWl, err := renderCompose(composeData{
		NodeID:            42,
		Mode:              ModeNVIDIA,
		Gateway:           "wss://api.everyapi.ai",
		RegistrationToken: "rt_x",
		Workloads:         []string{"coding", "image"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(withWl), `EVERYAPI_WORKLOADS: "coding,image"`) {
		t.Errorf("missing EVERYAPI_WORKLOADS in nvidia render:\n%s", withWl)
	}

	macWl, err := renderCompose(composeData{
		NodeID:            43,
		Mode:              ModeMacOS,
		Gateway:           "wss://api.everyapi.ai",
		RegistrationToken: "rt_x",
		Workloads:         []string{"chat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(macWl), `EVERYAPI_WORKLOADS: "chat"`) {
		t.Errorf("missing EVERYAPI_WORKLOADS in macos render:\n%s", macWl)
	}

	without, err := renderCompose(composeData{
		NodeID:            44,
		Mode:              ModeNVIDIA,
		Gateway:           "wss://api.everyapi.ai",
		RegistrationToken: "rt_x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(without), "EVERYAPI_WORKLOADS") {
		t.Errorf("EVERYAPI_WORKLOADS must be omitted when no declaration exists:\n%s", without)
	}
}

func TestParseWorkloadsFlag(t *testing.T) {
	got, err := parseWorkloadsFlag(" Coding, image ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(got, ",") != "coding,image" {
		t.Errorf("got %v, want [coding image]", got)
	}

	if got, err := parseWorkloadsFlag(""); err != nil || got != nil {
		t.Errorf("empty flag should return nil, nil; got %v, %v", got, err)
	}

	if _, err := parseWorkloadsFlag("chat,mining"); err == nil {
		t.Error("unknown value should error")
	} else if !strings.Contains(err.Error(), "mining") {
		t.Errorf("error should name the offending value, got: %v", err)
	}
}
