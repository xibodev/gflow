package util

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/xibodev/gflow/pkg/models"
)

var tinyMP4 = []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0}

func noPartFiles(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") || strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestSaveURLRejectsErrorPagesWithCharset(t *testing.T) {
	for name, body := range map[string]string{
		"html": "<!doctype html><html><body>Sign in</body></html>",
		"json": `{"error":{"code":403,"message":"forbidden"}}`,
		"text": "Service unavailable, try later",
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			dir := t.TempDir()
			_, _, err := SaveURL(context.Background(), srv.URL, dir, "image", "x", 0, 1, DownloadOptions{})
			if !errors.Is(err, ErrInvalidMedia) {
				t.Fatalf("err = %v, want ErrInvalidMedia", err)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatalf("rejected payload left files: %v", entries)
			}
		})
	}
}

func TestSaveURLKindMismatchRejected(t *testing.T) {
	srv := pngServer(t, tinyPNG, 0)
	defer srv.Close()
	dir := t.TempDir()
	if _, _, err := SaveURL(context.Background(), srv.URL, dir, "video", "v", 0, 1, DownloadOptions{Kind: "video"}); !errors.Is(err, ErrInvalidMedia) {
		t.Fatalf("err = %v, want kind mismatch", err)
	}
	noPartFiles(t, dir)
}

func TestSaveURLGeneratedNameUsesSniffedExtension(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 64)...))
	}))
	defer srv.Close()
	dir := t.TempDir()
	path, mime, err := SaveURL(context.Background(), srv.URL, dir, "image", "abc", 0, 1, DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/jpeg" || filepath.Ext(path) != ".jpg" {
		t.Fatalf("path=%s mime=%s, want .jpg image/jpeg", path, mime)
	}
	noPartFiles(t, dir)
}

func TestSaveURLExplicitNameKeepsExtension(t *testing.T) {
	srv := pngServer(t, tinyPNG, 0)
	defer srv.Close()
	target := filepath.Join(t.TempDir(), "keep.jpeg")
	path, _, err := SaveURL(context.Background(), srv.URL, target, "image", "abc", 0, 1, DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if path != target {
		t.Fatalf("explicit name changed: %s", path)
	}
}

func TestSaveURLRetriesServerErrorsButNot404(t *testing.T) {
	var calls atomic.Int32
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write(tinyMP4)
	}))
	defer flaky.Close()
	if _, _, err := SaveURL(context.Background(), flaky.URL, t.TempDir(), "video", "v", 0, 1, DownloadOptions{Attempts: 3}); err != nil {
		t.Fatalf("retry after 502 failed: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}

	var notFound atomic.Int32
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		notFound.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer missing.Close()
	_, _, err := SaveURL(context.Background(), missing.URL, t.TempDir(), "video", "v", 0, 1, DownloadOptions{Attempts: 3})
	var de *DownloadError
	if !errors.As(err, &de) || de.Status != http.StatusNotFound {
		t.Fatalf("err = %v, want DownloadError 404", err)
	}
	if notFound.Load() != 1 {
		t.Fatalf("404 retried %d times", notFound.Load())
	}
}

func TestSaveAssetSanitizesHostileIDs(t *testing.T) {
	srv := pngServer(t, tinyPNG, 0)
	defer srv.Close()
	dir := t.TempDir()
	for _, id := range []string{"../../escape", `a:b\c`, "CON", "名前*?<>|"} {
		a := &models.Asset{ID: id, Type: "image", URL: srv.URL}
		got, err := SaveAsset(context.Background(), a, dir)
		if err != nil {
			t.Fatalf("id %q: %v", id, err)
		}
		if filepath.Dir(got) != dir {
			t.Fatalf("id %q escaped output dir: %s", id, got)
		}
		if strings.ContainsAny(filepath.Base(got), `:\/*?"<>|`) {
			t.Fatalf("id %q produced unsafe name %s", id, got)
		}
	}
}

func TestSniffContainerBrands(t *testing.T) {
	cases := map[string]string{
		"avif": "image/avif",
		"heic": "image/heic",
		"M4A ": "audio/mp4",
		"isom": "video/mp4",
		"mp42": "video/mp4",
	}
	for brand, want := range cases {
		data := append([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p'}, []byte(brand)...)
		if got := SniffMediaType(data); got != want {
			t.Errorf("brand %q: got %s want %s", brand, got, want)
		}
	}
	if got := SniffMediaType([]byte{0x1A, 0x45, 0xDF, 0xA3, 0, 0, 0, 0}); got != "video/webm" {
		t.Errorf("webm: got %s", got)
	}
	if got := SniffMediaType([]byte{0xFF, 0xFB, 0x90, 0x64, 0, 0, 0, 0}); got != "audio/mpeg" {
		t.Errorf("raw mp3 frame: got %s", got)
	}
	if got := SniffMediaType([]byte{0xFF, 0xF1, 0x50, 0x80, 0, 0, 0, 0}); got != "audio/aac" {
		t.Errorf("adts aac: got %s", got)
	}
	if got := SniffMediaType([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0}); got != "image/jpeg" {
		t.Errorf("jpeg must not be taken for audio: got %s", got)
	}
	if got := ExtensionForMime("audio/aac"); got != ".aac" {
		t.Errorf("aac ext: got %s", got)
	}
	if got := ExtensionForMime("video/webm"); got != ".webm" {
		t.Errorf("webm ext: got %s", got)
	}
}

func TestWriteFileAtomicReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	if err := WriteFileAtomic(path, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "two" {
		t.Fatalf("content = %q", got)
	}
	noPartFiles(t, filepath.Dir(path))
}

func TestSafeFileComponent(t *testing.T) {
	cases := map[string]string{
		"abc-123_x":        "abc-123_x",
		"../../etc/passwd": "etc_passwd",
		`C:\x:y`:           "C_x_y",
		"":                 "",
		"nul":              "nul_",
		"___":              "",
	}
	for in, want := range cases {
		if got := SafeFileComponent(in, 32); got != want {
			t.Errorf("SafeFileComponent(%q) = %q, want %q", in, got, want)
		}
	}
	if got := SafeFileComponent("abcdefghijklmnop", 5); got != "abcde" {
		t.Errorf("truncation: %q", got)
	}
}
