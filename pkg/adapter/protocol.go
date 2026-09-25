// Package adapter implements gflow's private out-of-process provider protocol.
package adapter

import (
	"encoding/json"

	"github.com/xibodev/gflow/pkg/application"
)

// ProtocolVersion is independent of the MCP protocol version.
const ProtocolVersion = "1"

const (
	MethodInitialize    = "initialize"
	MethodGenerateImage = "image.generate"
	MethodSubmitVideo   = "video.submit"
	MethodPollVideo     = "video.poll"
	MethodChat          = "chat.generate"
	MethodGenerateAudio = "audio.generate"
	MethodAuthDescribe  = "auth.describe"
	MethodAuthLogin     = "auth.login"
)

const DefaultMaxMessageBytes = 1024 * 1024

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type initializeParams struct {
	ProtocolVersion string `json:"protocol_version"`
	CoreVersion     string `json:"core_version"`
}

// Descriptor is returned by an adapter during initialization.
type Descriptor struct {
	ProviderID      string                   `json:"provider_id"`
	DisplayName     string                   `json:"display_name"`
	AdapterVersion  string                   `json:"adapter_version"`
	ProtocolVersion string                   `json:"protocol_version"`
	Capabilities    []application.Capability `json:"capabilities"`
}

type imageParams struct {
	Prompt            string   `json:"prompt"`
	Aspect            string   `json:"aspect,omitempty"`
	Count             int      `json:"count,omitempty"`
	Model             string   `json:"model,omitempty"`
	Output            string   `json:"output,omitempty"`
	Reference         string   `json:"reference,omitempty"`
	ReferenceMediaIDs []string `json:"reference_media_ids,omitempty"`
	Seed              *int64   `json:"seed,omitempty"`
}

type imageResult struct {
	Assets []application.Asset `json:"assets"`
}

type videoParams struct {
	Prompt     string `json:"prompt"`
	Aspect     string `json:"aspect,omitempty"`
	Duration   int    `json:"duration,omitempty"`
	Model      string `json:"model,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	Output     string `json:"output,omitempty"`
	Start      string `json:"start,omitempty"`
	End        string `json:"end,omitempty"`
	Seed       *int64 `json:"seed,omitempty"`
}

type videoSubmitResult struct {
	OperationID string `json:"operation_id"`
}

type videoPollParams struct {
	OperationID string `json:"operation_id"`
}

type videoPollResult struct {
	OperationID string              `json:"operation_id"`
	Status      string              `json:"status"`
	Assets      []application.Asset `json:"assets,omitempty"`
	Error       string              `json:"error,omitempty"`
}

type chatParams struct {
	Prompt string `json:"prompt"`
}

type chatResult struct {
	Text string `json:"text"`
}

type audioParams struct {
	Prompt string `json:"prompt"`
	Output string `json:"output,omitempty"`
}

type audioResult struct {
	Text   string              `json:"text,omitempty"`
	Assets []application.Asset `json:"assets,omitempty"`
}

// AuthCheck is one named readiness probe reported by an adapter.
type AuthCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// AuthPayload is the typed result of auth.describe and auth.login.
// Adapters declare what gflow may auto-run (auth-login capability) versus
// what the operator must do themselves (next_steps guidance).
type AuthPayload struct {
	Ready     bool        `json:"ready"`
	Summary   string      `json:"summary,omitempty"`
	Checks    []AuthCheck `json:"checks,omitempty"`
	NextSteps []string    `json:"next_steps,omitempty"`
}

func normalizeAuthPayload(payload AuthPayload) AuthPayload {
	if payload.Checks == nil {
		payload.Checks = []AuthCheck{}
	}
	if payload.NextSteps == nil {
		payload.NextSteps = []string{}
	}
	return payload
}
