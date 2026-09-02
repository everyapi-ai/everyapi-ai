package workspace

import (
	"encoding/base64"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStripHTML(t *testing.T) {
	got := stripHTML(`<html><title>x</title><body>Hello <b>world</b> &amp; all</body></html>`)
	if got != "x Hello world &amp; all" {
		t.Fatalf("stripHTML() = %q", got)
	}
}

func TestParseWorktreesAndSelectCurrent(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "EveryAPI Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-qm", "init")
	list, err := parseWorktrees(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !samePath(list[0].Path, dir) || list[0].Dirty {
		t.Fatalf("worktrees = %#v", list)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err = parseWorktrees(dir)
	if err != nil || len(list) != 1 || !list[0].Dirty {
		t.Fatalf("dirty worktrees = %#v, err=%v", list, err)
	}
}

func TestSafeRepoPathCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	got := safeRepoPath(root, "../../outside.txt")
	if !samePath(filepath.Dir(got), root) {
		t.Fatalf("safeRepoPath escaped root: %s", got)
	}
}

func TestSafeRepoPathCannotEscapeThroughSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := safeRepoPath(root, filepath.Join("linked", "secret.txt"))
	if filepath.Base(got) != "secret.txt" || !isWithin(filepath.Dir(got), canonicalPath(root)) {
		t.Fatalf("safeRepoPath escaped through symlink: got %s, root %s, outside %s", got, root, outside)
	}
}

func TestIndexValueAcceptsZero(t *testing.T) {
	if got := indexValue([]string{"--index", "0"}, "index", -1); got != 0 {
		t.Fatalf("indexValue() = %d, want 0", got)
	}
}

func TestBrowserModelActions(t *testing.T) {
	state := browserState{URL: "https://example.test/path", Title: "Example", Text: "Hello world", HTML: `<a href="/next">Next page</a>`}
	value, err := evaluatePage(&state, "document.querySelector('Next page')")
	if err != nil || value != "Next page" {
		t.Fatalf("evaluatePage() = %#v, %v", value, err)
	}
	href, label := findLink(state.HTML, "Next page")
	if href != "/next" || label != "Next page" {
		t.Fatalf("findLink() = %q, %q", href, label)
	}
	if _, err := evaluatePage(&state, "document.cookie"); err == nil {
		t.Fatal("unsupported expression should return an error")
	}
}

func TestSplitCommandPreservesQuotedArguments(t *testing.T) {
	parts, err := splitCommand(`fill --element @e1 --value "hello world"`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"fill", "--element", "@e1", "--value", "hello world"}
	if strings.Join(parts, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("splitCommand() = %#v, want %#v", parts, want)
	}
	if _, err := splitCommand(`goto --url "https://example.test`); err == nil {
		t.Fatal("unterminated quote should be rejected")
	}
}

func TestTerminalTextDropsPositionalTerminalName(t *testing.T) {
	if got := terminalText([]string{"term-1", "hello", "world"}); got != "hello world" {
		t.Fatalf("terminalText() = %q, want %q", got, "hello world")
	}
	if got := terminalText([]string{"--terminal", "term-1", "hello", "world"}); got != "hello world" {
		t.Fatalf("terminalText() with flag = %q, want %q", got, "hello world")
	}
}

func TestTerminalWaitChecksImmediatelyAtZeroTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tmux terminal backend is unavailable on Windows")
	}
	binDir := t.TempDir()
	tmuxPath := filepath.Join(binDir, "tmux")
	if err := os.WriteFile(tmuxPath, []byte("#!/bin/sh\nif [ \"$1\" = \"has-session\" ]; then exit 1; fi\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	stateDir := t.TempDir()
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", stateDir)
	started := time.Now()
	value, err := terminal([]string{"wait", "--terminal", "missing", "--timeout", "0"})
	if err != nil {
		t.Fatalf("terminal wait: %v", err)
	}
	result, ok := value.(map[string]any)
	if !ok || result["exited"] != true || result["timedOut"] == true {
		t.Fatalf("terminal wait result = %#v, want immediate exited=true", value)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("zero-timeout wait took %s", elapsed)
	}
}

func TestBrowserClickUpdatesHistory(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", stateDir)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/next" {
			_, _ = w.Write([]byte("<title>Next</title><p>next</p>"))
			return
		}
		_, _ = w.Write([]byte(`<title>Home</title><a href="/next">Next</a>`))
	}))
	defer server.Close()
	if _, err := browser([]string{"--url", server.URL}, "goto"); err != nil {
		t.Fatal(err)
	}
	if _, err := browser([]string{"--element", "Next"}, "click"); err != nil {
		t.Fatal(err)
	}
	if _, err := browser(nil, "back"); err != nil {
		t.Fatalf("back after click: %v", err)
	}
	snapshot, err := browser(nil, "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.(map[string]any)["url"] != server.URL {
		t.Fatalf("back landed at %#v, want %s", snapshot, server.URL)
	}
}

func TestBrowserGotoTruncatesForwardHistory(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", stateDir)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<title>" + strings.TrimPrefix(r.URL.Path, "/") + "</title>"))
	}))
	defer server.Close()
	if _, err := browser([]string{"--url", server.URL + "/a"}, "goto"); err != nil {
		t.Fatal(err)
	}
	if _, err := browser([]string{"--url", server.URL + "/b"}, "goto"); err != nil {
		t.Fatal(err)
	}
	if _, err := browser(nil, "back"); err != nil {
		t.Fatal(err)
	}
	if _, err := browser([]string{"--url", server.URL + "/c"}, "goto"); err != nil {
		t.Fatal(err)
	}
	if _, err := browser(nil, "forward"); err == nil {
		t.Fatal("forward navigation survived a fresh goto")
	}
	state, err := browser(nil, "get")
	if err != nil {
		t.Fatal(err)
	}
	if state.(browserState).URL != server.URL+"/c" {
		t.Fatalf("current URL = %#v, want /c", state)
	}
}

func TestBrowserWaitChecksImmediatelyAtZeroTimeout(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", stateDir)
	if err := saveBrowser(filepath.Join(stateDir, "browser.json"), browserState{Text: "ready"}); err != nil {
		t.Fatal(err)
	}
	value, err := browser([]string{"--text", "ready", "--timeout", "0"}, "wait")
	if err != nil {
		t.Fatalf("wait with a zero timeout: %v", err)
	}
	if found, ok := value.(map[string]any)["found"].(bool); !ok || !found {
		t.Fatalf("wait result = %#v, want found=true", value)
	}
}

func TestHelpWordDoesNotConsumeFlagValues(t *testing.T) {
	if hasHelpWord([]string{"fill", "--value", "help"}) {
		t.Fatal("help value was mistaken for a command help request")
	}
	if !hasHelpWord([]string{"repo", "list", "help"}) {
		t.Fatal("subcommand help word was not recognized")
	}
}

func TestAgentContextMatchesReferenceShape(t *testing.T) {
	value, err := agentContext(nil)
	if err != nil {
		t.Fatal(err)
	}
	data, ok := value.(map[string]any)
	if !ok || data["schemaVersion"] != 1 || data["commandCount"] != 242 {
		t.Fatalf("agent context = %#v", value)
	}
	commands, ok := data["commands"].([]map[string]any)
	if !ok || len(commands) != 242 {
		t.Fatalf("commands = %T/%d", data["commands"], len(commands))
	}
	for _, command := range commands {
		if command["command"] == "worktree create" {
			flags := command["flags"].([]string)
			if !containsString(flags, "parent-worktree") || !containsString(flags, "no-parent") {
				t.Fatalf("worktree create flags = %#v", flags)
			}
			return
		}
	}
	t.Fatal("worktree create missing from schema")
}

func TestAutomationAndOrchestrationReferenceFlagsPersist(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", stateDir)
	automation, err := automations([]string{"create", "--name", "Nightly", "--prompt", "echo hi", "--provider", "codex", "--timezone", "UTC", "--disabled", "--fresh-session"})
	if err != nil {
		t.Fatal(err)
	}
	automationItem := automation.(map[string]any)
	if automationItem["prompt"] != "echo hi" || automationItem["command"] != "echo hi" || automationItem["enabled"] != false || automationItem["freshSession"] != true {
		t.Fatalf("automation = %#v", automationItem)
	}
	run, err := orchestration([]string{"run-create", "--objective", "ship", "--from", "cli"})
	if err != nil {
		t.Fatal(err)
	}
	runID := run.(map[string]any)["id"].(string)
	message, err := orchestration([]string{"send", "--body", "done", "--to", "worker-1", "--subject", "status"})
	if err != nil {
		t.Fatal(err)
	}
	messageItem := message.(map[string]any)
	if messageItem["body"] != "done" || messageItem["to"] != "worker-1" || messageItem["subject"] != "status" || messageItem["run"] != runID {
		t.Fatalf("message = %#v", messageItem)
	}
}

func TestBrowserAuxAndEmulatorAliases(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", stateDir)
	if _, err := browserAux([]string{"media", "--color-scheme", "dark", "--reduced-motion", "reduce"}, "set"); err != nil {
		t.Fatal(err)
	}
	if _, err := browserAux([]string{"set", "--name", "sid", "--value", "abc", "--domain", "example.test", "--secure"}, "cookie"); err != nil {
		t.Fatal(err)
	}
	cookie, err := browserAux([]string{"get", "--url", "https://example.test"}, "cookie")
	if err != nil || cookie.(map[string]any)["cookies"].(map[string]string)["sid"] != "abc" {
		t.Fatalf("cookie = %#v, %v", cookie, err)
	}
	if _, err := emulator([]string{"attach", "--device", "sim-1", "--focus"}); err != nil {
		t.Fatal(err)
	}
	action, err := emulator([]string{"tap", "--emulator", "sim-1", "--x", "0.25", "--y", "0.75"})
	if err != nil {
		t.Fatal(err)
	}
	if action.(map[string]any)["action"].(map[string]any)["x"] != 0.25 {
		t.Fatalf("tap action = %#v", action)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestRenderArtifacts(t *testing.T) {
	state := browserState{URL: "https://example.test", Text: "Hello (world)"}
	pdfPath := filepath.Join(t.TempDir(), "page.pdf")
	value, err := renderPDF(&state, []string{"--out", pdfPath})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(pdfPath)
	if err != nil || !strings.HasPrefix(string(data), "%PDF-1.4") {
		t.Fatalf("renderPDF() = %#v, %v", value, err)
	}
	svgPath := filepath.Join(t.TempDir(), "page.svg")
	if _, err := renderScreenshot(&state, []string{"--out", svgPath}); err != nil {
		t.Fatal(err)
	}
	svg, err := os.ReadFile(svgPath)
	if err != nil || !strings.Contains(string(svg), "Hello (world)") {
		t.Fatalf("renderScreenshot() = %q, %v", svg, err)
	}
	pngPath := filepath.Join(t.TempDir(), "page.png")
	if _, err := renderScreenshot(&state, []string{"--out", pngPath, "--format", "png"}); err != nil {
		t.Fatal(err)
	}
	pngFile, err := os.Open(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(pngFile); err != nil {
		t.Fatalf("renderScreenshot PNG is invalid: %v", err)
	}
	_ = pngFile.Close()
}

func TestTabsProfilesAndBrowserProperties(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", stateDir)
	if _, err := tabs([]string{"profile", "create", "--id", "isolated"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tabs([]string{"create", "--url", "about:blank", "--profile", "isolated"}); err != nil {
		t.Fatal(err)
	}
	current, err := tabs([]string{"current"})
	if err != nil {
		t.Fatal(err)
	}
	if current.(map[string]any)["profile"] != "isolated" {
		t.Fatalf("current tab profile = %#v", current)
	}
	if _, err := tabs([]string{"create", "--url", "about:blank", "--profile", "isolated"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tabs([]string{"close", "--index", "1"}); err != nil {
		t.Fatal(err)
	}
	current, err = tabs([]string{"current"})
	if err != nil {
		t.Fatal(err)
	}
	if current.(map[string]any)["profile"] != "isolated" {
		t.Fatalf("profile after closing an earlier tab = %#v, want isolated", current)
	}
	state := browserState{URL: "https://example.test", Title: "Example", Text: "Hello", HTML: `<a href="/next">Next</a>`, Fields: map[string]string{"#input": "value"}}
	if value, found := pageProperty(&state, "#input", "value"); !found || value != "value" {
		t.Fatalf("pageProperty(value) = %#v, %v", value, found)
	}
	if value, found := pageProperty(&state, "Next", "visible"); !found || value != true {
		t.Fatalf("pageProperty(visible) = %#v, %v", value, found)
	}
}

func TestLocalLinearIssueCurrentAndProjectList(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", stateDir)
	created, err := localLinear([]string{"create", "--title", "Without project", "--team", "ENG"})
	if err != nil {
		t.Fatal(err)
	}
	createdItem, ok := created.(map[string]any)
	if !ok {
		t.Fatalf("create returned %T, want map", created)
	}
	current, err := localLinear([]string{"issue", "--current"})
	if err != nil {
		t.Fatalf("issue --current: %v", err)
	}
	currentItem, ok := current.(map[string]any)
	if !ok || currentItem["id"] != createdItem["id"] {
		t.Fatalf("issue --current = %#v, want id %v", current, createdItem["id"])
	}
	if _, err := localLinear([]string{"create", "--title", "With project", "--team", "ENG", "--project", "Roadmap"}); err != nil {
		t.Fatal(err)
	}
	projects, err := localLinear([]string{"project", "list"})
	if err != nil {
		t.Fatal(err)
	}
	projectItems, ok := projects.([]map[string]any)
	if !ok || len(projectItems) != 1 || projectItems[0]["name"] != "Roadmap" {
		t.Fatalf("project list = %#v, want only Roadmap", projects)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "issues.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["current"] != createdItem["id"] && persisted["current"] == nil {
		t.Fatalf("persisted current missing: %#v", persisted)
	}
}

func TestEmulatorBridgeErrorsAreReported(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", stateDir)
	binDir := t.TempDir()
	adbPath := filepath.Join(binDir, "adb")
	if err := os.WriteFile(adbPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	if _, err := emulator([]string{"attach", "--device", "test-device"}); err != nil {
		t.Fatal(err)
	}
	apk := filepath.Join(stateDir, "app.apk")
	if err := os.WriteFile(apk, []byte("not-an-apk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := emulator([]string{"install", apk}); err == nil {
		t.Fatal("emulator install hid the adb failure")
	}
	if _, err := emulator([]string{"launch", "--package", "com.example"}); err == nil {
		t.Fatal("emulator launch hid the adb failure")
	}
}

func TestBrowserDownloadDecodesDataURLsAndRejectsMissingSelectors(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", stateDir)
	state := browserState{URL: "https://example.test", HTML: `<a href="data:text/plain,hello%20world">text</a><a href="data:text/plain;base64,` + base64.StdEncoding.EncodeToString([]byte("binary")) + `">binary</a>`}
	if err := saveBrowser(filepath.Join(stateDir, "browser.json"), state); err != nil {
		t.Fatal(err)
	}
	textPath := filepath.Join(stateDir, "text.txt")
	if _, err := browserAux([]string{"--selector", "text", "--path", textPath}, "download"); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(textPath)
	if err != nil || string(text) != "hello world" {
		t.Fatalf("decoded text download = %q, %v", text, err)
	}
	binaryPath := filepath.Join(stateDir, "binary.bin")
	if _, err := browserAux([]string{"--selector", "binary", "--path", binaryPath}, "download"); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(binaryPath)
	if err != nil || string(binary) != "binary" {
		t.Fatalf("decoded base64 download = %q, %v", binary, err)
	}
	downloads, err := browserAux([]string{"list"}, "download")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := downloads.([]string); !ok || len(got) != 2 || got[0] != textPath || got[1] != binaryPath {
		t.Fatalf("download list = %#v, want both downloaded paths", downloads)
	}
	if _, err := browserAux([]string{"--selector", "missing", "--path", filepath.Join(stateDir, "missing")}, "download"); err == nil {
		t.Fatal("download accepted a missing selector")
	}
}

func TestBooleanWorkspaceFieldsStayBooleans(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", stateDir)
	if _, err := automations([]string{"create", "--id", "automation-1"}); err != nil {
		t.Fatal(err)
	}
	updated, err := automations([]string{"edit", "--id", "automation-1", "--enabled", "false"})
	if err != nil {
		t.Fatal(err)
	}
	if enabled, ok := updated.(map[string]any)["enabled"].(bool); !ok || enabled {
		t.Fatalf("automation enabled = %#v, want bool false", updated)
	}
	if _, err := project([]string{"setup-create", "--id", "setup-1"}); err != nil {
		t.Fatal(err)
	}
	updatedSetup, err := project([]string{"setup-update", "--id", "setup-1", "--ready", "false"})
	if err != nil {
		t.Fatal(err)
	}
	if ready, ok := updatedSetup.(map[string]any)["ready"].(bool); !ok || ready {
		t.Fatalf("project setup ready = %#v, want bool false", updatedSetup)
	}
}

func TestDiscoverSkillsSkipsNamespaceDirectories(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	valid := filepath.Join(root, ".agents", "skills", "valid")
	namespace := filepath.Join(root, ".agents", "skills", "group")
	if err := os.MkdirAll(filepath.Join(namespace, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(valid, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(valid, "SKILL.md"), []byte("# Valid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := discoverSkills()
	if len(got) != 1 || got[0].Name != "Valid" {
		t.Fatalf("discoverSkills() = %#v, want only the installable skill", got)
	}
}

func TestSkillMetadataPrefersExplicitNameOverHeading(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "SKILL.md")
	content := "---\nname: stable-selector\ndescription: stable description\n---\n# Human-readable heading\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	name, description := skillMetadata(root, "fallback")
	if name != "stable-selector" || description != "stable description" {
		t.Fatalf("skillMetadata() = %q, %q; want explicit front matter", name, description)
	}
}

func TestOpenChangedPreservesTheFirstPathCharacter(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "EveryAPI Test")
	path := filepath.Join(dir, "changed.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "changed.txt")
	runGit(t, dir, "commit", "-qm", "init")
	if err := os.WriteFile(path, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	value, err := files([]string{"open-changed"})
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	paths, ok := result["files"].([]string)
	if !ok || len(paths) != 1 || paths[0] != "changed.txt" {
		t.Fatalf("open-changed = %#v, want changed.txt", value)
	}
}

func TestBrowserActionsRejectMissingRequiredArguments(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", stateDir)
	for _, name := range []string{"focus", "clear", "keypress", "scrollintoview"} {
		if _, err := browser(nil, name); err == nil {
			t.Errorf("%s accepted missing required arguments", name)
		}
	}
	if _, err := browser([]string{"--file", filepath.Join(stateDir, "file")}, "upload"); err == nil {
		t.Error("upload accepted a missing element")
	}
	for _, name := range []string{"highlight", "viewport", "geolocation"} {
		if _, err := browserAux(nil, name); err == nil {
			t.Errorf("%s accepted missing required arguments", name)
		}
	}
	if _, err := browserAux([]string{"remove"}, "intercept"); err == nil {
		t.Error("intercept remove accepted a missing pattern")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
