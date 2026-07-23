package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/frugalsh/frugal/internal/obs"
)

func main() {
	obs.InitLogger()

	if len(os.Args) < 2 {
		printHelp()
		return
	}

	switch os.Args[1] {
	case "-h", "--help", "help":
		printHelp()
	case "-v", "--version", "version":
		fmt.Println(version())
	case "mcp":
		os.Exit(runMCP(os.Args[2:]))
	case "stats":
		os.Exit(runStats(os.Args[2:]))
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q. Run 'frugal --help' for usage.\n", os.Args[1])
		os.Exit(2)
	}
}

func printHelp() {
	fmt.Println(`frugal — the open routing layer for AI tools
Tool calls routed per policy (cost, latency, failover, pinning) with the
decision in every response. Any model. BYOK. Source-available.

Usage:
  frugal mcp install [--client]   Install Frugal as an MCP server in agent clients
  frugal mcp serve [--http ADDR]  Run Frugal as an MCP server (stdio default)
  frugal stats [--all] [--json]   Show this month's tool-call savings receipt
  frugal -v | --version           Print the build version
  frugal -h | --help              Show this help

Common environment:
  FRUGAL_CONFIG                   Path to models.yaml (default: ./config/models.yaml,
                                  then ~/.frugal/config/models.yaml, then built-in defaults)
  FRUGAL_LOG_LEVEL                debug | info | warn | error
  FRUGAL_LOG_FORMAT               text | json
  SERPER_API_KEY                  Cheap-paid web search (Serper)
  YDC_API_KEY                     Premium web search (You.com)
  FIRECRAWL_API_KEY               Premium extract (Firecrawl)
  BROWSERLESS_TOKEN               Headless browse (Browserless)
  SEARXNG_URL                     Self-hosted SearXNG instance (free, preferred when set)
  FRUGAL_AUTH_TOKEN               Bearer token required by 'mcp serve --http'
                                  (or pass --allow-anon for localhost / trusted-proxy use)

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
