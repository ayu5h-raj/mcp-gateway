package clientcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// readClaudeDesktop parses the Claude Desktop config at path and returns
// the listed MCP servers. Returns ErrConfigMissing wrapped if path doesn't
// exist; other I/O or parse errors are returned wrapped with context.
func readClaudeDesktop(path string) ([]Server, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s: %w", path, ErrConfigMissing)
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(body) == 0 {
		return nil, nil
	}
	var raw struct {
		MCPServers map[string]Server `json:"mcpServers"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := make([]Server, 0, len(raw.MCPServers))
	for name, s := range raw.MCPServers {
		s.Name = name
		s.Enabled = true
		out = append(out, s)
	}
	return out, nil
}

// patchClaudeDesktop reads path, removes named servers, inserts an
// "mcp-gateway" stdio entry, writes via tmp+rename. Backs up the original
// to <path>.bak.<YYYYMMDD-HHMMSS> first.
func patchClaudeDesktop(path string, removedServers []string, gatewayBinary string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	// Resolve symlinks so we rewrite the actual backing file, not replace the
	// symlink with a regular file. Dotfile managers (chezmoi, stow, mackup)
	// commonly symlink Claude Desktop's config to a version-controlled location.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	// Parse into a generic map so we preserve unknown top-level keys verbatim.
	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if top == nil {
		top = map[string]any{}
	}
	srvsAny, _ := top["mcpServers"].(map[string]any)
	if srvsAny == nil {
		srvsAny = map[string]any{}
	}
	for _, name := range removedServers {
		delete(srvsAny, name)
	}
	srvsAny["mcp-gateway"] = map[string]any{
		"command": gatewayBinary,
		"args":    []any{"stdio"},
	}
	top["mcpServers"] = srvsAny

	// Backup first.
	bakPath := backupPath(path, time.Now())
	if err := os.WriteFile(bakPath, body, 0o600); err != nil {
		return fmt.Errorf("backup %s: %w", bakPath, err)
	}

	// Write atomically.
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	out = append(out, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".clientcfg.tmp.*")
	if err != nil {
		return fmt.Errorf("tmp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// backupPath returns a unique backup filename. If a file already exists at
// the natural <path>.bak.<timestamp>, append -1, -2, etc.
func backupPath(path string, now time.Time) string {
	base := fmt.Sprintf("%s.bak.%s", path, now.Format("20060102-150405"))
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return base
	}
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	// Extreme edge case: 1000 backups in the same second. Just overwrite the natural one.
	return base
}
