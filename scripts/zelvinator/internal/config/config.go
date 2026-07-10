// Package config loads the zelvinator bot configuration.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds the bot's configuration.
type Config struct {
	WhitelistUsers []string
	TargetOrgs     []string
	HermesEnvPath  string
	ScriptDir      string
}

// Load reads config from config.sh (sourced as shell script — we parse it).
func Load() (*Config, error) {
	// Determine script directory (same as where the binary lives, but config is one level up)
	execPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot determine executable path: %w", err)
	}
	scriptDir := filepath.Dir(filepath.Dir(execPath)) // scripts/zelvinator/ -> scripts/ -> repo root
	// Check if we're in scripts/zelvinator/ or scripts/
	// config.sh should be in the repo root
	repoRoot := scriptDir

	// Try to find config.sh
	configPath := filepath.Join(repoRoot, "config.sh")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Maybe we're running from scripts/ directly
		configPath = filepath.Join(repoRoot, "..", "config.sh")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("config.sh not found (tried %s and %s)",
				filepath.Join(repoRoot, "config.sh"),
				filepath.Join(repoRoot, "..", "config.sh"))
		}
		repoRoot = filepath.Dir(filepath.Dir(configPath))
	}

	cfg := &Config{
		ScriptDir: repoRoot,
	}
	if err := cfg.parseConfigFile(configPath); err != nil {
		return nil, err
	}

	// Default HERMES_ENV
	if cfg.HermesEnvPath == "" {
		home, _ := os.UserHomeDir()
		cfg.HermesEnvPath = filepath.Join(home, ".hermes", ".env")
	}

	return cfg, nil
}

// parseConfigFile reads the bash-style config.sh and extracts array variables.
func (c *Config) parseConfigFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open config: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var currentVar string
	var currentValues []string

	flush := func() {
		if currentVar == "" {
			return
		}
		switch currentVar {
		case "WHITELIST_USERS":
			c.WhitelistUsers = currentValues
		case "TARGET_ORGS":
			c.TargetOrgs = currentValues
		case "HERMES_ENV":
			if len(currentValues) > 0 {
				c.HermesEnvPath = currentValues[0]
			}
		}
		currentVar = ""
		currentValues = nil
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Detect new array assignment: VAR_NAME=(
		if strings.HasPrefix(line, "WHITELIST_USERS=(") || strings.HasPrefix(line, "TARGET_ORGS=(") {
			flush()
			if strings.HasPrefix(line, "WHITELIST_USERS=(") {
				currentVar = "WHITELIST_USERS"
			} else {
				currentVar = "TARGET_ORGS"
			}
			// Check if values are on the same line
			rest := strings.TrimPrefix(line, currentVar+"=(")
			rest = strings.TrimRight(rest, " ")
			if strings.HasSuffix(rest, ")") {
				rest = strings.TrimSuffix(rest, ")")
				vals := parseBashArrayValues(rest)
				currentValues = append(currentValues, vals...)
				flush()
			} else if rest != "" {
				vals := parseBashArrayValues(rest)
				currentValues = append(currentValues, vals...)
			}
			continue
		}

		// End of array
		if line == ")" && currentVar != "" {
			flush()
			continue
		}

		// Inside array
		if currentVar != "" {
			vals := parseBashArrayValues(line)
			currentValues = append(currentValues, vals...)
			continue
		}

		// Simple variable: HERMES_ENV="..."
		if strings.HasPrefix(line, "HERMES_ENV=") {
			flush()
			val := strings.TrimPrefix(line, "HERMES_ENV=")
			val = strings.Trim(val, " 	")
			// Strip surrounding quotes
			if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) ||
				(strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
				val = val[1 : len(val)-1]
			}
			// Resolve bash variable expressions
			val = resolveBashVar(val)
			c.HermesEnvPath = val
			continue
		}
	}

	flush()
	return scanner.Err()
}

// parseBashArrayValues extracts quoted values from a bash array line.
func parseBashArrayValues(s string) []string {
	var vals []string
	s = strings.TrimSpace(s)
	// Remove wrapping parens if present
	s = strings.TrimPrefix(s, "(")
	s = strings.TrimSuffix(s, ")")

	for {
		s = strings.TrimSpace(s)
		if s == "" {
			break
		}
		if strings.HasPrefix(s, "\"") {
			// Quoted value
			end := strings.Index(s[1:], "\"")
			if end < 0 {
				break
			}
			vals = append(vals, s[1:end+1])
			s = s[end+2:]
		} else if strings.HasPrefix(s, "'") {
			end := strings.Index(s[1:], "'")
			if end < 0 {
				break
			}
			vals = append(vals, s[1:end+1])
			s = s[end+2:]
		} else {
			// Unquoted value (up to space or newline)
			end := strings.Index(s, " ")
			if end < 0 {
				end = len(s)
			}
			vals = append(vals, s[:end])
			s = s[end:]
		}
	}
	return vals
}

// LoadEnv reads GITHUB_TOKEN from the Hermes .env file.
func (c *Config) LoadEnv() (string, error) {
	data, err := os.ReadFile(c.HermesEnvPath)
	if err != nil {
		return "", fmt.Errorf("cannot read env file %s: %w", c.HermesEnvPath, err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "GITHUB_TOKEN=") {
			val := strings.TrimPrefix(line, "GITHUB_TOKEN=")
			val = strings.Trim(val, "\"'")
			if val != "" {
				return val, nil
			}
		}
	}
	return "", fmt.Errorf("GITHUB_TOKEN not found in %s", c.HermesEnvPath)
}

// resolveBashVar resolves simple bash variable expressions like ${VAR:-default}.
func resolveBashVar(val string) string {
	// First check for ${VAR:-default} or ${VAR-$default} pattern
	for strings.Contains(val, "${") {
		start := strings.Index(val, "${")
		end := strings.Index(val[start:], "}")
		if end < 0 {
			break
		}
		end = start + end + 1
		expr := val[start:end]
		inner := expr[2 : len(expr)-1] // strip ${ }

		var resolved string
		if strings.Contains(inner, ":-") {
			parts := strings.SplitN(inner, ":-", 2)
			varName := parts[0]
			defaultVal := parts[1]
			envVal := os.Getenv(varName)
			if envVal != "" {
				resolved = envVal
			} else {
				resolved = os.ExpandEnv(defaultVal)
			}
		} else if strings.Contains(inner, "-") {
			parts := strings.SplitN(inner, "-", 2)
			varName := parts[0]
			defaultVal := parts[1]
			envVal := os.Getenv(varName)
			if envVal != "" {
				resolved = envVal
			} else {
				resolved = os.ExpandEnv(defaultVal)
			}
		} else {
			resolved = os.Getenv(inner)
		}
		val = val[:start] + resolved + val[end+1:]
	}
	// Also expand bare $VAR references
	val = os.ExpandEnv(val)
	return val
}
