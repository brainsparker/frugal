package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/config"
	"github.com/frugalsh/frugal/internal/install"
	"github.com/frugalsh/frugal/internal/ledger"
	"github.com/frugalsh/frugal/internal/routing"
)

// capture runs fn with os.Stdout and os.Stderr redirected and returns
// what each received.
func capture(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	oR, oW, _ := os.Pipe()
	eR, eW, _ := os.Pipe()
	oldO, oldE := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = oW, eW
	var ob, eb bytes.Buffer
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(&ob, oR); done <- struct{}{} }()
	go func() { _, _ = io.Copy(&eb, eR); done <- struct{}{} }()
	defer func() { os.Stdout, os.Stderr = oldO, oldE }()
	fn()
	oW.Close()
	eW.Close()
	<-done
	<-done
	return ob.String(), eb.String()
}

func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("FRUGAL_CONFIG", "")
	t.Setenv("ANYTHINGLLM_STORAGE_DIR", "")
	return home
}

func TestPrintHelpAndVersion(t *testing.T) {
	out, _ := capture(t, printHelp)
	for _, want := range []string{"frugal mcp serve", "frugal stats", "FRUGAL_CONFIG"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q", want)
		}
	}
	if version() == "" {
		t.Error("version() empty")
	}
	buildVersion = "v9.9.9"
	defer func() { buildVersion = "" }()
	if version() != "v9.9.9" {
		t.Errorf("ldflags version not preferred: %s", version())
	}
}

func TestRunMCP_Subcommands(t *testing.T) {
	_, errOut := capture(t, func() {
		if code := runMCP(nil); code != 2 {
			t.Errorf("runMCP(nil) = %d, want 2", code)
		}
		if code := runMCP([]string{"bogus"}); code != 2 {
			t.Errorf("runMCP(bogus) = %d, want 2", code)
		}
	})
	if !strings.Contains(errOut, "bogus") {
		t.Errorf("unknown subcommand not named: %q", errOut)
	}
}

func TestRunMCPServe_FlagAndConfigErrors(t *testing.T) {
	isolatedHome(t)
	capture(t, func() {
		if code := runMCPServe([]string{"--no-such-flag"}); code != 2 {
			t.Errorf("bad flag = %d, want 2", code)
		}
		bad := filepath.Join(t.TempDir(), "bad.yaml")
		_ = os.WriteFile(bad, []byte("limits:\n  call_timeout: -5s\n"), 0o600)
		t.Setenv("FRUGAL_CONFIG", bad)
		if code := runMCPServe(nil); code != 1 {
			t.Errorf("invalid config = %d, want 1", code)
		}
		t.Setenv("FRUGAL_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
		if code := runMCPServe(nil); code != 1 {
			t.Errorf("missing config = %d, want 1", code)
		}
	})
}

func TestFlagSet(t *testing.T) {
	isolatedHome(t)
	// --http with no token and no --allow-anon must refuse to start.
	_, errOut := capture(t, func() {
		t.Setenv("FRUGAL_AUTH_TOKEN", "")
		if code := runMCPServe([]string{"--http", "0.0.0.0:0", "--call-timeout", "5s"}); code == 0 {
			t.Errorf("unauthenticated non-local --http should fail")
		}
	})
	if errOut == "" {
		t.Error("expected an error message")
	}
}

func TestRunStats_EmptyAndPopulated(t *testing.T) {
	isolatedHome(t)
	out, _ := capture(t, func() {
		if code := runStats(nil); code != 0 {
			t.Errorf("runStats = %d", code)
		}
	})
	if out == "" {
		t.Error("empty receipt printed nothing")
	}
	dir, err := ledger.Dir()
	if err != nil {
		t.Fatal(err)
	}
	w := ledger.NewWriter(dir, map[string]float64{"search": 0.005}, nil)
	w.Record("frugal__search", "serper", 120*time.Millisecond, 0.001, true, true)
	w.Record("frugal__search", "searxng", 80*time.Millisecond, 0, false, false)
	out, _ = capture(t, func() {
		if code := runStats([]string{"--all"}); code != 0 {
			t.Errorf("runStats --all = %d", code)
		}
	})
	if !strings.Contains(out, "$") {
		t.Errorf("receipt has no dollar figures: %q", out)
	}
	out, _ = capture(t, func() {
		if code := runStats([]string{"--json"}); code != 0 {
			t.Errorf("runStats --json = %d", code)
		}
	})
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("--json not JSON: %q", out)
	}
	capture(t, func() {
		if code := runStats([]string{"--nope"}); code != 2 {
			t.Errorf("bad flag = %d", code)
		}
	})
}

func TestRunMCPInstall(t *testing.T) {
	home := isolatedHome(t)
	_, errOut := capture(t, func() {
		if code := runMCPInstall([]string{"--client", "nope"}); code != 2 {
			t.Errorf("unknown client = %d, want 2", code)
		}
		if code := runMCPInstall([]string{"--bad"}); code != 2 {
			t.Errorf("bad flag = %d, want 2", code)
		}
		if code := runMCPInstall([]string{"--client", "cursor", "--print"}); code != 0 {
			t.Errorf("--print = %d, want 0", code)
		}
	})
	if !strings.Contains(errOut, "planned changes") || !strings.Contains(errOut, "no changes written") {
		t.Errorf("--print output: %q", errOut)
	}
	capture(t, func() {
		if code := runMCPInstall([]string{"--client", "cursor", "--yes"}); code != 0 {
			t.Errorf("install cursor = %d, want 0", code)
		}
	})
	var found bool
	_ = filepath.Walk(home, func(p string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() && strings.HasSuffix(p, "mcp.json") {
			b, _ := os.ReadFile(p)
			found = found || strings.Contains(string(b), "frugal")
		}
		return nil
	})
	if !found {
		t.Error("cursor mcp.json with a frugal entry not written under temp HOME")
	}
}

func TestFilterClients(t *testing.T) {
	all := []install.Client{{ID: "a", Detected: true}, {ID: "b"}}
	got, err := filterClients(all, "all")
	if err != nil || len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("all = %v %v", got, err)
	}
	if got, _ := filterClients(all, "b"); len(got) != 1 {
		t.Fatalf("explicit b = %v", got)
	}
	if _, err := filterClients(all, "zzz"); err == nil || !strings.Contains(err.Error(), "a | b") {
		t.Fatalf("unknown client err = %v", err)
	}
}

func TestProviderEnvVars(t *testing.T) {
	isolatedHome(t)
	t.Setenv("SERPER_API_KEY", "sk")
	t.Setenv("BRAVE_API_KEY", "")
	env := providerEnvVars()
	if env["SERPER_API_KEY"] != "sk" {
		t.Errorf("SERPER_API_KEY not harvested: %v", env)
	}
	if _, ok := env["BRAVE_API_KEY"]; ok {
		t.Errorf("empty var harvested: %v", env)
	}
}

func TestBuilders(t *testing.T) {
	cfg, err := config.Parse([]byte(`cache:
  enabled: true
  search_ttl: 2m
  extract_ttl: 1h
routing:
  domains:
    deny: ["*.internal"]
    deny_private: true
browse_providers:
  browserless: {api_key_env: FRUGAL_TEST_BL, cost_per_call: 0.002}
`))
	if err != nil {
		t.Fatal(err)
	}
	c, sTTL, eTTL := buildResultCache(cfg)
	if c == nil || sTTL != 2*time.Minute || eTTL != time.Hour {
		t.Errorf("cache = %v %s %s", c, sTTL, eTTL)
	}
	if c, _, _ := buildResultCache(&config.Config{}); c != nil {
		t.Error("cache built with no config")
	}
	if buildDomainPolicy(cfg) == nil || buildDomainPolicy(&config.Config{}) != nil {
		t.Error("domain policy wiring wrong")
	}
	t.Setenv("FRUGAL_TEST_BL", "")
	if len(buildBrowsers(cfg)) != 0 {
		t.Error("browserless registered without a token")
	}
	t.Setenv("FRUGAL_TEST_BL", "tok")
	if b := buildBrowsers(cfg); len(b) != 1 || b[0].Name() != "browserless" {
		t.Errorf("browsers = %v", browserNames(b))
	}
	capture(t, func() {
		warnPolicyStrangers("search", routing.Policy{Order: []string{"ghost"}, Deny: map[string]bool{"phantom": true}}, []string{"serper"})
	})
	if got := denyNames(map[string]bool{"b": true, "a": true}); strings.Join(got, ",") != "a,b" {
		t.Errorf("denyNames = %v", got)
	}
}
