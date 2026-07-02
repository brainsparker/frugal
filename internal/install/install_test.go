package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeJSONConfig_CreatesFileWithEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "config.json")
	entry := ServerEntry{Command: "/usr/local/bin/frugal", Args: []string{"mcp", "serve"}}
	if err := mergeJSONConfig(path, ServerName, entry); err != nil {
		t.Fatalf("mergeJSONConfig: %v", err)
	}
	root := loadJSON(t, path)
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers missing or wrong type: %#v", root["mcpServers"])
	}
	frugal, ok := servers["frugal"].(map[string]any)
	if !ok {
		t.Fatalf("frugal entry missing: %#v", servers)
	}
	if got := frugal["command"]; got != "/usr/local/bin/frugal" {
		t.Errorf("command: got %v", got)
	}
	args, ok := frugal["args"].([]any)
	if !ok || len(args) != 2 || args[0] != "mcp" || args[1] != "serve" {
		t.Errorf("args: got %#v", frugal["args"])
	}
}

func TestMergeJSONConfig_PreservesUnrelatedEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// Seed with an existing user entry that has nothing to do with frugal.
	existing := map[string]any{
		"mcpServers": map[string]any{
			"my-custom-server": map[string]any{
				"command": "/opt/bin/other",
				"args":    []any{"--flag"},
			},
		},
		"unrelated_top_level": "should-survive",
	}
	writeJSON(t, path, existing)

	if err := mergeJSONConfig(path, ServerName, ServerEntry{Command: "/bin/frugal", Args: []string{"mcp", "serve"}}); err != nil {
		t.Fatalf("mergeJSONConfig: %v", err)
	}

	root := loadJSON(t, path)
	if root["unrelated_top_level"] != "should-survive" {
		t.Errorf("unrelated top-level key dropped: %#v", root)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if _, ok := servers["my-custom-server"]; !ok {
		t.Errorf("custom server entry dropped: %#v", servers)
	}
	if _, ok := servers["frugal"]; !ok {
		t.Errorf("frugal entry not added: %#v", servers)
	}
}

func TestMergeJSONConfig_IdempotentOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	entry1 := ServerEntry{Command: "/old/path/frugal", Args: []string{"mcp", "serve"}}
	entry2 := ServerEntry{Command: "/new/path/frugal", Args: []string{"mcp", "serve"}}

	if err := mergeJSONConfig(path, ServerName, entry1); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	if err := mergeJSONConfig(path, ServerName, entry2); err != nil {
		t.Fatalf("second merge: %v", err)
	}

	root := loadJSON(t, path)
	servers, _ := root["mcpServers"].(map[string]any)
	frugal, _ := servers["frugal"].(map[string]any)
	if got := frugal["command"]; got != "/new/path/frugal" {
		t.Errorf("expected command to be overwritten with new path, got %v", got)
	}
}

func TestPlanFor_JSONClient(t *testing.T) {
	c := Client{ID: "claude-desktop", Kind: KindJSONFile, ConfigPath: "/tmp/desktop.json"}
	plan := PlanFor(c, "/usr/local/bin/frugal", nil)
	if !strings.Contains(plan, "mcpServers.frugal") {
		t.Errorf("plan should mention mcpServers.frugal: %s", plan)
	}
	if !strings.Contains(plan, "/tmp/desktop.json") {
		t.Errorf("plan should mention config path: %s", plan)
	}
	if !strings.Contains(plan, "/usr/local/bin/frugal") {
		t.Errorf("plan should mention binary path: %s", plan)
	}
}

func TestPlanFor_JSONClient_ShowsEnvNamesNotValues(t *testing.T) {
	c := Client{ID: "claude-desktop", Kind: KindJSONFile, ConfigPath: "/tmp/desktop.json"}
	env := map[string]string{"SERPER_API_KEY": "s3cret-value"}
	plan := PlanFor(c, "/usr/local/bin/frugal", env)
	if !strings.Contains(plan, "SERPER_API_KEY") {
		t.Errorf("plan should list the env var name: %s", plan)
	}
	if strings.Contains(plan, "s3cret-value") {
		t.Errorf("plan must never print env var values: %s", plan)
	}
}

func TestPlanFor_CLIClient_PrintsClaudeCommand(t *testing.T) {
	c := Client{ID: "claude-code", Kind: KindCLI}
	plan := PlanFor(c, "/usr/local/bin/frugal", nil)
	if !strings.Contains(plan, "claude mcp add --scope user frugal") {
		t.Errorf("plan should suggest claude mcp add: %s", plan)
	}
	if !strings.Contains(plan, "/usr/local/bin/frugal mcp serve") {
		t.Errorf("plan should include frugal binary + args: %s", plan)
	}
}

func TestApply_JSONFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	c := Client{ID: "claude-desktop", Kind: KindJSONFile, ConfigPath: path}
	if _, err := Apply(c, "/bin/frugal", nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	root := loadJSON(t, path)
	servers, _ := root["mcpServers"].(map[string]any)
	frugal, _ := servers["frugal"].(map[string]any)
	if frugal == nil {
		t.Fatalf("frugal entry missing after Apply: %#v", servers)
	}
	if _, hasEnv := frugal["env"]; hasEnv {
		t.Errorf("no env vars passed; env block should be omitted: %#v", frugal)
	}
}

func TestApply_JSONFile_PreservesBakedEnvOnRerun(t *testing.T) {
	// Re-running install from a fresh shell (no exports) must not strip
	// the keys a previous shell baked in; new values win on conflicts.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	c := Client{ID: "claude-desktop", Kind: KindJSONFile, ConfigPath: path}
	if _, err := Apply(c, "/bin/frugal", map[string]string{"SERPER_API_KEY": "old", "YDC_API_KEY": "keep-me"}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if _, err := Apply(c, "/bin/frugal", map[string]string{"SERPER_API_KEY": "rotated"}); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	root := loadJSON(t, path)
	servers, _ := root["mcpServers"].(map[string]any)
	frugal, _ := servers["frugal"].(map[string]any)
	got, _ := frugal["env"].(map[string]any)
	if got["SERPER_API_KEY"] != "rotated" {
		t.Errorf("re-run should refresh rotated value; got %#v", got)
	}
	if got["YDC_API_KEY"] != "keep-me" {
		t.Errorf("re-run must preserve previously baked keys; got %#v", got)
	}
}

func TestApply_JSONFile_WritesEnvBlock(t *testing.T) {
	// GUI clients spawn the server with no login shell, so the only way a
	// key set in .zshrc reaches Frugal is the client config's env block.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	c := Client{ID: "claude-desktop", Kind: KindJSONFile, ConfigPath: path}
	env := map[string]string{"SERPER_API_KEY": "k1", "SEARXNG_URL": "http://localhost:8080"}
	if _, err := Apply(c, "/bin/frugal", env); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	root := loadJSON(t, path)
	servers, _ := root["mcpServers"].(map[string]any)
	frugal, _ := servers["frugal"].(map[string]any)
	got, _ := frugal["env"].(map[string]any)
	if got["SERPER_API_KEY"] != "k1" || got["SEARXNG_URL"] != "http://localhost:8080" {
		t.Errorf("env block not written through: %#v", frugal)
	}
}

func TestApply_CLIClient_ExecSuccess(t *testing.T) {
	prev := claudeMCPAdder
	t.Cleanup(func() { claudeMCPAdder = prev })
	claudeMCPAdder = func(string) error { return nil }

	c := Client{ID: "claude-code", Kind: KindCLI}
	suggestion, err := Apply(c, "/bin/frugal", nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if suggestion != "" {
		t.Errorf("exec succeeded; suggestion should be empty, got %q", suggestion)
	}
}

func TestApply_CLIClient_ExecFailureReturnsErrorAndSuggestion(t *testing.T) {
	// Registration did NOT happen, so the error must propagate (the
	// installer's exit code depends on it) alongside the remediation
	// command for the user to run by hand.
	prev := claudeMCPAdder
	t.Cleanup(func() { claudeMCPAdder = prev })
	claudeMCPAdder = func(string) error { return fmt.Errorf("simulated exec failure") }

	c := Client{ID: "claude-code", Kind: KindCLI}
	suggestion, err := Apply(c, "/bin/frugal", nil)
	if err == nil {
		t.Fatalf("exec failed; Apply must return the error, not swallow it")
	}
	if !strings.Contains(suggestion, "claude mcp add --scope user frugal") {
		t.Errorf("exec failed; expected fallback suggestion, got %q", suggestion)
	}
}

func TestLooksLikeUnknownFlag(t *testing.T) {
	cases := []struct {
		out  string
		want bool
	}{
		{"error: unknown option '--scope'", true},
		{"Error: unknown flag: --scope", true},
		{"unrecognized option '--scope'", true},
		// A VALUE rejection means the CLI knows --scope; retrying unscoped
		// would silently downgrade to local scope.
		{"error: option '--scope <scope>' argument 'user' is invalid. Allowed choices are local, project.", false},
		{"EACCES: permission denied writing ~/.claude.json", false},
		{"error: could not acquire config lock", false},
	}
	for _, c := range cases {
		if got := looksLikeUnknownFlag(c.out); got != c.want {
			t.Errorf("looksLikeUnknownFlag(%q) = %v, want %v", c.out, got, c.want)
		}
	}
}

// fakeClaudeCommand swaps the exec seam under runClaudeMCPAdd, recording
// every invocation and delegating the verdict to respond. Restores the
// real seam on cleanup.
func fakeClaudeCommand(t *testing.T, respond func(args []string) error) *[][]string {
	t.Helper()
	prev := runClaudeCommand
	t.Cleanup(func() { runClaudeCommand = prev })
	var calls [][]string
	runClaudeCommand = func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		return nil, respond(args)
	}
	return &calls
}

func isAdd(args []string) bool {
	return len(args) >= 2 && args[0] == "mcp" && args[1] == "add"
}

func hasScopeFlag(args []string) bool {
	for _, a := range args {
		if a == "--scope" {
			return true
		}
	}
	return false
}

func TestRunClaudeMCPAdd_ScopedAddSucceeds(t *testing.T) {
	calls := fakeClaudeCommand(t, func([]string) error { return nil })

	if err := runClaudeMCPAdd("/bin/frugal"); err != nil {
		t.Fatalf("runClaudeMCPAdd: %v", err)
	}
	var adds [][]string
	for _, c := range *calls {
		if isAdd(c) {
			adds = append(adds, c)
		}
	}
	if len(adds) != 1 {
		t.Fatalf("expected exactly one add attempt, got %d: %#v", len(adds), adds)
	}
	if !hasScopeFlag(adds[0]) {
		t.Errorf("first add attempt should use --scope user: %#v", adds[0])
	}
}

func TestRunClaudeMCPAdd_RealScopedFailureDoesNotDowngrade(t *testing.T) {
	// A genuine failure of the scoped add (permissions, config conflict)
	// must surface — an unscoped retry could "succeed" at local scope,
	// silently pinning the registration to the installer's cwd.
	calls := fakeClaudeCommand(t, func(args []string) error {
		if isAdd(args) && hasScopeFlag(args) {
			return fmt.Errorf("simulated: EACCES writing ~/.claude.json")
		}
		return nil
	})

	if err := runClaudeMCPAdd("/bin/frugal"); err == nil {
		t.Fatalf("real scoped failure must propagate, not downgrade to local scope")
	}
	var adds [][]string
	for _, c := range *calls {
		if isAdd(c) {
			adds = append(adds, c)
		}
	}
	if len(adds) != 1 {
		t.Errorf("no unscoped retry expected on a non-flag failure; adds=%#v", adds)
	}
}

func TestRunClaudeMCPAdd_RetriesWithoutScopeOnLegacyCLI(t *testing.T) {
	// Older claude CLIs reject --scope; the add must fall back to the
	// unscoped form instead of reporting failure.
	calls := fakeClaudeCommand(t, func(args []string) error {
		if isAdd(args) && hasScopeFlag(args) {
			return fmt.Errorf("simulated: unknown flag --scope")
		}
		return nil
	})

	if err := runClaudeMCPAdd("/bin/frugal"); err != nil {
		t.Fatalf("legacy fallback should succeed, got: %v", err)
	}
	var adds [][]string
	for _, c := range *calls {
		if isAdd(c) {
			adds = append(adds, c)
		}
	}
	if len(adds) != 2 {
		t.Fatalf("expected scoped attempt then legacy retry, got %d adds: %#v", len(adds), adds)
	}
	if !hasScopeFlag(adds[0]) || hasScopeFlag(adds[1]) {
		t.Errorf("expected [scoped, unscoped] order, got %#v", adds)
	}
}

func TestRunClaudeMCPAdd_ErrorsWhenBothAttemptsFail(t *testing.T) {
	// Legacy CLI rejects --scope AND the unscoped retry also fails: the
	// error must show both attempts so the user sees the whole story.
	fakeClaudeCommand(t, func(args []string) error {
		if isAdd(args) && hasScopeFlag(args) {
			return fmt.Errorf("simulated: unknown flag --scope")
		}
		if isAdd(args) {
			return fmt.Errorf("simulated exec failure")
		}
		return nil
	})

	err := runClaudeMCPAdd("/bin/frugal")
	if err == nil {
		t.Fatal("both add attempts failed; expected an error")
	}
	if !strings.Contains(err.Error(), "legacy retry") {
		t.Errorf("error should mention the legacy retry so users see both attempts: %v", err)
	}
}

func TestDetectClients_HitsForTempConfigDir(t *testing.T) {
	// Point HOME at a tempdir where we'll create a fake Claude Desktop
	// directory; detection should flip claude-desktop to Detected=true.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpHome, ".config")) // some platforms read this
	// Create the OS-specific parent directory expected by detection.
	desktopPath := claudeDesktopConfigPath()
	if desktopPath == "" {
		t.Skip("no claude desktop path for this OS")
	}
	if err := os.MkdirAll(filepath.Dir(desktopPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	clients := DetectClients()
	for _, c := range clients {
		if c.ID == "claude-desktop" {
			if !c.Detected {
				t.Errorf("expected claude-desktop detected, got false (reason=%s)", c.DetectionReason)
			}
			return
		}
	}
	t.Fatalf("claude-desktop not in client list")
}

func TestAllClients_CatalogStable(t *testing.T) {
	got := AllClients()
	wantIDs := []string{"claude-desktop", "cursor", "claude-code"}
	if len(got) != len(wantIDs) {
		t.Fatalf("AllClients: got %d entries, want %d", len(got), len(wantIDs))
	}
	for i, want := range wantIDs {
		if got[i].ID != want {
			t.Errorf("AllClients[%d].ID = %q, want %q", i, got[i].ID, want)
		}
	}
}

// --- helpers ---

func loadJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

func writeJSON(t *testing.T, path string, root map[string]any) {
	t.Helper()
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
