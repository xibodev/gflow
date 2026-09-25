package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func tempPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "history.json")
	SetPathOverride(p)
	t.Cleanup(func() { SetPathOverride("") })
	return p
}

func TestAddListOrderAndCap(t *testing.T) {
	tempPath(t)
	for i := 0; i < 3; i++ {
		if err := Add(Entry{ID: string(rune('a' + i)), Type: "image", Prompt: "p"}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := List(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].ID != "c" {
		t.Fatalf("newest-first failed: %+v", entries)
	}
	// Cap at 500: pre-fill then add one more.
	for i := 0; i < 500; i++ {
		_ = Add(Entry{ID: fmt.Sprintf("x%d", i), Type: "image"})
	}
	_ = Add(Entry{ID: "new", Type: "image"})
	all, _ := List(0)
	if len(all) != 500 || all[0].ID != "new" {
		t.Fatalf("want cap 500 with newest first, got %d first=%v", len(all), func() string {
			if len(all) > 0 {
				return all[0].ID
			}
			return "<empty>"
		}())
	}
}

func TestCorruptHistoryPreserved(t *testing.T) {
	p := tempPath(t)
	_ = os.WriteFile(p, []byte("{corrupt"), 0600)
	if err := Add(Entry{ID: "n", Type: "image"}); err == nil {
		t.Fatalf("corrupt history must not be silently replaced")
	}
	data, _ := os.ReadFile(p)
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil {
		// still corrupt raw; ensure untouched
		if string(data) != "{corrupt" {
			t.Fatalf("corrupt file was modified")
		}
	}
}

func TestConcurrentAdds(t *testing.T) {
	tempPath(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = Add(Entry{ID: string(rune('0'+i%10)) + string(rune('a'+i)), Type: "image"})
		}(i)
	}
	wg.Wait()
	entries, err := List(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 20 {
		t.Fatalf("want 20 entries, got %d (lost writes)", len(entries))
	}
}
