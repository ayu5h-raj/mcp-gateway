package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/clientcfg"
	"github.com/ayu5h-raj/mcp-gateway/internal/config"
	"github.com/ayu5h-raj/mcp-gateway/internal/service"
)

func newInitCmd() *cobra.Command {
	var (
		noImport  bool
		noPatch   bool
		noService bool
		force     bool
		assumeYes bool
		cfgPath   string
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "First-run wizard: detect MCP clients, migrate servers, install service",
		RunE: func(_ *cobra.Command, _ []string) error {
			if cfgPath == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				cfgPath = config.DefaultConfigPath(home)
			}
			gw, err := resolveGatewayBinary()
			if err != nil {
				return fmt.Errorf("resolve gateway binary: %w", err)
			}
			if err := refuseIfConfigured(cfgPath, force); err != nil {
				return err
			}
			fmt.Println("mcp-gateway init — first-run wizard")
			fmt.Println()
			imported, importedFromClients, err := importStep(cfgPath, gw, noImport, assumeYes)
			if err != nil {
				return err
			}
			if !noPatch {
				if err := patchStep(importedFromClients, gw, assumeYes); err != nil {
					return err
				}
			}
			if !noService {
				if err := serviceStep(gw, assumeYes); err != nil {
					return err
				}
			}
			printFooter(imported)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noImport, "no-import", false, "skip detection and write an empty config")
	cmd.Flags().BoolVar(&noPatch, "no-patch", false, "import but don't modify any client config")
	cmd.Flags().BoolVar(&noService, "no-service", false, "don't install the launchd auto-start service")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing non-empty config")
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "accept all prompts (non-interactive)")
	cmd.Flags().StringVar(&cfgPath, "config", "", "config destination (default ~/.mcp-gateway/config.jsonc)")
	return cmd
}

// refuseIfConfigured aborts when the target config already exists with
// non-empty mcpServers and --force was not passed.
func refuseIfConfigured(cfgPath string, force bool) error {
	if force {
		return nil
	}
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read existing config: %w", err)
	}
	if len(body) == 0 {
		return nil
	}
	cfg, err := config.ParseFile(cfgPath)
	if err != nil {
		// Existing file but unparseable: refuse to silently destroy it.
		return fmt.Errorf("existing config at %s is unparseable: %w (use --force to overwrite)", cfgPath, err)
	}
	if len(cfg.MCPServers) > 0 {
		return fmt.Errorf("mcp-gateway is already configured at %s with %d server(s). Use --force to overwrite (resets daemon settings too), or `mcp-gateway add` to add more", cfgPath, len(cfg.MCPServers))
	}
	return nil
}

func printFooter(imported int) {
	fmt.Println()
	if imported > 0 {
		fmt.Println("mcp-gateway is running. Restart any patched client to pick up the new config.")
	} else {
		fmt.Println("mcp-gateway is configured. Use `mcp-gateway add` to add servers.")
	}
	fmt.Println()
	fmt.Println("Useful commands:")
	fmt.Println("  mcp-gateway tui              live ops dashboard")
	fmt.Println("  mcp-gateway list             show servers and tool counts")
	fmt.Println("  mcp-gateway add <name> ...   add a new server")
	if runtime.GOOS == "darwin" {
		fmt.Println("  mcp-gateway service status   check the launchd service")
	}
}

type importedFromClient struct {
	client  clientcfg.Client
	servers []string // names imported (for the patch step)
}

// skipReason returns a non-empty string explaining why s should be excluded
// from import (and a friendly message printed in the discovery list), or
// "" if s is a normal stdio server we can import.
//
// Filters two cases:
//   - Empty Command: HTTP/SSE-transport entries (type:http with url+headers).
//     mcp-gateway v1 only proxies stdio downstream servers.
//   - Self-pointing Command: an entry whose Command resolves to the gateway
//     binary itself. Almost always a previously-patched mcpServers entry from
//     a prior `init` run; importing it would create a recursive supervisor →
//     gateway → supervisor loop. Resolves symlinks on both sides because
//     /opt/homebrew/bin/mcp-gateway is symlinked into the Cellar directory
//     and the patched config records one path while os.Executable returns
//     the other.
func skipReason(s clientcfg.Server, gw, clientName string) string {
	if s.Command == "" {
		return fmt.Sprintf("(HTTP transport — not yet supported by mcp-gateway, leave in %s)", clientName)
	}
	if samePath(s.Command, gw) {
		return "(self-pointing — already an mcp-gateway entry from a prior install, skipping)"
	}
	return ""
}

// samePath returns true when a and b refer to the same file on disk after
// symlink resolution and normalization. Best-effort: any error in resolution
// falls back to literal string comparison.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	resolveOrSelf := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	return resolveOrSelf(a) == resolveOrSelf(b)
}

// importableServers returns only the entries that survive skipReason.
func importableServers(in []clientcfg.Server, gw, clientName string) []clientcfg.Server {
	out := make([]clientcfg.Server, 0, len(in))
	for _, s := range in {
		if skipReason(s, gw, clientName) == "" {
			out = append(out, s)
		}
	}
	return out
}

func importStep(cfgPath, gw string, noImport, assumeYes bool) (int, []importedFromClient, error) {
	// Ensure the config directory exists. 0o700 — the file inside is 0o600
	// (server env may carry secrets); the parent dir is consistent with that.
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		return 0, nil, fmt.Errorf("mkdir config dir: %w", err)
	}
	// Build the new Config from the canonical defaults so future bumps to
	// config.DefaultDaemon() flow through the wizard automatically.
	cfg := &config.Config{
		Version:    config.Version,
		Daemon:     config.DefaultDaemon(),
		MCPServers: map[string]config.Server{},
	}
	var importedClients []importedFromClient
	totalImported := 0

	if !noImport {
		detected := clientcfg.Detect()
		if len(detected) == 0 {
			fmt.Println("No MCP clients detected. Writing an empty config.")
		} else {
			fmt.Println("Detected MCP clients:")
			for _, d := range detected {
				if d.Err != nil {
					fmt.Printf("  • %s — parse error: %v (skipping)\n", d.Client.Name, d.Err)
					continue
				}
				fmt.Printf("  • %s\n", d.Client.Name)
				if len(d.Servers) == 0 {
					fmt.Println("      (no servers configured)")
					continue
				}
				for _, s := range d.Servers {
					if reason := skipReason(s, gw, d.Client.Name); reason != "" {
						fmt.Printf("      %-15s — %s\n", s.Name, reason)
						continue
					}
					argsStr := ""
					if len(s.Args) > 0 {
						argsStr = " " + strings.Join(s.Args, " ")
					}
					fmt.Printf("      %-15s — %s%s\n", s.Name, s.Command, argsStr)
				}
			}
			fmt.Println()
			for _, d := range detected {
				if d.Err != nil || len(d.Servers) == 0 {
					continue
				}
				importable := importableServers(d.Servers, gw, d.Client.Name)
				if len(importable) == 0 {
					continue
				}
				prompt := fmt.Sprintf("Import %d server(s) from %s?", len(importable), d.Client.Name)
				if !confirm(prompt, true, assumeYes) {
					continue
				}
				names := make([]string, 0, len(importable))
				for _, s := range importable {
					if _, exists := cfg.MCPServers[s.Name]; exists {
						fmt.Printf("  ⚠ skipping %s (already in config)\n", s.Name)
						continue
					}
					cfg.MCPServers[s.Name] = config.Server{
						Command: s.Command,
						Args:    s.Args,
						Env:     s.Env,
						Enabled: true,
					}
					names = append(names, s.Name)
					totalImported++
				}
				importedClients = append(importedClients, importedFromClient{
					client:  d.Client,
					servers: names,
				})
			}
		}
	}

	// Write the config atomically.
	if err := writeFreshConfig(cfgPath, cfg); err != nil {
		return 0, nil, fmt.Errorf("write config: %w", err)
	}
	fmt.Printf("  ✓ wrote %s\n", cfgPath)
	return totalImported, importedClients, nil
}

// writeFreshConfig writes cfg to path atomically, mirroring the
// internal/configwrite pattern: validate → write → chmod-on-handle →
// sync → close → rename. Sync ensures the bytes hit disk before the
// rename publishes the new file (prevents partial-config-on-crash).
func writeFreshConfig(path string, cfg *config.Config) error {
	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	body = append(body, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config.tmp.*")
	if err != nil {
		return fmt.Errorf("tempfile: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once Rename succeeds
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func patchStep(imported []importedFromClient, gw string, assumeYes bool) error {
	if len(imported) == 0 {
		return nil
	}
	for _, ifc := range imported {
		if len(ifc.servers) == 0 {
			continue
		}
		prompt := fmt.Sprintf("Patch %s's config to point at the gateway?", ifc.client.Name)
		if !confirm(prompt, true, assumeYes) {
			fmt.Printf("  Skipped %s. To do it manually, replace its mcpServers with:\n", ifc.client.Name)
			fmt.Printf("    \"mcp-gateway\": { \"command\": %q, \"args\": [\"stdio\"] }\n", gw)
			continue
		}
		if err := clientcfg.Patch(ifc.client, ifc.servers, gw); err != nil {
			return fmt.Errorf("patch %s: %w", ifc.client.Name, err)
		}
		fmt.Printf("  ✓ backed up + patched %s\n", ifc.client.ConfigPath)
	}
	return nil
}

func serviceStep(gw string, assumeYes bool) error {
	if runtime.GOOS != "darwin" {
		fmt.Println("Skipping auto-start: macOS only for v1.0 (run `mcp-gateway start` from your shell rc on Linux).")
		return nil
	}
	if !confirm("Auto-start mcp-gateway on login (recommended)?", true, assumeYes) {
		fmt.Println("  Skipped. You can install the service later with `mcp-gateway service install`.")
		return nil
	}
	if err := service.Install(gw); err != nil {
		// Non-fatal: the wizard already wrote the config and patched the
		// client(s); failing the whole `init` here would leave the user
		// thinking nothing worked. Print the error + the workaround and
		// let the footer remind them how to use the daemon.
		fmt.Printf("  ⚠ service install failed: %v\n", err)
		fmt.Println("  The config is written and the daemon is otherwise fine — just no auto-start.")
		fmt.Println("  Run `mcp-gateway start` to start it now, then `mcp-gateway service install` later to retry auto-start.")
		return nil
	}
	fmt.Println("  ✓ launchd service installed and loaded")
	return nil
}
