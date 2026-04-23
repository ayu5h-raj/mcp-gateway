# Implementation Notes

## Phase 0 — Scaffolding

### MCP SDK choice (Task 0.6)

Primary SDK selected: `github.com/modelcontextprotocol/go-sdk v1.5.0`

The primary SDK (`github.com/modelcontextprotocol/go-sdk`) resolved successfully via `go get`. No fallback to `github.com/mark3labs/mcp-go` was needed.

Note: both `github.com/modelcontextprotocol/go-sdk` and `github.com/stretchr/testify` declare a minimum Go version of 1.25. Project `go` directive set to `1.25` and CI `go-version` updated to match, so the toolchains are aligned. (Plan 01 stated "Go 1.23+"; 1.25 satisfies that requirement.)
