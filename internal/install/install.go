// Package install wires Frugal as an MCP server into the configs of
// MCP-aware agent clients — Claude Desktop, Cursor, and Claude Code.
//
// Two install patterns live here:
//
//   - File-based clients (Claude Desktop, Cursor) keep `mcpServers` in a
//     JSON config file. We idempotently merge `mcpServers.frugal =
//     {command, args, env}` into the existing file (or create it),
//     preserving every other key the user has set — including env values
//     a previous install baked that this run doesn't supply.
//
//   - CLI-managed clients (Claude Code) own their own config and expect
//     `claude mcp add` to mutate it. We shell out to the claude CLI when
//     it's available, doing a remove-then-add so the operation is
//     idempotent. If the exec fails (claude not on PATH at apply time,
//     incompatible flags, sandboxing), we fall back to printing the
//     exact command the user can run themselves.
//
// `frugal mcp install` consumes this package: it calls DetectClients to
// see what's present, renders the plan, and (with confirmation) calls
// Apply on each detected client.
package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ServerName is the key Frugal registers under in every client's
// mcpServers map. Stable across releases — changing it would orphan
// existing user configs.
const ServerName = "frugal"

// Kind describes how a client is mutated. File-based clients get a JSON
// merge; CLI-managed clients get a printed shell command.
type Kind string

const (
	KindJSONFile Kind = "json-file"
	KindCLI      Kind = "cli"
)

// Client describes one MCP-aware client target.
type Client struct {
	// ID is the stable identifier for the --client flag (claude-desktop,
	// cursor, claude-code).
	ID string
	// Title is the human-readable name shown in the install plan.
	Title string
	// Kind picks between JSON-file merge and CLI-command print.
	Kind Kind
	// ConfigPath is the absolute path of the file that gets merged
	// (file-based clients only). Empty for CLI-managed clients.
	ConfigPath string
	// Detected is true when DetectClients found enough evidence that the
	// client is installed on this machine.
	Detected bool
	// DetectionReason is a short human-readable note explaining the
	// Detected verdict.
	DetectionReason string
}

// ServerEntry is the value Frugal writes under mcpServers.frugal. The
// shape matches what Claude Desktop and Cursor consume (both use the same
// schema). Args is omitted from JSON when empty.
type ServerEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// AllClients returns the static catalog of clients Frugal knows how to
// install into. Detection state (config-file existence, CLI on PATH) is
// populated by DetectClients; this function alone is filesystem-pure so
// tests can assert the catalog without setting up paths.
func AllClients() []Client {
	return []Client{
		{ID: "claude-desktop", Title: "Claude Desktop", Kind: KindJSONFile, ConfigPath: claudeDesktopConfigPath()},
		{ID: "cursor", Title: "Cursor", Kind: KindJSONFile, ConfigPath: cursorConfigPath()},
		{ID: "claude-code", Title: "Claude Code", Kind: KindCLI},
	}
}

// DetectClients fills Detected + DetectionReason on each client by
// checking for the relevant config file or binary on PATH. Returns the
// full catalog — undetected entries are still present so callers can
// explain "we didn't find X" if the user passes --client X.
func DetectClients() []Client {
	clients := AllClients()
	for i := range clients {
		c := &clients[i]
		switch c.ID {
		case "claude-desktop", "cursor":
			if c.ConfigPath == "" {
				c.DetectionReason = "no known config path for this OS"
				continue
			}
			// Detection is "the parent directory exists" — having the
			// directory means the client has been installed at some point,
			// even if the file itself hasn't been written yet.
			dir := filepath.Dir(c.ConfigPath)
			if _, err := os.Stat(dir); err == nil {
				c.Detected = true
				c.DetectionReason = "config dir present at " + dir
			} else {
				c.DetectionReason = "config dir not found at " + dir
			}
		case "claude-code":
			if path, err := exec.LookPath("claude"); err == nil {
				c.Detected = true
				c.DetectionReason = "claude CLI at " + path
			} else {
				c.DetectionReason = "claude CLI not found on PATH"
			}
		}
	}
	return clients
}

// PlanFor returns the human-readable change description that Apply would
// effect for client c, given the path to the frugal binary and the env
// vars that will ride along. Used by `frugal mcp install --print` to
// preview without writing. Env var NAMES appear in the plan; values are
// secrets and never get printed.
func PlanFor(c Client, binPath string, env map[string]string) string {
	switch c.Kind {
	case KindJSONFile:
		if len(env) > 0 {
			return fmt.Sprintf("merge `mcpServers.%s = {command: %q, args: [mcp serve], env: {%s}}` into %s (env values copied from this shell)",
				ServerName, binPath, strings.Join(sortedKeys(env), ", "), c.ConfigPath)
		}
		return fmt.Sprintf("merge `mcpServers.%s = {command: %q, args: [mcp serve]}` into %s",
			ServerName, binPath, c.ConfigPath)
	case KindCLI:
		return "register via `" + claudeCodeAddCommand(binPath) + "` (idempotent)"
	}
	return ""
}

// Apply writes (or schedules) the install for one client. For JSON-file
// clients the config is read, merged, and written atomically — including
// env, because Claude Desktop and Cursor spawn the server without a
// login shell, so the user's rc-file exports never reach it. CLI clients
// (Claude Code) deliberately get NO baked env: the claude CLI runs from
// a shell and its MCP children inherit that environment live, so baking
// values would only let them go stale after a key rotation. For CLI
// clients Apply shells out to `claude mcp` to register Frugal
// idempotently; on failure it returns BOTH the error (registration did
// not happen — the caller must not exit 0) and the equivalent command
// string as remediation for the user to run by hand.
func Apply(c Client, binPath string, env map[string]string) (suggestion string, err error) {
	switch c.Kind {
	case KindJSONFile:
		entry := ServerEntry{Command: binPath, Args: []string{"mcp", "serve"}, Env: env}
		return "", mergeJSONConfig(c.ConfigPath, ServerName, entry)
	case KindCLI:
		if err := claudeMCPAdder(binPath); err != nil {
			return claudeCodeAddCommand(binPath), err
		}
		return "", nil
	}
	return "", fmt.Errorf("unknown client kind: %s", c.Kind)
}

// sortedKeys returns env's keys in stable order so plans and JSON output
// don't shuffle between runs.
func sortedKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// claudeMCPAdder is swappable so tests can avoid shelling out. Default
// implementation invokes the real claude CLI; tests reassign it.
var claudeMCPAdder = runClaudeMCPAdd

// runClaudeCommand is the exec seam under runClaudeMCPAdd, swappable so
// tests can simulate specific claude CLI versions (e.g. one that rejects
// --scope) without a real binary on PATH.
var runClaudeCommand = func(args ...string) ([]byte, error) {
	return exec.Command("claude", args...).CombinedOutput()
}

// runClaudeMCPAdd registers Frugal with Claude Code via the claude CLI.
// Removes any prior `frugal` entry first so re-running install is
// idempotent (the `add` itself would otherwise error on a duplicate).
// claude's output is suppressed — the installer prints its own status.
func runClaudeMCPAdd(binPath string) error {
	// Best-effort removes; missing-entry is not an error we care about. The
	// unscoped variant clears local-scoped entries left by installer
	// versions that predate --scope user.
	_, _ = runClaudeCommand("mcp", "remove", ServerName)
	_, _ = runClaudeCommand("mcp", "remove", "--scope", "user", ServerName)
	out, err := runClaudeCommand("mcp", "add", "--scope", "user", ServerName, "--", binPath, "mcp", "serve")
	if err == nil {
		return nil
	}
	// Older claude CLIs predate --scope and reject the flag outright —
	// only THAT failure gets the unscoped retry. Any other failure (config
	// write conflict, permissions) must surface: retrying it unscoped can
	// "succeed" at the CLI's default local scope, silently pinning the
	// registration to whatever directory the installer ran from.
	if !looksLikeUnknownFlag(string(out) + " " + err.Error()) {
		return fmt.Errorf("claude mcp add --scope user: %w (%s)", err, bytes.TrimSpace(out))
	}
	legacyOut, legacyErr := runClaudeCommand("mcp", "add", ServerName, "--", binPath, "mcp", "serve")
	if legacyErr == nil {
		return nil
	}
	return fmt.Errorf("claude mcp add: %w (%s); legacy retry without --scope: %v (%s)",
		err, bytes.TrimSpace(out), legacyErr, bytes.TrimSpace(legacyOut))
}

// looksLikeUnknownFlag reports whether CLI output/error text reads as a
// flag-parse rejection of --scope itself (legacy claude CLI that predates
// the flag) rather than a real failure. Deliberately narrow: a VALUE
// rejection like commander's "option '--scope <scope>' argument 'user' is
// invalid. Allowed choices are ..." means the CLI knows --scope but not
// our value — retrying unscoped there would silently register at local
// scope, the exact downgrade this gate exists to prevent. Only "unknown"/
// "unrecognized" flag-shaped messages qualify.
func looksLikeUnknownFlag(s string) bool {
	s = strings.ToLower(s)
	if strings.Contains(s, "argument") || strings.Contains(s, "allowed choices") {
		return false
	}
	if !strings.Contains(s, "scope") && !strings.Contains(s, "flag") && !strings.Contains(s, "option") {
		return false
	}
	return strings.Contains(s, "unknown") || strings.Contains(s, "unrecognized")
}

// FrugalBinary resolves the absolute path of the running frugal binary,
// following symlinks so the embedded config points at the real file
// (not the homebrew shim or the installer's wrapper). The path is what
// gets written into Claude Desktop / Cursor configs so the agent process
// can spawn `frugal mcp serve` later.
func FrugalBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate frugal binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// claudeDesktopConfigPath returns the OS-specific Claude Desktop config
// file location.
func claudeDesktopConfigPath() string {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return ""
		}
		return filepath.Join(appData, "Claude", "claude_desktop_config.json")
	case "linux":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
	return ""
}

// cursorConfigPath returns the global Cursor MCP config path. The project
// variant (.cursor/mcp.json in cwd) is out of scope for the v1 installer
// — easy to add later behind a --project flag.
func cursorConfigPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".cursor", "mcp.json")
}

// claudeCodeAddCommand renders the `claude mcp add` invocation the user
// should run to register Frugal with Claude Code. We print this rather
// than shelling out because the `claude` CLI's flag surface varies
// across versions and we'd rather hand the user a correct command than
// fail at exec. --scope user because the claude CLI defaults to local
// scope, which would pin the registration to whatever cwd the installer
// happened to run from.
func claudeCodeAddCommand(binPath string) string {
	return fmt.Sprintf("claude mcp add --scope user %s -- %s mcp serve", ServerName, binPath)
}

// mergeJSONConfig reads path (creating empty `{}` if missing), sets
// `mcpServers.<name> = entry`, and writes the result back. Other keys
// (including unknown mcpServers entries the user added by hand) are
// preserved verbatim — this is why we round-trip through map[string]any
// rather than a typed struct.
func mergeJSONConfig(path, name string, entry ServerEntry) error {
	if path == "" {
		return fmt.Errorf("install: empty config path")
	}
	root, err := readJSONFile(path)
	if err != nil {
		return err
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	entry.Env = mergeEnv(servers[name], entry.Env)
	servers[name] = entryToMap(entry)
	root["mcpServers"] = servers
	return writeJSONFile(path, root)
}

// mergeEnv folds the prior entry's env block into the new one. A re-run
// of `frugal mcp install` from a fresh shell (no exports) must not
// silently strip the keys a previous shell baked in — the post-install
// text tells users to re-run after rotating a key, so this path is hot.
// New values win on conflicts; removing a baked key entirely is a
// hand-edit of the client config by design.
func mergeEnv(prevEntry any, env map[string]string) map[string]string {
	prev, _ := prevEntry.(map[string]any)
	prevEnv, _ := prev["env"].(map[string]any)
	if len(prevEnv) == 0 {
		return env
	}
	merged := make(map[string]string, len(prevEnv)+len(env))
	for k, v := range prevEnv {
		if s, ok := v.(string); ok {
			merged[k] = s
		}
	}
	for k, v := range env {
		merged[k] = v
	}
	return merged
}

func entryToMap(e ServerEntry) map[string]any {
	out := map[string]any{"command": e.Command}
	if len(e.Args) > 0 {
		// json.Marshal/Unmarshal roundtrip would turn []string into []any
		// when later read back, so we materialize it as []any up front for
		// the in-memory value to match the on-disk shape. Users editing
		// the file by hand will see the same structure either way.
		args := make([]any, len(e.Args))
		for i, a := range e.Args {
			args[i] = a
		}
		out["args"] = args
	}
	if len(e.Env) > 0 {
		env := make(map[string]any, len(e.Env))
		for k, v := range e.Env {
			env[k] = v
		}
		out["env"] = env
	}
	return out
}

// readJSONFile loads path as a generic JSON object. Returns an empty map
// when the file doesn't exist — that's the "first install" case.
func readJSONFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

// writeJSONFile writes the map atomically: dump JSON to a temp file in
// the same directory, fsync, rename over the target. Pretty-prints with
// two-space indentation to match what Claude Desktop and Cursor produce.
func writeJSONFile(path string, root map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	buf, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	// Append a trailing newline — both Claude Desktop and Cursor write
	// one and editors expect it.
	buf = append(buf, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".frugal-install-*.json")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op if rename succeeded
	if _, err := tmp.Write(buf); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp to %s: %w", path, err)
	}
	return nil
}
