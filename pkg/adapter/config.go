package adapter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	EnvCommand  = "GFLOW_ADAPTER_COMMAND"
	EnvArgsJSON = "GFLOW_ADAPTER_ARGS_JSON"
	EnvEnvJSON  = "GFLOW_ADAPTER_ENV_JSON"
)

// ConfigFromEnv loads the explicit process configuration for an external
// adapter. The returned bool is false when no adapter command was configured.
func ConfigFromEnv(coreVersion string) (Config, bool, error) {
	command := os.Getenv(EnvCommand)
	if command == "" {
		if os.Getenv(EnvArgsJSON) != "" || os.Getenv(EnvEnvJSON) != "" {
			return Config{}, false, fmt.Errorf("%s is required when adapter arguments or environment are configured", EnvCommand)
		}
		return Config{}, false, nil
	}
	if !filepath.IsAbs(command) {
		return Config{}, false, fmt.Errorf("%s must be an absolute path", EnvCommand)
	}

	var args []string
	if raw := os.Getenv(EnvArgsJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return Config{}, false, fmt.Errorf("parse %s as a JSON string array: %w", EnvArgsJSON, err)
		}
		if args == nil {
			return Config{}, false, fmt.Errorf("%s must be a JSON string array", EnvArgsJSON)
		}
		for _, arg := range args {
			if strings.IndexByte(arg, 0) >= 0 {
				return Config{}, false, fmt.Errorf("%s values must not contain NUL", EnvArgsJSON)
			}
		}
	}

	var env []string
	if raw := os.Getenv(EnvEnvJSON); raw != "" {
		values := map[string]string{}
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return Config{}, false, fmt.Errorf("parse %s as a JSON object with string values: %w", EnvEnvJSON, err)
		}
		if values == nil {
			return Config{}, false, fmt.Errorf("%s must be a JSON object with string values", EnvEnvJSON)
		}
		keys := make([]string, 0, len(values))
		for key := range values {
			if key == "" || strings.ContainsAny(key, "=\x00") || strings.IndexByte(values[key], 0) >= 0 {
				return Config{}, false, fmt.Errorf("%s contains an invalid environment entry", EnvEnvJSON)
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if !isBlockedEnvVar(key) {
				env = append(env, key+"="+values[key])
			}
		}
	}

	return Config{Command: command, Args: args, Env: env, CoreVersion: coreVersion}, true, nil
}
