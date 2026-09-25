package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xibodev/gflow/pkg/application"
	"github.com/xibodev/gflow/pkg/util"
)

var (
	ErrClosed                = errors.New("adapter client is closed")
	ErrProcessExited         = errors.New("adapter process exited")
	ErrProtocol              = errors.New("adapter protocol error")
	ErrProtocolMismatch      = errors.New("adapter protocol version mismatch")
	ErrUnsupportedCapability = errors.New("adapter capability is not declared")
	ErrCloseTimeout          = errors.New("adapter process did not exit before close timeout")

	providerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)
)

const defaultCloseTimeout = 2 * time.Second

var terminateProcessTreeFn = terminateProcessTree

// Config starts one explicitly named executable without a shell. Command must
// be absolute. Env is the complete child environment and is never inherited
// implicitly; pass os.Environ() explicitly when inheritance is intended. Do not
// put secrets in Args because command lines may be visible to other processes.
type Config struct {
	Command         string
	Args            []string
	Env             []string
	CoreVersion     string
	Stderr          io.Writer
	MaxMessageBytes int
	CloseTimeout    time.Duration
}

// Client is both an application provider and an asynchronous video provider.
// Calls are serialized so adapters do not need to process concurrent requests.
type Client struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdoutPipe io.ReadCloser
	stdout     *bufio.Reader
	descriptor Descriptor
	maxMessage int
	closeWait  time.Duration

	callGate  chan struct{}
	mu        sync.Mutex
	nextID    uint64
	closed    bool
	waitErr   error
	waitDone  chan struct{}
	closeOnce sync.Once
	closeErr  error

	supervisorStop chan struct{}
	startConfig    Config
}

// Start launches and initializes an adapter process.
func Start(ctx context.Context, cfg Config) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.Command == "" || !filepath.IsAbs(cfg.Command) {
		return nil, fmt.Errorf("adapter command must be an absolute executable path")
	}
	if cfg.CoreVersion == "" {
		return nil, fmt.Errorf("adapter core version is required")
	}
	maxMessage := cfg.MaxMessageBytes
	if maxMessage <= 0 {
		maxMessage = DefaultMaxMessageBytes
	}
	closeWait := cfg.CloseTimeout
	if closeWait <= 0 {
		closeWait = defaultCloseTimeout
	}

	cmd := exec.Command(cfg.Command, cfg.Args...)
	configureProcessTree(cmd)
	cmd.Env = util.ChildEnv()
	for _, e := range cfg.Env {
		if key := envKey(e); !isBlockedEnvVar(key) {
			cmd.Env = append(cmd.Env, e)
		}
	}
	if cfg.Stderr == nil {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = cfg.Stderr
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("adapter stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("adapter stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start adapter: %w", err)
	}

	c := &Client{
		cmd: cmd, stdin: stdin, stdoutPipe: stdout, stdout: bufio.NewReaderSize(stdout, maxMessage+1),
		maxMessage: maxMessage, closeWait: closeWait, waitDone: make(chan struct{}), callGate: make(chan struct{}, 1),
		supervisorStop: make(chan struct{}), startConfig: cfg,
	}
	c.callGate <- struct{}{}
	go func() {
		err := cmd.Wait()
		c.mu.Lock()
		c.waitErr = err
		c.mu.Unlock()
		close(c.waitDone)
	}()
	go c.supervise()

	var descriptor Descriptor
	if err := c.call(ctx, MethodInitialize, initializeParams{ProtocolVersion: ProtocolVersion, CoreVersion: cfg.CoreVersion}, &descriptor); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("initialize adapter: %w", err)
	}
	if err := validateDescriptor(descriptor); err != nil {
		_ = c.Close()
		return nil, err
	}
	c.descriptor = descriptor
	return c, nil
}

// Handshake starts an adapter with a 30-second bounded timeout for
// the initialization handshake. If the handshake does not complete
// within 30 seconds, an error is returned.
func Handshake(ctx context.Context, cfg Config) (*Client, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return Start(ctx, cfg)
}

// supervise monitors the adapter process and restarts with exponential
// backoff on unexpected exits, up to a maximum number of restarts.
func (c *Client) supervise() {
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second
	maxRestarts := 5
	restartWindow := 5 * time.Minute
	var restarts int
	var windowStart time.Time

	for {
		<-c.waitDone

		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()

		now := time.Now()
		if windowStart.IsZero() {
			windowStart = now
		}
		if now.Sub(windowStart) > restartWindow {
			restarts = 0
			windowStart = now
		}
		if restarts >= maxRestarts {
			return
		}
		restarts++

		select {
		case <-c.supervisorStop:
			return
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
		}

		if err := c.restart(); err != nil {
			return
		}
	}
}

func (c *Client) restart() error {
	cfg := c.startConfig
	cmd := exec.Command(cfg.Command, cfg.Args...)
	configureProcessTree(cmd)
	cmd.Env = util.ChildEnv()
	for _, e := range cfg.Env {
		if key := envKey(e); !isBlockedEnvVar(key) {
			cmd.Env = append(cmd.Env, e)
		}
	}
	if cfg.Stderr == nil {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = cfg.Stderr
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("adapter stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("adapter stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("start adapter: %w", err)
	}

	c.mu.Lock()
	_ = c.stdin.Close()
	_ = c.stdoutPipe.Close()
	c.cmd = cmd
	c.stdin = stdin
	c.stdoutPipe = stdout
	c.stdout = bufio.NewReaderSize(stdout, c.maxMessage+1)
	c.waitDone = make(chan struct{})
	c.mu.Unlock()

	go func() {
		err := cmd.Wait()
		c.mu.Lock()
		c.waitErr = err
		c.mu.Unlock()
		close(c.waitDone)
	}()

	return nil
}

// Descriptor returns the application provider descriptor negotiated at startup.
func (c *Client) Descriptor() application.Descriptor {
	return application.Descriptor{
		ID: application.ProviderID(c.descriptor.ProviderID), Name: c.descriptor.DisplayName,
		Capabilities: append([]application.Capability(nil), c.descriptor.Capabilities...),
	}
}

// AdapterDescriptor returns a defensive copy of the full negotiated descriptor.
func (c *Client) AdapterDescriptor() Descriptor {
	d := c.descriptor
	d.Capabilities = append([]application.Capability(nil), d.Capabilities...)
	return d
}

func (c *Client) GenerateImage(ctx context.Context, request application.ImageRequest) (application.ImageResult, error) {
	if err := c.require(application.CapabilityImageGeneration); err != nil {
		return application.ImageResult{}, err
	}
	params := imageParams{
		Prompt: request.Prompt, Aspect: request.Aspect, Count: request.Count, Model: request.Model,
		Output: request.Output, Reference: request.Reference, ReferenceMediaIDs: request.ReferenceMediaIDs, Seed: request.Seed,
	}
	var result imageResult
	if err := c.call(ctx, MethodGenerateImage, params, &result); err != nil {
		return application.ImageResult{}, err
	}
	if err := validateAssets(result.Assets, "image"); err != nil {
		_ = c.terminate()
		return application.ImageResult{}, err
	}
	return application.ImageResult{Assets: result.Assets}, nil
}

func (c *Client) SubmitVideoOperation(ctx context.Context, request application.VideoRequest) (application.VideoOperation, error) {
	if err := c.require(application.CapabilityVideoSubmit); err != nil {
		return application.VideoOperation{}, err
	}
	params := videoParams{
		Prompt: request.Prompt, Aspect: request.Aspect, Duration: request.Duration, Model: request.Model,
		Resolution: request.Resolution, Output: request.Output, Start: request.Start, End: request.End, Seed: request.Seed,
	}
	var result videoSubmitResult
	if err := c.call(ctx, MethodSubmitVideo, params, &result); err != nil {
		return application.VideoOperation{}, err
	}
	if result.OperationID == "" {
		return application.VideoOperation{}, c.protocolError("empty operation ID")
	}
	return application.VideoOperation{ID: result.OperationID}, nil
}

func (c *Client) PollVideoOperation(ctx context.Context, operationID string) (application.VideoOperation, error) {
	if err := c.require(application.CapabilityVideoPoll); err != nil {
		return application.VideoOperation{}, err
	}
	if operationID == "" {
		return application.VideoOperation{}, fmt.Errorf("operation ID is required")
	}
	var result videoPollResult
	if err := c.call(ctx, MethodPollVideo, videoPollParams{OperationID: operationID}, &result); err != nil {
		return application.VideoOperation{}, err
	}
	if result.OperationID != operationID {
		return application.VideoOperation{}, c.protocolError("poll returned operation %q for %q", result.OperationID, operationID)
	}
	switch result.Status {
	case "queued", "running", "succeeded", "failed":
	default:
		return application.VideoOperation{}, c.protocolError("invalid video status %q", result.Status)
	}
	if err := validateAssets(result.Assets, "video"); err != nil {
		_ = c.terminate()
		return application.VideoOperation{}, err
	}
	return application.VideoOperation{ID: result.OperationID, Status: result.Status, Assets: result.Assets, Error: result.Error}, nil
}

func (c *Client) Chat(ctx context.Context, request application.ChatRequest) (application.ChatResult, error) {
	if err := c.require(application.CapabilityChat); err != nil {
		return application.ChatResult{}, err
	}
	var result chatResult
	if err := c.call(ctx, MethodChat, chatParams{Prompt: request.Prompt}, &result); err != nil {
		if isMethodNotFound(err) {
			return application.ChatResult{}, fmt.Errorf("%w: %s (adapter declares capability but misses method)", ErrUnsupportedCapability, MethodChat)
		}
		return application.ChatResult{}, err
	}
	if result.Text == "" {
		return application.ChatResult{}, c.protocolError("empty chat response")
	}
	return application.ChatResult{Text: result.Text}, nil
}

func (c *Client) GenerateAudio(ctx context.Context, request application.AudioRequest) (application.AudioResult, error) {
	if err := c.require(application.CapabilityAudio); err != nil {
		return application.AudioResult{}, err
	}
	var result audioResult
	if err := c.call(ctx, MethodGenerateAudio, audioParams{Prompt: request.Prompt, Output: request.Output}, &result); err != nil {
		if isMethodNotFound(err) {
			return application.AudioResult{}, fmt.Errorf("%w: %s (adapter declares capability but misses method)", ErrUnsupportedCapability, MethodGenerateAudio)
		}
		return application.AudioResult{}, err
	}
	if len(result.Assets) == 0 {
		return application.AudioResult{}, c.protocolError("audio.generate returned no assets")
	}
	for i, asset := range result.Assets {
		if asset.Type != "audio" && asset.Type != "video" {
			return application.AudioResult{}, c.protocolError("audio asset %d has type %q, want audio or video", i, asset.Type)
		}
		if asset.ID == "" && asset.URL == "" && asset.LocalPath == "" {
			return application.AudioResult{}, c.protocolError("audio asset %d has no identity or location", i)
		}
	}
	return application.AudioResult{Text: result.Text, Assets: result.Assets}, nil
}

func (c *Client) require(capability application.Capability) error {
	if !c.Descriptor().Supports(capability) {
		return fmt.Errorf("%w: %s", ErrUnsupportedCapability, capability)
	}
	return nil
}

// AuthDescribe returns the adapter-declared auth status. Adapters without the
// auth-status capability fail fast with ErrUnsupportedCapability; a declared
// capability whose method is missing surfaces as unsupported, never a hang.
func (c *Client) AuthDescribe(ctx context.Context) (AuthPayload, error) {
	if err := c.require(application.CapabilityAuthStatus); err != nil {
		return AuthPayload{}, err
	}
	var result AuthPayload
	if err := c.call(ctx, MethodAuthDescribe, struct{}{}, &result); err != nil {
		if isMethodNotFound(err) {
			return AuthPayload{}, fmt.Errorf("%w: %s (adapter declares capability but misses method)", ErrUnsupportedCapability, MethodAuthDescribe)
		}
		return AuthPayload{}, err
	}
	return normalizeAuthPayload(result), nil
}

// AuthLogin runs the adapter-declared login automation. It requires the
// auth-login capability (which itself requires auth-status at handshake).
func (c *Client) AuthLogin(ctx context.Context) (AuthPayload, error) {
	if err := c.require(application.CapabilityAuthLogin); err != nil {
		return AuthPayload{}, err
	}
	var result AuthPayload
	if err := c.call(ctx, MethodAuthLogin, struct{}{}, &result); err != nil {
		if isMethodNotFound(err) {
			return AuthPayload{}, fmt.Errorf("%w: %s (adapter declares capability but misses method)", ErrUnsupportedCapability, MethodAuthLogin)
		}
		return AuthPayload{}, err
	}
	return normalizeAuthPayload(result), nil
}

func isMethodNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if len(msg) >= 6 && containsCode(msg, -32601) {
		return true
	}
	lower := msg
	if len(lower) > 0 {
		// case-insensitive substring without importing strings for one check
		for i := 0; i+len("method not found") <= len(lower); i++ {
			match := true
			for j := 0; j < len("method not found"); j++ {
				a := lower[i+j]
				b := "method not found"[j]
				if a >= 'A' && a <= 'Z' {
					a += 'a' - 'A'
				}
				if a != b {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
		for i := 0; i+len("unknown method") <= len(lower); i++ {
			match := true
			for j := 0; j < len("unknown method"); j++ {
				a := lower[i+j]
				b := "unknown method"[j]
				if a >= 'A' && a <= 'Z' {
					a += 'a' - 'A'
				}
				if a != b {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

func containsCode(msg string, code int) bool {
	want := strconv.Itoa(code)
	for i := 0; i+len(want) <= len(msg); i++ {
		if msg[i:i+len(want)] == want {
			return true
		}
	}
	return false
}

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.callGate:
	}
	defer func() { c.callGate <- struct{}{} }()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	payload, err := json.Marshal(request{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return err
	}
	if len(payload) > c.maxMessage {
		return fmt.Errorf("%w: request exceeds %d bytes", ErrProtocol, c.maxMessage)
	}
	if _, err := c.stdin.Write(append(payload, '\n')); err != nil {
		return c.processError(err)
	}

	type readResult struct {
		line []byte
		err  error
	}
	read := make(chan readResult, 1)
	go func() {
		line, err := c.readLine()
		read <- readResult{line: line, err: err}
	}()
	var rr readResult
	select {
	case rr = <-read:
	case <-ctx.Done():
		_ = c.terminate()
		select {
		case <-read:
		case <-time.After(c.closeWait):
		}
		return ctx.Err()
	}
	if rr.err != nil {
		return c.processError(rr.err)
	}

	var response response
	decoder := json.NewDecoder(bytes.NewReader(rr.line))
	if err := decoder.Decode(&response); err != nil {
		return c.protocolError("malformed JSON response: %v", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return c.protocolError("response contains multiple JSON values")
	}
	if response.JSONRPC != "2.0" || string(response.ID) != strconv.FormatUint(id, 10) {
		return c.protocolError("invalid or mismatched response ID")
	}
	if response.Error != nil {
		return fmt.Errorf("adapter RPC error %d: %s", response.Error.Code, response.Error.Message)
	}
	if len(response.Result) == 0 || bytes.Equal(response.Result, []byte("null")) {
		return c.protocolError("missing result")
	}
	if err := json.Unmarshal(response.Result, result); err != nil {
		return c.protocolError("invalid result: %v", err)
	}
	return nil
}

func (c *Client) readLine() ([]byte, error) {
	line, err := c.stdout.ReadSlice('\n')
	if err == bufio.ErrBufferFull || len(line) > c.maxMessage {
		_ = c.terminate()
		return nil, fmt.Errorf("%w: response exceeds %d bytes", ErrProtocol, c.maxMessage)
	}
	if err != nil {
		return nil, err
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, c.protocolError("empty response")
	}
	return line, nil
}

func (c *Client) processError(cause error) error {
	if errors.Is(cause, ErrProtocol) {
		return cause
	}
	select {
	case <-c.waitDone:
		c.mu.Lock()
		err := c.waitErr
		c.mu.Unlock()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrProcessExited, err)
		}
		return ErrProcessExited
	default:
		return fmt.Errorf("%w: %v", ErrProcessExited, cause)
	}
}

func (c *Client) protocolError(format string, args ...any) error {
	_ = c.terminate()
	return fmt.Errorf("%w: %s", ErrProtocol, fmt.Sprintf(format, args...))
}

// Close closes stdin first so a cooperative adapter can exit, then kills it if
// it has not exited within the configured bound.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		select {
		case c.supervisorStop <- struct{}{}:
		default:
		}
		_ = c.stdin.Close()
		_ = c.stdoutPipe.Close()
		select {
		case <-c.waitDone:
		case <-time.After(c.closeWait):
			terminateErr := c.terminateProcessTree()
			select {
			case <-c.waitDone:
				c.setCloseErr(terminateErr)
			case <-time.After(c.closeWait):
				c.setCloseErr(errors.Join(terminateErr, ErrCloseTimeout))
			}
		}
	})
	c.mu.Lock()
	err := c.closeErr
	c.mu.Unlock()
	return err
}

// Shutdown stops the supervisor and kills the adapter process.
func (c *Client) Shutdown() error {
	return c.Close()
}

func envKey(s string) string {
	if i := strings.IndexByte(s, '='); i >= 0 {
		return s[:i]
	}
	return s
}

func isBlockedEnvVar(key string) bool {
	upper := strings.ToUpper(key)
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

func (c *Client) terminate() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	_ = c.stdin.Close()
	_ = c.stdoutPipe.Close()
	select {
	case <-c.waitDone:
		return nil
	default:
		return c.terminateProcessTree()
	}
}

func (c *Client) terminateProcessTree() error {
	ctx, cancel := context.WithTimeout(context.Background(), c.closeWait)
	defer cancel()
	return terminateProcessTreeFn(ctx, c.cmd)
}

func (c *Client) setCloseErr(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	c.closeErr = err
	c.mu.Unlock()
}

func validateDescriptor(descriptor Descriptor) error {
	if descriptor.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("%w: core=%s adapter=%s", ErrProtocolMismatch, ProtocolVersion, descriptor.ProtocolVersion)
	}
	if !providerIDPattern.MatchString(descriptor.ProviderID) {
		return fmt.Errorf("%w: invalid provider ID %q", ErrProtocol, descriptor.ProviderID)
	}
	if descriptor.DisplayName == "" || descriptor.AdapterVersion == "" {
		return fmt.Errorf("%w: display name and adapter version are required", ErrProtocol)
	}
	known := map[application.Capability]bool{
		application.CapabilityImageGeneration: true,
		application.CapabilityVideoSubmit:     true,
		application.CapabilityVideoPoll:       true,
		application.CapabilityChat:            true,
		application.CapabilityAudio:           true,
		application.CapabilityAuthStatus:      true,
		application.CapabilityAuthLogin:       true,
	}
	seen := make(map[application.Capability]bool, len(descriptor.Capabilities))
	for _, capability := range descriptor.Capabilities {
		if !known[capability] {
			return fmt.Errorf("%w: unknown capability %q", ErrProtocol, capability)
		}
		if seen[capability] {
			return fmt.Errorf("%w: duplicate capability %q", ErrProtocol, capability)
		}
		seen[capability] = true
	}
	if seen[application.CapabilityVideoSubmit] != seen[application.CapabilityVideoPoll] {
		return fmt.Errorf("%w: video-submit and video-poll must be declared together", ErrProtocol)
	}
	if seen[application.CapabilityAuthLogin] && !seen[application.CapabilityAuthStatus] {
		return fmt.Errorf("%w: auth-login requires auth-status", ErrProtocol)
	}
	return nil
}

func validateAssets(assets []application.Asset, expectedType string) error {
	for i, asset := range assets {
		if asset.Type != expectedType {
			return fmt.Errorf("%w: asset %d has type %q, want %q", ErrProtocol, i, asset.Type, expectedType)
		}
		if asset.ID == "" && asset.URL == "" && asset.LocalPath == "" {
			return fmt.Errorf("%w: asset %d has no identity or location", ErrProtocol, i)
		}
	}
	return nil
}
