package main

import (
	"fmt"
	"log"
	"os"
	"runtime/debug"

	"github.com/frugalsh/frugal/internal/config"
	"github.com/frugalsh/frugal/internal/metrics"
	"github.com/frugalsh/frugal/internal/obs"
	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/router"
)

func main() {
	obs.InitLogger()
	metrics.Register()

	configPath := "config/models.yaml"
	if p := os.Getenv("FRUGAL_CONFIG"); p != "" {
		configPath = p
	}

	if len(os.Args) < 2 {
		printHelp()
		return
	}

	switch os.Args[1] {
	case "-h", "--help", "help":
		printHelp()
	case "-v", "--version", "version":
		fmt.Println(version())
	case "sync":
		if err := runSync(configPath); err != nil {
			log.Fatalf("sync failed: %v", err)
		}
	case "bench":
		os.Exit(runBench(configPath, os.Args[2:]))
	case "run":
		os.Exit(runRun(os.Args[2:]))
	case "route":
		os.Exit(runRoute(os.Args[2:]))
	case "compare":
		os.Exit(runCompare(os.Args[2:]))
	case "mcp":
		os.Exit(runMCP(os.Args[2:]))
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q. Run 'frugal --help' for usage.\n", os.Args[1])
		os.Exit(2)
	}
}

// stubNotImplemented prints a uniform "not yet wired" message and exits 1.
// Each new Phase 1 subcommand reserves its name now so external scripts and
// docs can adopt the final spelling before the executor lands; the message
// tells the reader exactly which PR will replace the stub.
func stubNotImplemented(cmd, when string) int {
	fmt.Fprintf(os.Stderr,
		"frugal %s: not yet implemented (lands in %s)\n"+
			"see frugal-strategy-v6.md §11 for the rollout plan.\n",
		cmd, when)
	return 1
}

func printHelp() {
	fmt.Println(`frugal — tool calls are the new tokens
Cost-arbitrage MCP server. $0-first routing. Any model. BYOK. Source-available.

Usage:
  frugal run <task>              Execute the cheapest reliable toolchain for the task
  frugal route <task>            Dry-run: print the recipe + estimated cost (no execution)
  frugal compare <task>          Side-by-side: recipe vs baseline (cost + quality)
  frugal mcp install [--client]  Install Frugal as an MCP server in agent clients
  frugal mcp serve [--http :PORT] Run Frugal as an MCP server (stdio default)
  frugal bench [flags]           Run the recipe-bake-off benchmark
  frugal sync                    Refresh model pricing from models.dev
  frugal -v | --version          Print the build version
  frugal -h | --help             Show this help

Common environment:
  FRUGAL_CONFIG                  Path to models.yaml (default: config/models.yaml)
  FRUGAL_LOG_LEVEL               debug | info | warn | error
  FRUGAL_LOG_FORMAT              text | json
  OPENAI_API_KEY, ANTHROPIC_API_KEY, GOOGLE_API_KEY  Provider credentials

v1.0 note: the OpenAI-compatible HTTP proxy (frugal serve) and command-wrap
mode (frugal <cmd>) from v0.x were removed. See frugal-strategy-v6.md §10
for the migration path.

See README.md for the full reference.`)
}

// buildVersion is injected at release time via
//
//	go build -ldflags "-X main.buildVersion=$VERSION"
//
// (see Makefile). It takes precedence over debug.ReadBuildInfo so release
// binaries built with `go build` — not `go install` — still report a real
// tag. Left empty for local `go run` / `go build` without ldflags.
var buildVersion string

func version() string {
	if buildVersion != "" {
		return buildVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	return "dev"
}

// modelNames flattens the model map for a provider config into a slice.
// Used by bench.go when registering providers; kept here as a package-private
// helper so bench's startup mirrors the future run/route/compare startup that
// will need the same slice.
func modelNames(pc config.ProviderConfig) []string {
	names := make([]string, 0, len(pc.Models))
	for name := range pc.Models {
		names = append(names, name)
	}
	return names
}

// filterRegisteredModels drops router entries whose model has no registered
// provider (no API key, no provider driver). Shared between bench and the
// recipe executor (Phase 1 PR 5) so both refuse to route to a model nothing
// can serve.
func filterRegisteredModels(entries []router.ModelEntry, registry *provider.Registry) []router.ModelEntry {
	filtered := make([]router.ModelEntry, 0, len(entries))
	for _, entry := range entries {
		if _, err := registry.Resolve(entry.Name); err == nil {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
