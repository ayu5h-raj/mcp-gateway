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
			imported, importedFromClients, err := importStep(cfgPath, noImport, assumeYes)
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

// stdioServers returns only those entries whose Command is set. HTTP/SSE
// transport entries (which have no Command, only a url + headers) are
// filtered out — mcp-gateway v1 only proxies stdio downstream servers.
func stdioServers(in []clientcfg.Server) []clientcfg.Server {
	out := make([]clientcfg.Server, 0, len(in))
	for _, s := range in {
		if s.Command != "" {
			out = append(out, s)
		}
	}
	return out
}

func importStep(cfgPath string, noImport, assumeYes bool) (int, []importedFromClient, error) {
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
					if s.Command == "" {
						// Almost certainly an HTTP/SSE-transport entry
						// (type: http with url + headers). mcp-gateway v1
						// only proxies stdio downstream servers; surface
						// the entry as unsupported so the user knows
						// nothing is silently dropped, and leave it in
						// the client's own config.
						fmt.Printf("      %-15s — (HTTP transport — not yet supported by mcp-gateway, leave in %s)\n", s.Name, d.Client.Name)
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
				supported := stdioServers(d.Servers)
				if len(supported) == 0 {
					continue
				}
				prompt := fmt.Sprintf("Import %d server(s) from %s?", len(supported), d.Client.Name)
				if !confirm(prompt, true, assumeYes) {
					continue
				}
				names := make([]string, 0, len(supported))
				for _, s := range supported {
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
		return fmt.Errorf("service install: %w", err)
	}
	fmt.Println("  ✓ launchd service installed and loaded")
	return nil
}
