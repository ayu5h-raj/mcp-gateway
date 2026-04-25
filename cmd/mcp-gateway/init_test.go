package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ayu5h-raj/mcp-gateway/internal/config"
)

func TestInit_NoImportNoServiceNoPatch(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.jsonc")
	// Direct call into the import step at the package level.
	imported, _, err := importStep(cfgPath, true /*noImport*/, true /*assumeYes*/)
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
