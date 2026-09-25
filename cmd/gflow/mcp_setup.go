package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/xibodev/gflow/pkg/util"
)

var (
	mcpSetupClient string
	mcpSetupForce  bool
)

var mcpSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Automatically configure gflow MCP server in installed AI coding tools",
	Long: `Detects installed AI coding tools (OpenCode, Claude Desktop, Cursor, Claude Code)
and automatically registers gflow into their global configuration files with the correct client-specific schemas.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunMCPSetup()
	},
}

func init() {
	mcpSetupCmd.Flags().StringVarP(&mcpSetupClient, "client", "c", "all", "Specific client to configure (all, opencode, claude, cursor)")
	mcpSetupCmd.Flags().BoolVarP(&mcpSetupForce, "force", "f", false, "Force create configuration files even if the client directory is not yet detected")
	mcpCmd.AddCommand(mcpSetupCmd)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func getGflowExecutable() string {
	exe, err := os.Executable()
	if err == nil && exe != "" {
		if realPath, err := filepath.EvalSymlinks(exe); err == nil {
			return realPath
		}
		return exe
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		return filepath.Join(home, ".gflow", "bin", "gflow.exe")
	}
	return filepath.Join(home, ".gflow", "bin", "gflow")
}

func RunMCPSetup() error {
	exe := getGflowExecutable()
	fmt.Printf("Configuring gflow MCP server (%s)...\n\n", exe)

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not determine home directory: %w", err)
	}

	configuredCount := 0

	// 1. OpenCode
	if mcpSetupClient == "all" || mcpSetupClient == "opencode" {
		cfgPath := filepath.Join(home, ".config", "opencode", "opencode.json")
		cfgDir := filepath.Dir(cfgPath)
		if dirExists(cfgDir) || mcpSetupForce || fileExists(cfgPath) {
			if err := configureOpenCode(cfgPath, exe); err != nil {
				fmt.Printf("  [FAIL] OpenCode: %v\n", err)
			} else {
				fmt.Printf("  [OK] OpenCode: configured in %s\n", cfgPath)
				configuredCount++
			}
		}
	}

	// 2. Claude Desktop
	if mcpSetupClient == "all" || mcpSetupClient == "claude" {
		var claudeConfigPath string
		switch runtime.GOOS {
		case "windows":
			appData := os.Getenv("APPDATA")
			if appData != "" {
				claudeConfigPath = filepath.Join(appData, "Claude", "claude_desktop_config.json")
			}
		case "darwin":
			claudeConfigPath = filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
		default:
			claudeConfigPath = filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
		}

		if claudeConfigPath != "" {
			claudeDir := filepath.Dir(claudeConfigPath)
			if dirExists(claudeDir) || mcpSetupForce || fileExists(claudeConfigPath) {
				if err := configureStandardMCPServers(claudeConfigPath, exe); err != nil {
					fmt.Printf("  [FAIL] Claude Desktop: %v\n", err)
				} else {
					fmt.Printf("  [OK] Claude Desktop: configured in %s\n", claudeConfigPath)
					configuredCount++
				}
			}
		}
	}

	// 3. Cursor
	if mcpSetupClient == "all" || mcpSetupClient == "cursor" {
		cursorPath := filepath.Join(home, ".cursor", "mcp.json")
		cursorDir := filepath.Dir(cursorPath)
		if dirExists(cursorDir) || mcpSetupForce || fileExists(cursorPath) {
			if err := configureStandardMCPServers(cursorPath, exe); err != nil {
				fmt.Printf("  [FAIL] Cursor: %v\n", err)
			} else {
				fmt.Printf("  [OK] Cursor: configured in %s\n", cursorPath)
				configuredCount++
			}
		}
	}

	// 4. Claude Code CLI
	if mcpSetupClient == "all" || mcpSetupClient == "claude-code" {
		if claudeBin, err := exec.LookPath("claude"); err == nil && claudeBin != "" {
			cmd := exec.Command(claudeBin, "mcp", "add", "-s", "user", "gflow", "--", exe, "mcp")
			if out, err := cmd.CombinedOutput(); err != nil {
				fmt.Printf("  [FAIL] Claude Code CLI: %v (%s)\n", err, string(out))
			} else {
				fmt.Printf("  [OK] Claude Code CLI: registered user-level MCP server\n")
				configuredCount++
			}
		}
	}

	// 5. Install global Skill file for agents (Claude Code / OpenCode)
	skillPaths := []string{
		filepath.Join(home, ".claude", "skills", "gflow", "SKILL.md"),
		filepath.Join(home, ".config", "opencode", "skills", "gflow", "SKILL.md"),
	}
	skillContent := getEmbeddedSkill()
	for _, sp := range skillPaths {
		if dirExists(filepath.Dir(filepath.Dir(sp))) || mcpSetupForce {
			if err := os.MkdirAll(filepath.Dir(sp), 0755); err == nil {
				if err := os.WriteFile(sp, []byte(skillContent), 0644); err == nil {
					fmt.Printf("  [OK] AI Skill: installed in %s\n", sp)
					configuredCount++
				}
			}
		}
	}

	if configuredCount == 0 {
		fmt.Println("No supported AI coding client directories detected.")
		fmt.Println("Run 'gflow mcp setup --force' to create default configuration files anyway.")
	} else {
		fmt.Println("\nSetup complete! Restart your AI assistant sessions to load gflow tools.")
	}

	return nil
}

func configureOpenCode(cfgPath, exePath string) error {
	var cfg map[string]any
	if data, err := os.ReadFile(cfgPath); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return err
		}
	}
	if cfg == nil {
		cfg = make(map[string]any)
	}

	if _, ok := cfg["$schema"]; !ok {
		cfg["$schema"] = "https://opencode.ai/config.json"
	}

	var mcpMap map[string]any
	if rawMcp, ok := cfg["mcp"].(map[string]any); ok {
		mcpMap = rawMcp
	} else {
		mcpMap = make(map[string]any)
	}

	mcpMap["gflow"] = map[string]any{
		"type":    "local",
		"command": []string{exePath, "mcp"},
		"enabled": true,
	}
	cfg["mcp"] = mcpMap

	return writeFormattedJSON(cfgPath, cfg)
}

func configureStandardMCPServers(cfgPath, exePath string) error {
	var cfg map[string]any
	if data, err := os.ReadFile(cfgPath); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return err
		}
	}
	if cfg == nil {
		cfg = make(map[string]any)
	}

	var serversMap map[string]any
	if rawServers, ok := cfg["mcpServers"].(map[string]any); ok {
		serversMap = rawServers
	} else {
		serversMap = make(map[string]any)
	}

	serversMap["gflow"] = map[string]any{
		"command": exePath,
		"args":    []string{"mcp"},
	}
	cfg["mcpServers"] = serversMap

	return writeFormattedJSON(cfgPath, cfg)
}

func writeFormattedJSON(filePath string, data any) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return util.WriteFileAtomic(filePath, bytes, 0644)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func getEmbeddedSkill() string {
	return `---
name: gflow
description: Generate AI video clips (Veo 3.1), images (Imagen 3 / Nano Banana 2), and music with Google Flow/Gemini. Use whenever asked to generate videos, create AI clips, synthesize voice with edge-tts, or assemble multi-scene videos with ffmpeg.
---

# Google Flow & Gemini Video Generation (gflow)

` + "`gflow`" + ` provides direct access to Google Veo 3.1 (video with synchronized native audio), Imagen 3 / Nano Banana 2 (images), and Lyria (music).

## Two Ways to Invoke

### 1. Model Context Protocol (MCP Tool Call)
If the MCP server is loaded, call the tools directly via function calling:
- **` + "`generate_flow_video`" + `** (or ` + "`gflow_generate_flow_video`" + `):
  Non-blocking! Returns in <1s with a ` + "`job_id`" + `:
  ` + "```json" + `
  {
    "prompt": "Cinematic aerial shot of a futuristic coastal city at sunset. (no subtitles, no text overlays)",
    "duration": 10,
    "aspect": "landscape"
  }
  ` + "```" + `
  Returns:
  ` + "```json" + `
  {
    "status": "queued",
    "job_id": "job_vid_1789246139...",
    "message": "Video job queued. Poll get_flow_status(job_id=...) every 10-15s until completed."
  }
  ` + "```" + `
- **` + "`get_flow_status`" + `** (or ` + "`gflow_get_flow_status`" + `):
  Poll generation progress by passing ` + "`job_id`" + `:
  ` + "```json" + `
  {
    "job_id": "job_vid_1789246139..."
  }
  ` + "```" + `
  When status is ` + "`\"completed\"`" + `, it returns:
  ` + "```json" + `
  {
    "status": "completed",
    "job_id": "job_vid_1789246139...",
    "file_path": "C:\\path\\to\\output\\clip.mp4",
    "duration": 10,
    "has_audio": true
  }
  ` + "```" + `
- **` + "`generate_flow_image`" + `** (or ` + "`gflow_generate_flow_image`" + `):
  ` + "```json" + `
  {
    "prompt": "Cyberpunk cat on a neon roof, 8k resolution",
    "aspect": "landscape"
  }
  ` + "```" + `
  Returns JSON containing ` + "`file_path`" + `.

### 2. Command Line Interface (CLI in Terminal / PowerShell)
If calling via terminal/bash, run the ` + "`gflow`" + ` executable directly:
` + "```bash" + `
# Video generation (Veo 3.1)
gflow video "Cinematic aerial shot of a coastal city at sunset. (no subtitles, no text overlays)" -d 10 -a landscape

# Image generation (Imagen 3)
gflow image "A sleek minimalist smart home speaker with amber glow" -a landscape

# Check readiness
gflow status
` + "```" + `

---

## Veo 3.1 Prompting Best Practices

### 1. Dialogue & Lip-Sync
Veo 3.1 generates synchronous mouth movement and audio. Always use the **colon format** before quotes:
* Correct: ` + "`The woman looks at the camera and says: \"Welcome to the future.\"`" + `
* Avoid: ` + "`The woman says \"Welcome...\"`" + ` (without a colon, Veo may burn subtitles onto the video).

### 2. Voice, Narration & Language Direction
Specify vocal gender, accent, language, and emotional tone explicitly:
* ` + "`Audio: A mature male narrator with a deep Brazilian Portuguese voice speaks in Portuguese, saying: \"...\"`" + `
* ` + "`Audio: An elegant female French narrator speaks calmly in French, saying: \"...\"`" + `

### 3. Subtitle Prevention
Always append this negative instruction at the end of every prompt:
` + "`(no subtitles, no text overlays)`" + `

### 4. Layered Sound Effects & Ambience
* ` + "`SFX: Gentle ocean waves crashing against rocks, distant seagulls.`" + `
* ` + "`Audio: Natural room tone, no audience sounds, no background music.`" + `

---

## Building Multi-Scene Videos (>10s) with edge-tts & ffmpeg

Veo generates modular 8s or 10s clips. To produce longer continuous videos (30s, 40s, 60s+):

### Step 1: Synthesize Narration (` + "`edge-tts`" + `)
Use ` + "`--write-media`" + ` (not ` + "`--write-audio`" + `):
` + "```bash" + `
edge-tts --voice pt-BR-AntonioNeural --text "Bem-vindo ao futuro..." --write-media shot1.mp3
` + "```" + `

### Step 2: Generate Video Clips (` + "`gflow`" + `)
Generate each shot (10s each) using MCP or CLI.

### Step 3: Concatenate Video Clips on Windows (` + "`ffmpeg`" + `)
Create a plain ` + "`concat.txt`" + ` file (never use bash process substitution ` + "`<(...)`" + ` on Windows):
` + "```text" + `
file 'C:\path\to\clip1.mp4'
file 'C:\path\to\clip2.mp4'
file 'C:\path\to\clip3.mp4'
file 'C:\path\to\clip4.mp4'
` + "```" + `
Then stitch:
` + "```bash" + `
ffmpeg -f concat -safe 0 -i concat.txt -c copy stitched.mp4
` + "```" + `

### Step 4: Mux Audio Track
` + "```bash" + `
# Combine shot audio files
ffmpeg -f concat -safe 0 -i audio_concat.txt -c copy narration.mp3

# Mux into final video (DO NOT use -shortest if narration is shorter than video)
ffmpeg -i stitched.mp4 -i narration.mp3 -c:v copy -c:a aac -map 0:v:0 -map 1:a:0 final_video.mp4
` + "```" + `
`
}
