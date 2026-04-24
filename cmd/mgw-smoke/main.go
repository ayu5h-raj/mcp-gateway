// Command mgw-smoke is a small MCP client that drives the gateway through
// the standard initialize → notifications/initialized → tools/list lifecycle
// (and optionally a tools/call), reporting PASS/FAIL for each step. Useful
// for verifying that the gateway behaves correctly toward an MCP client.
//
// Usage:
//
//	mgw-smoke --gateway ./bin/mcp-gateway --port 7823
//	mgw-smoke --gateway ./bin/mcp-gateway --port 7823 --call <tool> [--args '{...}']
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const (
	colorReset = "\033[0m"
	colorRed   = "\033[31m"
	colorGreen = "\033[32m"
	colorCyan  = "\033[36m"
	colorDim   = "\033[2m"
)

type smoke struct {
	stdin   io.WriteCloser
	stdoutC chan string
	cmd     *exec.Cmd
	verbose bool
	failed  bool
}

func main() {
	if code := run(); code != 0 {
		os.Exit(code)
	}
}

// run is the body of main wrapped so deferred cleanups (shutdown, cancel)
// run before any os.Exit. Returns the process exit code (0 on success).
func run() int {
	gateway := flag.String("gateway", "./bin/mcp-gateway", "path to mcp-gateway binary")
	port := flag.Int("port", 7823, "daemon HTTP port (mcp-gateway daemon must be running)")
	verbose := flag.Bool("v", false, "print every frame sent and received")
	callTool := flag.String("call", "", "if set, call this tool after tools/list")
	callArgs := flag.String("args", "{}", "JSON arguments object for --call (default {})")
	timeout := flag.Duration("timeout", 10*time.Second, "overall timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	s, err := startBridge(ctx, *gateway, *port, *verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sFATAL%s start bridge: %v\n", colorRed, colorReset, err)
		return 2
	}
	defer s.shutdown()

	// 1. initialize
	resp := s.requestExpect(ctx, "initialize", 1, map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "mgw-smoke", "version": "0"},
	})
	if resp == nil {
		s.fail("initialize", "no response (daemon may not be running on port %d)", *port)
	} else if _, ok := resp["result"]; !ok {
		s.fail("initialize", "response missing result: %v", resp)
	} else {
		result := resp["result"].(map[string]any)
		serverInfo, _ := result["serverInfo"].(map[string]any)
		name, _ := serverInfo["name"].(string)
		version, _ := serverInfo["version"].(string)
		s.pass("initialize", fmt.Sprintf("server=%s/%s", name, version))
	}

	// 2. notifications/initialized — MUST NOT receive a reply.
	s.notify(ctx, "notifications/initialized", nil)
	if frame, got := s.tryRead(800 * time.Millisecond); got {
		s.fail("notifications/initialized",
			"server replied to a notification (this would break Claude Desktop): %s", frame)
	} else {
		s.pass("notifications/initialized", "no reply (correct)")
	}

	// 3. tools/list
	resp = s.requestExpect(ctx, "tools/list", 2, nil)
	var tools []map[string]any
	if resp == nil {
		s.fail("tools/list", "no response")
	} else if errObj, ok := resp["error"].(map[string]any); ok {
		s.fail("tools/list", "error response: %v", errObj)
	} else {
		result := resp["result"].(map[string]any)
		raw, _ := result["tools"].([]any)
		for _, t := range raw {
			tools = append(tools, t.(map[string]any))
		}
		names := make([]string, 0, len(tools))
		for _, t := range tools {
			names = append(names, t["name"].(string))
		}
		summary := fmt.Sprintf("%d tools", len(tools))
		if len(tools) > 0 {
			preview := names
			if len(preview) > 5 {
				preview = preview[:5]
			}
			summary += ": " + strings.Join(preview, ", ")
			if len(names) > 5 {
				summary += fmt.Sprintf(", … (%d more)", len(names)-5)
			}
		}
		s.pass("tools/list", summary)
	}

	// 4. resources/list (best-effort — most servers don't implement this)
	resp = s.requestExpect(ctx, "resources/list", 3, nil)
	if resp != nil {
		if errObj, ok := resp["error"].(map[string]any); ok {
			s.pass("resources/list", fmt.Sprintf("error %v (likely not supported)", errObj["code"]))
		} else {
			result := resp["result"].(map[string]any)
			rs, _ := result["resources"].([]any)
			s.pass("resources/list", fmt.Sprintf("%d resources", len(rs)))
		}
	}

	// 5. prompts/list (same)
	resp = s.requestExpect(ctx, "prompts/list", 4, nil)
	if resp != nil {
		if errObj, ok := resp["error"].(map[string]any); ok {
			s.pass("prompts/list", fmt.Sprintf("error %v (likely not supported)", errObj["code"]))
		} else {
			result := resp["result"].(map[string]any)
			ps, _ := result["prompts"].([]any)
			s.pass("prompts/list", fmt.Sprintf("%d prompts", len(ps)))
		}
	}

	// 6. ping (request — must get a response)
	resp = s.requestExpect(ctx, "ping", 5, nil)
	if resp == nil {
		s.fail("ping", "no response")
	} else if _, ok := resp["result"]; !ok {
		s.fail("ping", "response missing result: %v", resp)
	} else {
		s.pass("ping", "ack")
	}

	// 7. Optionally invoke a tool.
	if *callTool != "" {
		var args any
		if err := json.Unmarshal([]byte(*callArgs), &args); err != nil {
			fmt.Fprintf(os.Stderr, "%sFATAL%s invalid --args JSON: %v\n", colorRed, colorReset, err)
			return 2
		}
		resp = s.requestExpect(ctx, "tools/call", 6, map[string]any{
			"name":      *callTool,
			"arguments": args,
		})
		if resp == nil {
			s.fail("tools/call "+*callTool, "no response")
		} else if errObj, ok := resp["error"].(map[string]any); ok {
			s.fail("tools/call "+*callTool, "error: %v", errObj)
		} else {
			result := resp["result"].(map[string]any)
			b, _ := json.MarshalIndent(result, "  ", "  ")
			s.pass("tools/call "+*callTool, "isError="+fmt.Sprint(result["isError"]))
			fmt.Printf("  %sresult:%s\n  %s\n", colorDim, colorReset, string(b))
		}
	}

	// 8. notifications/cancelled with a fake id — also must not reply.
	s.notify(ctx, "notifications/cancelled", map[string]any{"requestId": "9999"})
	if frame, got := s.tryRead(400 * time.Millisecond); got {
		s.fail("notifications/cancelled", "server replied to a notification: %s", frame)
	} else {
		s.pass("notifications/cancelled", "no reply (correct)")
	}

	if s.failed {
		fmt.Printf("\n%sSMOKE FAILED%s\n", colorRed, colorReset)
		return 1
	}
	fmt.Printf("\n%sSMOKE PASSED%s — gateway behaves correctly to an MCP client\n", colorGreen, colorReset)
	return 0
}

// startBridge spawns `mcp-gateway stdio --port <port>` and wires its stdio.
func startBridge(ctx context.Context, gateway string, port int, verbose bool) (*smoke, error) {
	if _, err := os.Stat(gateway); err != nil {
		return nil, fmt.Errorf("gateway binary not found at %s: %w", gateway, err)
	}
	cmd := exec.CommandContext(ctx, gateway, "stdio", "--port", fmt.Sprint(port))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Forward Ctrl-C to the child.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}()

	stdoutC := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
		for sc.Scan() {
			stdoutC <- sc.Text()
		}
		close(stdoutC)
	}()

	return &smoke{
		stdin:   stdin,
		stdoutC: stdoutC,
		cmd:     cmd,
		verbose: verbose,
	}, nil
}

func (s *smoke) shutdown() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(syscall.SIGTERM)
		_, _ = s.cmd.Process.Wait()
	}
}

func (s *smoke) send(_ context.Context, method string, id any, params map[string]any) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if id != nil {
		msg["id"] = id
	}
	if params != nil {
		msg["params"] = params
	}
	b, _ := json.Marshal(msg)
	if s.verbose {
		fmt.Printf("%s→ %s%s\n", colorCyan, string(b), colorReset)
	}
	_, _ = s.stdin.Write(append(b, '\n'))
}

func (s *smoke) requestExpect(ctx context.Context, method string, id any, params map[string]any) map[string]any {
	s.send(ctx, method, id, params)
	frame, ok := s.tryRead(3 * time.Second)
	if !ok {
		return nil
	}
	if s.verbose {
		fmt.Printf("%s← %s%s\n", colorDim, frame, colorReset)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(frame), &resp); err != nil {
		s.fail(method, "response is not JSON: %s", frame)
		return nil
	}
	if resp["jsonrpc"] != "2.0" {
		s.fail(method, "response missing or wrong jsonrpc field: %v", resp["jsonrpc"])
	}
	return resp
}

func (s *smoke) notify(ctx context.Context, method string, params map[string]any) {
	s.send(ctx, method, nil, params)
}

func (s *smoke) tryRead(timeout time.Duration) (string, bool) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case line, ok := <-s.stdoutC:
		if !ok {
			return "", false
		}
		return line, true
	case <-t.C:
		return "", false
	}
}

func (s *smoke) pass(name, info string) {
	fmt.Printf("%sPASS%s %s %s%s%s\n", colorGreen, colorReset, name, colorDim, info, colorReset)
}

func (s *smoke) fail(name, format string, args ...any) {
	s.failed = true
	fmt.Printf("%sFAIL%s %s — %s\n", colorRed, colorReset, name, fmt.Sprintf(format, args...))
}
