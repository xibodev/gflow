package util

import (
	"os"
	"strings"
)

// isSecretEnvVar returns true if the environment variable name matches
// gflow-private secret patterns that must never leak into child processes.
// Test helper vars (GFLOW_ADAPTER_HELPER_*) are allowed through.
func isSecretEnvVar(name string) bool {
	upper := strings.ToUpper(name)
	if strings.HasPrefix(upper, "GFLOW_ADAPTER_HELPER_") {
		return false
	}
	if strings.HasPrefix(upper, "GFLOW_ADAPTER_") {
		return true
	}
	if strings.HasPrefix(upper, "GFLOW_") && strings.HasSuffix(upper, "_TOKEN") {
		return true
	}
	return false
}

// ChildEnv returns the current environment minus gflow-private secrets, for
// child processes gflow starts that are not the configured adapter.
func ChildEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if !isSecretEnvVar(name) {
			out = append(out, kv)
		}
	}
	return out
}
