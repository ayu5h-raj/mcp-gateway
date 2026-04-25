package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayu5h-raj/mcp-gateway/internal/clientcfg"
	"github.com/ayu5h-raj/mcp-gateway/internal/config"
)

func TestImportableServers_FiltersHTTPAndSelfPointing(t *testing.T) {
	gw := "/opt/homebrew/bin/mcp-gateway"
	in := []clientcfg.Server{
		{Name: "kite", Command: "npx", Args: []string{"mcp-remote", "https://x"}, Enabled: true},
		{Name: "n8n-mcp", Enabled: true}, // HTTP transport: no Command
		{Name: "hostinger-mcp", Command: "npx", Args: []string{"hostinger-api-mcp@latest"}, Enabled: true},
		// Self-pointing — left over from a previous mcp-gateway init run.
		{Name: "mcp-gateway", Command: gw, Args: []string{"stdio"}, Enabled: true},
	}
	got := importableServers(in, gw, "Cursor")
	if len(got) != 2 {
		t.Fatalf("want 2 importable servers (kite + hostinger-mcp), got %d: %#v", len(got), got)
	}
	names := map[string]bool{}
	for _, s := range got {
		names[s.Name] = true
	}
	if names["n8n-mcp"] {
		t.Fatal("n8n-mcp (HTTP) should have been filtered out")
	}
	if names["mcp-gateway"] {
		t.Fatal("mcp-gateway (self-pointing) should have been filtered out — would cause supervisor recursion")
	}
}

func TestSkipReason_SelfPointingFlaggedDistinctlyFromHTTP(t *testing.T) {
	gw := "/opt/homebrew/bin/mcp-gateway"
	httpEntry := clientcfg.Server{Name: "n8n", Enabled: true}
	selfEntry := clientcfg.Server{Name: "mcp-gateway", Command: gw, Args: []string{"stdio"}, Enabled: true}
	stdioEntry := clientcfg.Server{Name: "kite", Command: "npx", Args: []string{"x"}, Enabled: true}

	if r := skipReason(httpEntry, gw, "Cursor"); r == "" || !strings.Contains(r, "HTTP transport") {
		t.Fatalf("HTTP entry should be flagged with HTTP message, got %q", r)
	}
	if r := skipReason(selfEntry, gw, "Cursor"); r == "" || !strings.Contains(r, "self-pointing") {
		t.Fatalf("self-pointing entry should be flagged distinctly, got %q", r)
	}
	if r := skipReason(stdioEntry, gw, "Cursor"); r != "" {
		t.Fatalf("normal stdio entry should not be skipped, got %q", r)
	}
}

func TestInit_NoImportNoServiceNoPatch(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.jsonc")
	// Direct call into the import step at the package level.
	imported, _, err := importStep(cfgPath, "/opt/homebrew/bin/mcp-gateway" /*gw*/, true /*noImport*/, true /*assumeYes*/)
	if err != nil {
		t.Fatalf("importStep: %v", err)
	}
	if imported != 0 {
		t.Fatalf("expected 0 imports, got %d", imported)
	}
	// Verify the config was written and parses.
	cfg, err := config.ParseFile(cfgPath)
	if err != nil {
		t.Fatalf("parse written config: %v", err)
	}
	if cfg.Daemon.HTTPPort != 7823 {
		t.Fatalf("default port not set: %#v", cfg.Daemon)
	}
	if len(cfg.MCPServers) != 0 {
		t.Fatalf("expected 0 servers, got %d", len(cfg.MCPServers))
	}
}

func TestRefuseIfConfigured_SkipsEmptyMCPServers(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.jsonc")
	body := []byte(`{"version":1,"daemon":{"http_port":7823,"log_level":"info"},"mcpServers":{}}`)
	if err := os.WriteFile(cfgPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refuseIfConfigured(cfgPath, false); err != nil {
		t.Fatalf("should NOT refuse on empty mcpServers: %v", err)
	}
}

func TestRefuseIfConfigured_RefusesNonEmpty(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.jsonc")
	body := []byte(`{"version":1,"daemon":{"http_port":7823,"log_level":"info"},"mcpServers":{"x":{"command":"echo","enabled":true}}}`)
	if err := os.WriteFile(cfgPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refuseIfConfigured(cfgPath, false); err == nil {
		t.Fatal("should refuse on non-empty mcpServers")
	}
	// --force overrides
	if err := refuseIfConfigured(cfgPath, true); err != nil {
		t.Fatalf("--force should override: %v", err)
	}
}
