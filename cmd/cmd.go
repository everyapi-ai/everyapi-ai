// Package cmd hosts the small / top-level subcommand handlers (login, logout, status, topup, use, update, version, relaykey, qrurl). Bigger command families live in their own subpackages:
//
//   - cmd/seller  — channel-marketplace `seller …` commands
//   - cmd/proxy   — local sanitizer proxy `proxy …` commands
//   - cmd/mcp     — `mcp install` / `mcp uninstall` glue (the MCP server itself lives in internal/mcp)
//
// Commands take (args []string) and return an error. Errors bubble to main.go which prints "Error: <msg>" and exits 1. Shared output helpers (Out, Printf, Println, contexts) live in internal/cliout so the subpackages can reach them without an awkward back-import.
package cmd
