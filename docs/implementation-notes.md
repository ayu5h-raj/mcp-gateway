# Implementation Notes

## Phase 0 — Scaffolding

### MCP SDK choice (Task 0.6)

Primary SDK selected: `github.com/modelcontextprotocol/go-sdk v1.5.0`

The primary SDK (`github.com/modelcontextprotocol/go-sdk`) resolved successfully via `go get`. No fallback to `github.com/mark3labs/mcp-go` was needed.

Note: both `github.com/modelcontextprotocol/go-sdk` and `github.com/stretchr/testify` declare a minimum Go version of 1.25, which causes `go get` to upgrade the `go` directive in `go.mod`. The directive is manually pinned back to `go 1.23` after each `go get` invocation to match the project's target Go version. This is safe as long as the local toolchain (Go 1.25.x) is used for development; CI uses `go-version: "1.23"` which will download the 1.23 toolchain and may not be able to build packages that internally require 1.25 features. **If CI fails due to toolchain mismatch, consider bumping the project's go directive to `1.25` and updating the CI `go-version` to match.**
