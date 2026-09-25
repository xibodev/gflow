package util

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xibodev/gflow/pkg/models"
)

func pngServer(t *testing.T, payload []byte, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("nope"))
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(payload)
	}))
}

var tinyPNG = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x01}

func TestExplicitFilenameHonored(t *testing.T) {
	srv := pngServer(t, tinyPNG, 0)
	defer srv.Close()
	dir := t.TempDir()
	target := filepath.Join(dir, "clip.png")
	a := &models.Asset{ID: "abc123", Type: "image", URL: srv.URL, MimeType: "image/png"}
	got, err := SaveAsset(context.Background(), a, target)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Fatalf("explicit filename must be honored, got %s", got)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("file missing: %v", err)
	}
}

func TestDirectoryOutputGeneratesFile(t *testing.T) {
	srv := pngServer(t, tinyPNG, 0)
	defer srv.Close()
	dir := t.TempDir()
	a := &models.Asset{ID: "abc12345", Type: "image", URL: srv.URL, MimeType: "image/png"}
	got, err := SaveAsset(context.Background(), a, dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(got) != dir {
		t.Fatalf("expected file inside dir, got %s", got)
	}
}

func TestPreexistingFileNeverOverwritten(t *testing.T) {
	srv := pngServer(t, tinyPNG, 0)
	defer srv.Close()
	dir := t.TempDir()
	target := filepath.Join(dir, "clip.png")
	_ = os.WriteFile(target, []byte("original"), 0600)
	a := &models.Asset{ID: "abc", Type: "image", URL: srv.URL, MimeType: "image/png"}
	got, err := SaveAsset(context.Background(), a, target)
	if err != nil {
		t.Fatal(err)
	}
	if got == target {
		t.Fatalf("must not overwrite existing file")
	}
	orig, _ := os.ReadFile(target)
	if string(orig) != "original" {
		t.Fatalf("original clobbered")
	}
}

func TestDownload404AndHTMLRejected(t *testing.T) {
	srv := pngServer(t, nil, http.StatusNotFound)
	defer srv.Close()
	a := &models.Asset{ID: "x", Type: "image", URL: srv.URL, MimeType: "image/png"}
	if _, err := SaveAsset(context.Background(), a, t.TempDir()); err == nil {
		t.Fatalf("404 must fail")
	}
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>login</html>"))
	}))
	defer html.Close()
	b := &models.Asset{ID: "y", Type: "video", URL: html.URL, MimeType: "video/mp4"}
	if _, err := SaveAsset(context.Background(), b, t.TempDir()); err == nil {
		t.Fatalf("HTML payload must be rejected")
	}
	// No partial files left.
	entries, _ := os.ReadDir(t.TempDir())
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			t.Fatalf("partial file left behind")
		}
	}
}

func TestConcurrentSavesUnique(t *testing.T) {
	srv := pngServer(t, tinyPNG, 0)
	defer srv.Close()
	dir := t.TempDir()
	var wg sync.WaitGroup
	results := make([]string, 8)
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := &models.Asset{ID: "same-id", Type: "image", URL: srv.URL, MimeType: "image/png"}
			p, err := SaveAsset(context.Background(), a, dir)
			results[i], errs[i] = p, err
		}(i)
	}
	wg.Wait()
	seen := map[string]bool{}
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("save %d: %v", i, errs[i])
		}
		if seen[results[i]] {
			t.Fatalf("duplicate path %s", results[i])
		}
		seen[results[i]] = true
	}
}
