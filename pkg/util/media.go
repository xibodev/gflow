package util

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/xibodev/gflow/pkg/models"
)

// MaxDownloadBytes bounds a single media download (4K video safe upper bound).
const MaxDownloadBytes = int64(512 << 20)

// SniffMediaType detects the media type of a payload from its leading bytes.
// Container formats are identified by structure (ISO-BMFF brands, RIFF
// forms, EBML) so an AVIF image or M4A audio file is never labelled video.
func SniffMediaType(data []byte) string {
	if len(data) >= 8 {
		switch {
		case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
			return "image/png"
		case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
			return "image/jpeg"
		case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
			return "image/gif"
		case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
			return "image/webp"
		case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE":
			return "audio/wav"
		case bytes.HasPrefix(data, []byte{0x1A, 0x45, 0xDF, 0xA3}):
			return "video/webm"
		case bytes.HasPrefix(data, []byte("OggS")):
			return "audio/ogg"
		case bytes.HasPrefix(data, []byte("ID3")):
			return "audio/mpeg"
		case data[0] == 0xFF && data[1]&0xF6 == 0xF0:
			// ADTS AAC: 12-bit sync, layer bits 00.
			return "audio/aac"
		case data[0] == 0xFF && data[1]&0xE0 == 0xE0 && data[1]&0x06 != 0:
			// Raw MPEG audio frame (MP3 without an ID3 tag): 11-bit sync,
			// non-reserved layer.
			return "audio/mpeg"
		case len(data) >= 12 && string(data[4:8]) == "ftyp":
			return isoBMFFType(string(data[8:12]))
		}
	}
	return http.DetectContentType(data)
}

// isoBMFFType maps an ISO base media file format major brand to a MIME type.
func isoBMFFType(brand string) string {
	switch strings.TrimSpace(brand) {
	case "avif", "avis":
		return "image/avif"
	case "heic", "heix", "heim", "heis", "mif1", "msf1":
		return "image/heic"
	case "M4A", "M4B", "M4P", "F4A":
		return "audio/mp4"
	case "qt":
		return "video/quicktime"
	default:
		return "video/mp4"
	}
}

// ExtensionForMime returns standard file extension for a mime type.
func ExtensionForMime(mime string) string {
	mime = strings.ToLower(mime)
	switch {
	case strings.Contains(mime, "png"):
		return ".png"
	case strings.Contains(mime, "jpeg") || strings.Contains(mime, "jpg"):
		return ".jpg"
	case strings.Contains(mime, "webp"):
		return ".webp"
	case strings.Contains(mime, "gif"):
		return ".gif"
	case strings.Contains(mime, "avif"):
		return ".avif"
	case strings.Contains(mime, "heic"):
		return ".heic"
	case strings.Contains(mime, "webm"):
		return ".webm"
	case strings.Contains(mime, "quicktime"):
		return ".mov"
	case strings.Contains(mime, "audio/wav"), strings.Contains(mime, "audio/x-wav"), strings.Contains(mime, "wave"):
		return ".wav"
	case strings.Contains(mime, "audio/mpeg"), strings.Contains(mime, "mp3"):
		return ".mp3"
	case strings.Contains(mime, "audio/aac"):
		return ".aac"
	case strings.Contains(mime, "audio/mp4"):
		return ".m4a"
	case strings.Contains(mime, "ogg"):
		return ".ogg"
	case strings.Contains(mime, "mp4") || strings.Contains(mime, "video"):
		return ".mp4"
	default:
		return ".bin"
	}
}

// mediaKind reports the broad kind ("image", "video", "audio") of a MIME
// type, or "" when it is not a recognized media type.
func mediaKind(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	default:
		return ""
	}
}

// IsErrorPageMime reports payload types that are never valid generated media:
// HTML sign-in/error pages, plain text, and JSON/XML error bodies.
func IsErrorPageMime(mime string) bool {
	mime = strings.ToLower(mime)
	return strings.HasPrefix(mime, "text/") ||
		strings.HasPrefix(mime, "application/json") ||
		strings.HasPrefix(mime, "application/xml") ||
		strings.HasPrefix(mime, "application/javascript")
}

func extFor(assetType, mime string) string {
	ext := ExtensionForMime(mime)
	if ext == ".bin" {
		switch assetType {
		case "video":
			return ".mp4"
		case "audio":
			return ".wav"
		}
		return ".png"
	}
	return ext
}

// ResolveOutputPath maps a user --output value to an exact file path.
// Semantics: existing directory or trailing separator means directory; an
// explicit filename (e.g. clip.mp4) is honored for single assets; multiple
// assets with a filename produce deterministic suffixed siblings.
func ResolveOutputPath(output, assetType, assetID, mime string, index, total int) (string, error) {
	path, _, err := resolveOutputPath(output, assetType, assetID, mime, index, total)
	return path, err
}

// resolveOutputPath also reports whether the file name was generated (and so
// may have its extension corrected once the payload type is known).
func resolveOutputPath(output, assetType, assetID, mime string, index, total int) (string, bool, error) {
	ext := extFor(assetType, mime)
	kind := SafeFileComponent(assetType, 16)
	if kind == "" {
		kind = "asset"
	}
	generated := func(dir string) string {
		ts := time.Now().Format("20060102_150405")
		short := SafeFileComponent(assetID, 12)
		if short == "" {
			short = "asset"
		}
		name := fmt.Sprintf("%s_%s_%s%s", kind, ts, short, ext)
		if total > 1 {
			stem := strings.TrimSuffix(name, ext)
			name = fmt.Sprintf("%s_%d%s", stem, index+1, ext)
		}
		return filepath.Join(dir, name)
	}
	if strings.TrimSpace(output) == "" {
		return generated("."), true, nil
	}
	if isDir(output) {
		if err := os.MkdirAll(output, 0755); err != nil {
			return "", false, err
		}
		return generated(output), true, nil
	}
	// Non-existent path without an extension is treated as a directory.
	if filepath.Ext(output) == "" {
		if err := os.MkdirAll(output, 0755); err != nil {
			return "", false, err
		}
		return generated(output), true, nil
	}
	// Explicit filename.
	if total > 1 {
		dir := filepath.Dir(output)
		extOut := filepath.Ext(output)
		stem := strings.TrimSuffix(filepath.Base(output), extOut)
		if index == 0 {
			return output, false, nil
		}
		return filepath.Join(dir, fmt.Sprintf("%s_%d%s", stem, index+1, extOut)), false, nil
	}
	return output, false, nil
}

// createExclusive creates parent dirs and exclusively creates path (or a
// suffixed sibling). It never overwrites an existing file.
func createExclusive(path string) (*os.File, string, error) {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, "", err
		}
	}
	if f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600); err == nil {
		return f, path, nil
	} else if !errors.Is(err, os.ErrExist) {
		return nil, "", err
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), ext)
	for i := 1; i < 10000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s_%d%s", base, i, ext))
		if f, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600); err == nil {
			return f, candidate, nil
		} else if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("could not allocate unique output path for %s", path)
}

// DownloadOptions tunes SaveURL for provider-specific needs.
type DownloadOptions struct {
	// Client performs the requests. Nil uses a shared client that honors
	// HTTP(S)_PROXY. Provide a client with a cookie Jar when the download
	// needs session cookies; the jar scopes cookies to matching hosts on every
	// redirect hop.
	Client *http.Client
	// Header adds request headers (User-Agent, Referer, ...).
	Header http.Header
	// Kind, when "image", "video" or "audio", rejects a payload whose sniffed
	// type is a different media kind.
	Kind string
	// MaxBytes bounds the payload size (default MaxDownloadBytes).
	MaxBytes int64
	// Attempts bounds tries for transient failures (default 3).
	Attempts int
	// IdleTimeout aborts a transfer that makes no progress for this long
	// (default 60s). Large files are never cut off by a total deadline.
	IdleTimeout time.Duration
}

// DownloadError reports a failed media download. Status is the HTTP status
// when the server answered, 0 for transport failures.
type DownloadError struct {
	Status int
	URL    string
	Err    error
}

func (e *DownloadError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("HTTP %d downloading media", e.Status)
	}
	return fmt.Sprintf("downloading media: %v", e.Err)
}

func (e *DownloadError) Unwrap() error { return e.Err }

var errInvalidMedia = errors.New("invalid media payload")

// ErrInvalidMedia is returned (wrapped) when a download is not valid media:
// empty, an HTML/text/JSON error page, or the wrong media kind.
var ErrInvalidMedia = errInvalidMedia

var sharedDownloadClient = func() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 60 * time.Second
	return &http.Client{Transport: t}
}()

// DownloadFile downloads a URL to targetPath (or a suffixed sibling when
// targetPath exists), retrying transient network failures.
func DownloadFile(ctx context.Context, url string, targetPath string) error {
	_, _, err := SaveURL(ctx, url, targetPath, "", "", 0, 1, DownloadOptions{})
	return err
}

// SaveURL downloads url into outputPath, which may be a directory, a path
// with a trailing separator, or an explicit file name (see ResolveOutputPath).
// The payload streams into a temp ".part" file in the destination directory,
// is validated (non-empty, not an HTML/text/JSON error page, matching
// opts.Kind), and is then moved into place without overwriting any existing
// file. Generated names take the extension of the sniffed type. It returns the
// final path and the sniffed MIME type.
func SaveURL(ctx context.Context, url, outputPath, assetType, assetID string, index, total int, opts DownloadOptions) (string, string, error) {
	if strings.TrimSpace(url) == "" {
		return "", "", errors.New("empty media URL")
	}
	planned, generated, err := resolveOutputPath(outputPath, assetType, assetID, "", index, total)
	if err != nil {
		return "", "", err
	}
	dir := filepath.Dir(planned)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", "", err
	}
	attempts := opts.Attempts
	if attempts <= 0 {
		attempts = 3
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if cerr := ctx.Err(); cerr != nil {
			return "", "", cerr
		}
		tmpPath, mime, err := fetchToTemp(ctx, url, dir, opts)
		if err == nil {
			final := planned
			if generated {
				if ext := ExtensionForMime(mime); ext != ".bin" && ext != filepath.Ext(final) {
					final = strings.TrimSuffix(final, filepath.Ext(final)) + ext
				}
			}
			placed, perr := placeExclusive(tmpPath, final)
			if perr != nil {
				_ = os.Remove(tmpPath)
				return "", "", perr
			}
			return placed, mime, nil
		}
		lastErr = err
		if !retryableDownload(err) || attempt == attempts {
			break
		}
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}
	if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
	}
	return "", "", fmt.Errorf("download failed: %w", lastErr)
}

// retryableDownload retries transport failures and 5xx/408/429 responses,
// never content rejections or other client errors.
func retryableDownload(err error) bool {
	if errors.Is(err, errInvalidMedia) {
		return false
	}
	var de *DownloadError
	if errors.As(err, &de) && de.Status != 0 {
		return de.Status >= 500 || de.Status == http.StatusTooManyRequests || de.Status == http.StatusRequestTimeout
	}
	return true
}

// placeExclusive moves tmp to final (or a suffixed sibling) without ever
// replacing an existing file: the name is reserved with an exclusive create,
// then the finished temp file is renamed over the reservation.
func placeExclusive(tmp, final string) (string, error) {
	f, reserved, err := createExclusive(final)
	if err != nil {
		return "", err
	}
	_ = f.Close()
	if err := RenameWithRetry(tmp, reserved); err != nil {
		_ = os.Remove(reserved)
		return "", err
	}
	return reserved, nil
}

// fetchToTemp performs one download attempt into a temp file in dir.
func fetchToTemp(ctx context.Context, url, dir string, opts DownloadOptions) (string, string, error) {
	client := opts.Client
	if client == nil {
		client = sharedDownloadClient
	}
	idle := opts.IdleTimeout
	if idle <= 0 {
		idle = 60 * time.Second
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = MaxDownloadBytes
	}

	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stalled atomic.Bool
	watchdog := time.AfterFunc(idle, func() { stalled.Store(true); cancel() })
	defer watchdog.Stop()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	for k, vs := range opts.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Mozilla/5.0")
	}
	resp, err := client.Do(req)
	if err != nil {
		if stalled.Load() {
			err = fmt.Errorf("no response within %v: %w", idle, err)
		}
		return "", "", &DownloadError{URL: url, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", "", &DownloadError{Status: resp.StatusCode, URL: url, Err: fmt.Errorf("unexpected status %d", resp.StatusCode)}
	}

	tmp, err := os.CreateTemp(dir, ".gflow-*.part")
	if err != nil {
		return "", "", err
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	head := make([]byte, 0, 512)
	total := int64(0)
	buf := make([]byte, 64*1024)
	limited := io.LimitReader(resp.Body, maxBytes+1)
	for {
		n, rerr := limited.Read(buf)
		if n > 0 {
			watchdog.Reset(idle)
			if len(head) < 512 {
				need := 512 - len(head)
				if need > n {
					need = n
				}
				head = append(head, buf[:need]...)
			}
			total += int64(n)
			if total > maxBytes {
				return "", "", fmt.Errorf("%w: download exceeds %d bytes", errInvalidMedia, maxBytes)
			}
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				return "", "", werr
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if stalled.Load() {
				rerr = fmt.Errorf("transfer stalled for %v: %w", idle, rerr)
			}
			return "", "", &DownloadError{URL: url, Err: rerr}
		}
	}
	if total == 0 {
		return "", "", fmt.Errorf("%w: empty download payload", errInvalidMedia)
	}
	mime := SniffMediaType(head)
	if IsErrorPageMime(mime) {
		return "", "", fmt.Errorf("%w: server returned %s instead of media (session expired or link invalid?)", errInvalidMedia, mime)
	}
	if opts.Kind != "" {
		if got := mediaKind(mime); got != "" && got != opts.Kind {
			return "", "", fmt.Errorf("%w: expected %s but received %s", errInvalidMedia, opts.Kind, mime)
		}
	}
	if err := tmp.Sync(); err != nil {
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		return "", "", err
	}
	keep = true
	return tmpPath, mime, nil
}

// SaveBytes stores an in-memory payload with SaveURL's rules: it must be
// real media of the expected kind, generated names take the sniffed
// extension, and an existing file is never overwritten.
func SaveBytes(data []byte, outputPath, assetType, assetID string, index, total int, kind string) (string, string, error) {
	if len(data) == 0 {
		return "", "", fmt.Errorf("%w: empty payload", errInvalidMedia)
	}
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	mime := SniffMediaType(head)
	if IsErrorPageMime(mime) {
		return "", "", fmt.Errorf("%w: payload is %s, not media", errInvalidMedia, mime)
	}
	if kind != "" {
		if got := mediaKind(mime); got != "" && got != kind {
			return "", "", fmt.Errorf("%w: expected %s but received %s", errInvalidMedia, kind, mime)
		}
	}
	planned, generated, err := resolveOutputPath(outputPath, assetType, assetID, "", index, total)
	if err != nil {
		return "", "", err
	}
	final := planned
	if generated {
		if ext := ExtensionForMime(mime); ext != ".bin" && ext != filepath.Ext(final) {
			final = strings.TrimSuffix(final, filepath.Ext(final)) + ext
		}
	}
	dir := filepath.Dir(final)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", "", err
	}
	tmp, err := os.CreateTemp(dir, ".gflow-*.part")
	if err != nil {
		return "", "", err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", err
	}
	placed, err := placeExclusive(tmpPath, final)
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", "", err
	}
	return placed, mime, nil
}

// SaveAsset writes an Asset to disk by fetching its URL.
func SaveAsset(ctx context.Context, asset *models.Asset, outputPath string) (string, error) {
	return SaveAssetIndexed(ctx, asset, outputPath, 0, 1)
}

// SaveAssetIndexed saves one of total assets, honoring explicit filenames.
func SaveAssetIndexed(ctx context.Context, asset *models.Asset, outputPath string, index, total int) (string, error) {
	if asset.LocalPath != "" {
		if st, err := os.Stat(asset.LocalPath); err == nil && st.Size() > 0 {
			return asset.LocalPath, nil
		}
	}
	if asset.URL == "" {
		return "", errors.New("asset has no downloadable URL")
	}
	kind := ""
	switch asset.Type {
	case "image", "video", "audio":
		kind = asset.Type
	}
	actual, mime, err := SaveURL(ctx, asset.URL, outputPath, asset.Type, asset.ID, index, total, DownloadOptions{Kind: kind})
	if err != nil {
		return "", err
	}
	asset.LocalPath = actual
	if mime != "" && mediaKind(mime) != "" {
		asset.MimeType = mime
	}
	if st, err := os.Stat(actual); err == nil {
		asset.Size = st.Size()
	}
	return actual, nil
}

// SaveResult is the per-asset outcome for centralized save handling.
type SaveResult struct {
	Asset *models.Asset
	Path  string
	Err   error
}

// DecodeBase64AndSave saves base64 data to targetPath atomically.
func DecodeBase64AndSave(b64Data string, targetPath string) error {
	data, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		return err
	}
	return WriteFileAtomic(targetPath, data, 0644)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		return true
	}
	return strings.HasSuffix(path, "/") || strings.HasSuffix(path, "\\")
}
