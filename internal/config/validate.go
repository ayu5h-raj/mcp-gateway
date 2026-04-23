package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// validServerName matches "[A-Za-z0-9_-]{1,64}".
var validServerName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

var allowedLogLevels = map[string]struct{}{
	"debug": {}, "info": {}, "warn": {}, "error": {},
}

// Validate checks invariants on a parsed Config. It applies no defaults —
// call after Parse (which has already defaulted Daemon fields).
func Validate(c *Config) error {
	if c == nil {
		return errors.New("config is nil")
	}
	if c.Version != Version {
		return fmt.Errorf("unsupported config version %d (expected %d)", c.Version, Version)
	}
	if c.Daemon.HTTPPort < 1 || c.Daemon.HTTPPort > 65535 {
		return fmt.Errorf("daemon.http_port %d out of range", c.Daemon.HTTPPort)
	}
	if _, ok := allowedLogLevels[c.Daemon.LogLevel]; !ok {
		return fmt.Errorf("daemon.log_level %q must be one of debug|info|warn|error", c.Daemon.LogLevel)
	}

	seenPrefix := map[string]string{}
	for name, s := range c.McpServers {
		if !validServerName.MatchString(name) {
			return fmt.Errorf("server name %q invalid: must match [A-Za-z0-9_-]{1,64}", name)
		}
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("server %q: command must be set", name)
		}
		// Explicit empty/whitespace prefix is forbidden (collision footgun).
		// If not set at all, EffectivePrefix will use the server name.
		if s.Prefix != "" && strings.TrimSpace(s.Prefix) == "" {
			return fmt.Errorf("server %q: prefix must not be blank/whitespace when set", name)
		}
		p := EffectivePrefix(name, s)
		if !validServerName.MatchString(p) {
			return fmt.Errorf("server %q: effective prefix %q invalid", name, p)
		}
		if prev, ok := seenPrefix[p]; ok {
			return fmt.Errorf("server %q: duplicate prefix %q already used by %q", name, p, prev)
		}
		seenPrefix[p] = name
	}
	return nil
}
