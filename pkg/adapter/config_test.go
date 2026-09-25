package adapter_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xibodev/gflow/pkg/adapter"
)

func clearAdapterEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{adapter.EnvCommand, adapter.EnvArgsJSON, adapter.EnvEnvJSON} {
		t.Setenv(key, "")
	}
}

func TestConfigFromEnv(t *testing.T) {
	clearAdapterEnv(t)
	command := filepath.Join(t.TempDir(), "adapter")
	t.Setenv(adapter.EnvCommand, command)
	t.Setenv(adapter.EnvArgsJSON, `["--mode","image"]`)
	t.Setenv(adapter.EnvEnvJSON, `{"TOKEN":"secret","MODE":"test"}`)

	cfg, configured, err := adapter.ConfigFromEnv("test-core")
	if err != nil {
		t.Fatal(err)
	}
	if !configured || cfg.Command != command || cfg.CoreVersion != "test-core" {
		t.Fatalf("config = %+v, configured=%v", cfg, configured)
	}
	if !reflect.DeepEqual(cfg.Args, []string{"--mode", "image"}) {
		t.Fatalf("args = %#v", cfg.Args)
	}
	if !reflect.DeepEqual(cfg.Env, []string{"MODE=test", "TOKEN=secret"}) {
		t.Fatalf("environment = %#v", cfg.Env)
	}
}

func TestConfigFromEnvRejectsMalformedConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    string
		env     string
		want    string
	}{
		{name: "orphan args", args: `[]`, want: adapter.EnvCommand},
		{name: "relative command", command: "adapter", want: "absolute"},
		{name: "args object", command: filepath.Join(t.TempDir(), "adapter"), args: `{}`, want: "string array"},
		{name: "args null", command: filepath.Join(t.TempDir(), "adapter"), args: `null`, want: "string array"},
		{name: "environment array", command: filepath.Join(t.TempDir(), "adapter"), env: `[]`, want: "JSON object"},
		{name: "environment null", command: filepath.Join(t.TempDir(), "adapter"), env: `null`, want: "JSON object"},
		{name: "environment non-string", command: filepath.Join(t.TempDir(), "adapter"), env: `{"PORT":1}`, want: "string values"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearAdapterEnv(t)
			t.Setenv(adapter.EnvCommand, test.command)
			t.Setenv(adapter.EnvArgsJSON, test.args)
			t.Setenv(adapter.EnvEnvJSON, test.env)
			_, _, err := adapter.ConfigFromEnv("test-core")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want text %q", err, test.want)
			}
		})
	}
}

func TestConfigFromEnvIsUnconfiguredWithoutVariables(t *testing.T) {
	clearAdapterEnv(t)
	_, configured, err := adapter.ConfigFromEnv("test-core")
	if err != nil || configured {
		t.Fatalf("configured=%v error=%v", configured, err)
	}
}
