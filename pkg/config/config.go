package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/xibodev/gflow/pkg/util"
)

// Default configuration constants for Google Flow APIs.
//
// APIBase, APIKey, RecaptchaSiteKey, ToolName, OriginURL, FlowURL and
// LabsAPIBase are public upstream client identifiers/endpoint addresses,
// not user secrets. Local daemon credentials are never embedded here; see
// LoadAuth.
const (
	APIBase          = "https://aisandbox-pa.googleapis.com"
	APIKey           = "AIzaSyBtrm0o5ab1c-Ec8ZuLcGt3oJAA5VWt3pY"
	RecaptchaSiteKey = "6LdsFiUsAAAAAIjVDZcuLhaHiDn5nnHVXVRQGeMV"
	ToolName         = "PINHOLE"
	OriginURL        = "https://labs.google"
	FlowURL          = "https://labs.google/fx/tools/flow"
	LabsAPIBase      = "https://labs.google/fx/api"
)

// Endpoints for aisandbox-pa.googleapis.com
const (
	EndpointBatchGenerateImages = "/v1/projects/%s/flowMedia:batchGenerateImages"
	EndpointGenerateVideoText   = "/v1/video:batchAsyncGenerateVideoText"
	EndpointGenerateVideoStart  = "/v1/video:batchAsyncGenerateVideoStartImage"
	EndpointGenerateVideoFL     = "/v1/video:batchAsyncGenerateVideoStartAndEndImage"
	EndpointGenerateVideoRef    = "/v1/video:batchAsyncGenerateVideoReferenceImages"
	EndpointGenerateVideoEdit   = "/v1/video:batchAsyncGenerateVideoEditVideo"
	EndpointUpsampleVideo       = "/v1/video:batchAsyncGenerateVideoUpsampleVideo"
	EndpointPollVideoStatus     = "/v1/video:batchCheckAsyncVideoGenerationStatus"
	EndpointGetMedia            = "/v1/media/%s"
	EndpointGetFlowMedia        = "/v1/flowMedia/%s"
	EndpointUploadImage         = "/v1/flow/uploadImage"
	EndpointCredits             = "/v1/credits"
)

// Image model identifiers
const (
	ImageModelDefault = "NARWHAL"     // Imagen 4 / Nano Banana 2
	ImageModelLite    = "HARBOR_SEAL" // Nano Banana 2 Lite
	ImageModelPro     = "GEM_PIX_2"   // Nano Banana Pro
)

// Video model identifiers
const (
	VideoModelDefault = "abra_t2v_10s"
	VideoModelUltra   = "veo_3_1_t2v_fast_ultra"
)

// Video upsampler models
const (
	VideoUpsampler1080p = "veo_3_1_upsampler_1080p"
	VideoUpsampler4k    = "veo_3_1_upsampler_4k"
)

// Resolution enums
const (
	Resolution1080pEnum = "VIDEO_RESOLUTION_1080P"
	Resolution4kEnum    = "VIDEO_RESOLUTION_4K"
)

// Aspect ratio mappings for images
var ImageAspectMap = map[string]string{
	"landscape": "IMAGE_ASPECT_RATIO_LANDSCAPE",
	"16:9":      "IMAGE_ASPECT_RATIO_LANDSCAPE",
	"square":    "IMAGE_ASPECT_RATIO_SQUARE",
	"1:1":       "IMAGE_ASPECT_RATIO_SQUARE",
	"portrait":  "IMAGE_ASPECT_RATIO_PORTRAIT",
	"9:16":      "IMAGE_ASPECT_RATIO_PORTRAIT",
	"4:3":       "IMAGE_ASPECT_RATIO_4_3",
	"4x3":       "IMAGE_ASPECT_RATIO_4_3",
	"3:4":       "IMAGE_ASPECT_RATIO_3_4",
	"3x4":       "IMAGE_ASPECT_RATIO_3_4",
}

// Aspect ratio mappings for videos
var VideoAspectMap = map[string]string{
	"landscape": "VIDEO_ASPECT_RATIO_LANDSCAPE",
	"16:9":      "VIDEO_ASPECT_RATIO_LANDSCAPE",
	"square":    "VIDEO_ASPECT_RATIO_SQUARE",
	"1:1":       "VIDEO_ASPECT_RATIO_SQUARE",
	"portrait":  "VIDEO_ASPECT_RATIO_PORTRAIT",
	"9:16":      "VIDEO_ASPECT_RATIO_PORTRAIT",
}

// OpenAI size to named aspect mapping for compatibility.
var openAISizeAspect = map[string]string{
	"1024x1024": "square",
	"512x512":   "square",
	"1024x1792": "portrait",
	"1792x1024": "landscape",
}

// Config represents runtime configuration
type Config struct {
	Host              string
	Port              int
	OutputDir         string
	ProjectID         string
	DefaultImageModel string
	CDPPort           int
	Debug             bool
	APIToken          string
	BridgeToken       string
}

// ResolveImageAspect maps an explicit aspect or OpenAI size to a named aspect.
// Explicit aspect takes precedence. Unknown values return an error instead of
// silently falling back to landscape.
func ResolveImageAspect(aspect, size string) (string, error) {
	if strings.TrimSpace(aspect) != "" {
		a := strings.ToLower(strings.TrimSpace(aspect))
		if _, ok := ImageAspectMap[a]; ok {
			return a, nil
		}
		return "", fmt.Errorf("unknown image aspect %q", aspect)
	}
	s := strings.TrimSpace(size)
	if s == "" {
		return "landscape", nil
	}
	if a, ok := openAISizeAspect[s]; ok {
		return a, nil
	}
	if strings.Contains(s, "9:16") {
		return "portrait", nil
	}
	// Accept named aspects passed as size for backward compatibility.
	low := strings.ToLower(s)
	if _, ok := ImageAspectMap[low]; ok {
		return low, nil
	}
	return "", fmt.Errorf("unknown image size/aspect %q", size)
}

// ResolveVideoAspect validates a named video aspect.
func ResolveVideoAspect(aspect string) (string, error) {
	if strings.TrimSpace(aspect) == "" {
		return "landscape", nil
	}
	a := strings.ToLower(strings.TrimSpace(aspect))
	if _, ok := VideoAspectMap[a]; ok {
		return a, nil
	}
	return "", fmt.Errorf("unknown video aspect %q", aspect)
}

// ValidateBindHost rejects non-loopback bind hosts for the local-only design.
func ValidateBindHost(host string) error {
	h := strings.ToLower(strings.TrimSpace(host))
	switch h {
	case "127.0.0.1", "localhost", "::1":
		return nil
	default:
		if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
			return nil
		}
		return fmt.Errorf("unsupported bind host %q: gflow is a local-only daemon; use 127.0.0.1", host)
	}
}

// ValidatePort checks the TCP port range.
func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port %d: must be in [1,65535]", port)
	}
	return nil
}

// RequireProjectID returns an actionable error when generation is attempted
// without a configured Google Flow project. Status/setup must work without it.
func (c *Config) RequireProjectID() (string, error) {
	if strings.TrimSpace(c.ProjectID) == "" {
		return "", fmt.Errorf("Google Flow project is not configured: set DEFAULT_PROJECT (or FLOW_PROJECT_ID) to your Flow project ID, then retry generation")
	}
	return c.ProjectID, nil
}

// LoadConfig loads configuration from environment variables with defaults.
// Generation project comes only from DEFAULT_PROJECT/FLOW_PROJECT_ID; there is
// no tracked default. Local daemon tokens are loaded via LoadAuth.
// Environment variables are stripped of gflow-private secrets before use.
func LoadConfig() *Config {
	env := util.ChildEnv()
	envMap := make(map[string]string)
	for _, e := range env {
		if i := strings.IndexByte(e, '='); i >= 0 {
			envMap[e[:i]] = e[i+1:]
		}
	}

	host := envMap["FLOW_HOST"]
	if host == "" {
		host = getEnv("FLOW_HOST", "127.0.0.1")
	}
	portStr := envMap["FLOW_PORT"]
	if portStr == "" {
		portStr = getEnv("FLOW_PORT", "8001")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 8001
	}

	outDir := envMap["FLOW_OUTPUT_DIR"]
	if outDir == "" {
		outDir = getEnv("FLOW_OUTPUT_DIR", "")
	}
	if outDir == "" {
		cwd, err := os.Getwd()
		if err == nil {
			outDir = filepath.Join(cwd, "output")
		} else {
			outDir = "./output"
		}
	}

	cdpPortStr := envMap["FLOW_CDP_PORT"]
	if cdpPortStr == "" {
		cdpPortStr = getEnv("FLOW_CDP_PORT", "9222")
	}
	cdpPort, err := strconv.Atoi(cdpPortStr)
	if err != nil {
		cdpPort = 9222
	}

	projectID := firstNonEmpty(envMap["DEFAULT_PROJECT"], envMap["FLOW_PROJECT_ID"])
	if projectID == "" {
		projectID = firstNonEmpty(getEnv("DEFAULT_PROJECT", ""), getEnv("FLOW_PROJECT_ID", ""))
	}
	if projectID == "" {
		if home, err := os.UserHomeDir(); err == nil {
			if data, err := os.ReadFile(filepath.Join(home, ".gflow", "project")); err == nil {
				projectID = strings.TrimSpace(string(data))
			}
		}
	}

	cfg := &Config{
		Host:              host,
		Port:              port,
		OutputDir:         outDir,
		ProjectID:         projectID,
		DefaultImageModel: envMap["IMAGE_MODEL"],
		CDPPort:           cdpPort,
		Debug:             envMap["FLOW_DEBUG"] == "true" || envMap["FLOW_DEBUG"] == "1",
	}
	if cfg.DefaultImageModel == "" {
		cfg.DefaultImageModel = getEnv("IMAGE_MODEL", ImageModelDefault)
	}
	cfg.APIToken = envMap["FLOW_API_TOKEN"]
	cfg.BridgeToken = envMap["FLOW_BRIDGE_TOKEN"]
	if cfg.APIToken == "" || cfg.BridgeToken == "" {
		if auth, err := LoadAuth(); err == nil {
			if cfg.APIToken == "" {
				cfg.APIToken = auth.APIToken
			}
			if cfg.BridgeToken == "" {
				cfg.BridgeToken = auth.BridgeToken
			}
		}
	}
	return cfg
}

// Auth holds persistent local daemon credentials. These authenticate local
// CLI/MCP callers (APIToken) and the browser extension (BridgeToken).
// They are not Google credentials and are never embedded in the binary.
type Auth struct {
	APIToken    string `json:"api_token"`
	BridgeToken string `json:"bridge_token"`
}

var authMu sync.Mutex

func authPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gflow", "auth.json"), nil
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gflow", "config.json"), nil
}

// ValidateAuth checks all required credentials exist and are non-empty.
// Returns explicit errors for missing credentials. The system fails closed.
func ValidateAuth() error {
	cfg := LoadConfig()
	if strings.TrimSpace(cfg.APIToken) == "" && strings.TrimSpace(cfg.BridgeToken) == "" {
		return fmt.Errorf("auth validation failed: no credentials found; set FLOW_API_TOKEN and FLOW_BRIDGE_TOKEN or run 'gflow setup'")
	}
	if strings.TrimSpace(cfg.APIToken) == "" {
		return fmt.Errorf("auth validation failed: missing API token (FLOW_API_TOKEN)")
	}
	if strings.TrimSpace(cfg.BridgeToken) == "" {
		return fmt.Errorf("auth validation failed: missing bridge token (FLOW_BRIDGE_TOKEN)")
	}
	return nil
}

// LoadAuth loads or creates persistent local credentials. Concurrent
// daemon/CLI startup uses exclusive file creation so tokens stay stable and
// are never overwritten on each start. File is written atomically using
// WriteFileAtomic to prevent truncation on crash.
func LoadAuth() (*Auth, error) {
	authMu.Lock()
	defer authMu.Unlock()

	path, err := authPath()
	if err != nil {
		return nil, err
	}
	if data, err := os.ReadFile(path); err == nil {
		var a Auth
		if err := json.Unmarshal(data, &a); err == nil && a.APIToken != "" && a.BridgeToken != "" {
			return &a, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	a := &Auth{APIToken: newToken(32), BridgeToken: newToken(32)}
	data, _ := json.MarshalIndent(a, "", "  ")
	if err := util.WriteFileAtomic(path, data, 0600); err != nil {
		if data, rerr := os.ReadFile(path); rerr == nil {
			var existing Auth
			if jerr := json.Unmarshal(data, &existing); jerr == nil && existing.APIToken != "" {
				return &existing, nil
			}
		}
		return nil, err
	}
	return a, nil
}

// SaveAuth saves auth credentials atomically.
func SaveAuth(a *Auth) error {
	authMu.Lock()
	defer authMu.Unlock()

	path, err := authPath()
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(a, "", "  ")
	return util.WriteFileAtomic(path, data, 0600)
}

// AuthPathForTest overrides HOME-dependent lookup in tests via GFLOW_HOME.
func AuthPathForTest() string {
	if h := os.Getenv("GFLOW_HOME"); h != "" {
		return filepath.Join(h, ".gflow", "auth.json")
	}
	p, _ := authPath()
	return p
}

func newToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func getEnv(key, defaultVal string) string {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	return val
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
