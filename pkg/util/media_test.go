package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xibodev/gflow/pkg/models"
)

func TestSniffMediaType(t *testing.T) {
	pngHeader := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00}
	if got := SniffMediaType(pngHeader); got != "image/png" {
		t.Errorf("expected image/png, got %s", got)
	}

	jpegHeader := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46}
	if got := SniffMediaType(jpegHeader); got != "image/jpeg" {
		t.Errorf("expected image/jpeg, got %s", got)
	}

	webpHeader := []byte{'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'E', 'B', 'P'}
	if got := SniffMediaType(webpHeader); got != "image/webp" {
		t.Errorf("expected image/webp, got %s", got)
	}

	mp4Header := []byte{0x00, 0x00, 0x00, 0x20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	if got := SniffMediaType(mp4Header); got != "video/mp4" {
		t.Errorf("expected video/mp4, got %s", got)
	}
}

func TestExtensionForMime(t *testing.T) {
	cases := map[string]string{
		"image/png":  ".png",
		"image/jpeg": ".jpg",
		"image/webp": ".webp",
		"video/mp4":  ".mp4",
		"other/data": ".bin",
	}

	for mime, expected := range cases {
		if got := ExtensionForMime(mime); got != expected {
			t.Errorf("for mime %s expected %s, got %s", mime, expected, got)
		}
	}
}

func TestDecodeBase64AndSave(t *testing.T) {
	tmpDir := t.TempDir()
	targetFile := filepath.Join(tmpDir, "test.png")

	// 1x1 transparent PNG base64
	sampleB64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

	err := DecodeBase64AndSave(sampleB64, targetFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("could not read saved file: %v", err)
	}

	if SniffMediaType(data) != "image/png" {
		t.Errorf("expected saved file to be image/png, got %s", SniffMediaType(data))
	}
}

func TestSaveAssetConstructsDefaultFilename(t *testing.T) {
	tmpDir := t.TempDir()
	asset := &models.Asset{
		ID:       "12345678-abcd-ef01-2345-6789abcdef01",
		Type:     "image",
		MimeType: "image/png",
	}

	// When URL is empty, SaveAsset should report error
	_, err := SaveAsset(t.Context(), asset, tmpDir)
	if err == nil {
		t.Error("expected error when URL is empty")
	}
}
