// Package bridge implements a thin stdio ↔ HTTP proxy so that stdio-only MCP
// clients (e.g. Claude Desktop) can talk to the mcp-gateway daemon's Streamable
// HTTP endpoint. Each newline-delimited JSON frame on stdin becomes a single
// HTTP POST; each response body becomes one newline-delimited JSON frame on
// stdout. Streams (SSE) are v1.
package bridge

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RunConfig parameterises the bridge.
type RunConfig struct {
	URL    string    // full URL to the daemon's /mcp endpoint
	Stdin  io.Reader // where client writes requests
	Stdout io.Writer // where we write responses
}

// Run reads frames from Stdin and proxies to URL; writes each response to Stdout.
// Returns when Stdin hits EOF or ctx is cancelled.
func Run(ctx context.Context, cfg RunConfig) error {
	if cfg.URL == "" {
		return errors.New("bridge: URL required")
	}
	if !strings.HasPrefix(cfg.URL, "http://") && !strings.HasPrefix(cfg.URL, "https://") {
		return fmt.Errorf("bridge: URL must be http(s)://, got %s", cfg.URL)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	scanner := bufio.NewScanner(cfg.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(line))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return err
		}
		body = bytes.TrimRight(body, "\r\n")
		// Notifications get HTTP 202 with no body. Don't emit a stdout frame —
		// per JSON-RPC, the server (us) MUST NOT respond to notifications.
		if len(body) == 0 {
			continue
		}
		body = append(body, '\n')
		if _, werr := cfg.Stdout.Write(body); werr != nil {
			return werr
		}
	}
	return scanner.Err()
}
