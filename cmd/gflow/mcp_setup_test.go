package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigureOpenCode(t *testing.T) {
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "opencode.json")

	// Pre-seed with existing config
	initial := map[string]any{
		"username": "tester",
		"mcp": map[string]any{
			"other": map[string]any{
				"type": "local",
			},
		},
	}
	b, _ := json.Marshal(initial)
	if err := os.WriteFile(cfgFile, b, 0644); err != nil {
		t.Fatal(err)
	}

	if err := configureOpenCode(cfgFile, "C:\\test\\gflow.exe"); err != nil {
		t.Fatalf("configureOpenCode failed: %v", err)
	}

	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed["username"] != "tester" {
		t.Errorf("expected username 'tester', got %v", parsed["username"])
	}

	mcpMap, ok := parsed["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("expected mcp map, got %T", parsed["mcp"])
	}

	if _, ok := mcpMap["other"]; !ok {
		t.Errorf("expected existing 'other' mcp entry preserved")
	}

	gflowEntry, ok := mcpMap["gflow"].(map[string]any)
	if !ok {
		t.Fatalf("expected gflow entry in mcp map")
	}

	if gflowEntry["type"] != "local" {
		t.Errorf("expected type 'local', got %v", gflowEntry["type"])
	}
}

func TestConfigureStandardMCPServers(t *testing.T) {
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "claude_desktop_config.json")

	if err := configureStandardMCPServers(cfgFile, "/usr/local/bin/gflow"); err != nil {
		t.Fatalf("configureStandardMCPServers failed: %v", err)
	}

	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	servers, ok := parsed["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("expected mcpServers map, got %T", parsed["mcpServers"])
	}

	gflowEntry, ok := servers["gflow"].(map[string]any)
	if !ok {
		t.Fatalf("expected gflow entry in mcpServers")
	}

	if gflowEntry["command"] != "/usr/local/bin/gflow" {
		t.Errorf("expected command '/usr/local/bin/gflow', got %v", gflowEntry["command"])
	}
}
