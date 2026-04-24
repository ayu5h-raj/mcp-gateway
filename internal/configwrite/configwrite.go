// Package configwrite atomically mutates the on-disk config.jsonc.
//
// The flow is: read → parse → mutate → validate → write to tmp → atomic
// rename. JSONC comments are LOST on round-trip — the file is re-emitted as
// indented JSON. Documented trade-off vs. building a comment-preserving JSONC
// AST rewriter, which would be substantial engineering for cosmetic value.
package configwrite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ayu5h-raj/mcp-gateway/internal/config"
)

// Apply parses cfgPath, runs mutate on the parsed Config, validates the
// result, then writes the file atomically (tmp + rename). On any failure
// the original file is untouched.
func Apply(cfgPath string, mutate func(*config.Config) error) error {
	cfg, err := config.ParseFile(cfgPath)
	if err != nil {
		return fmt.Errorf("configwrite: parse: %w", err)
	}
	if err := mutate(cfg); err != nil {
		return fmt.Errorf("configwrite: mutate: %w", err)
	}
	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("configwrite: validate: %w", err)
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("configwrite: marshal: %w", err)
	}
	out = append(out, '\n')

	dir := filepath.Dir(cfgPath)
	tmp, err := os.CreateTemp(dir, ".config.jsonc.tmp.*")
	if err != nil {
		return fmt.Errorf("configwrite: tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath) // no-op if already renamed
	}()
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("configwrite: write: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("configwrite: chmod: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("configwrite: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("configwrite: close: %w", err)
	}
	if err := os.Rename(tmpPath, cfgPath); err != nil {
		return fmt.Errorf("configwrite: rename: %w", err)
	}
	return nil
}
