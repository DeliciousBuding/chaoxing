package persist

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriteReplacesExistingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != `{"version":2}` {
		t.Fatal(err, string(b))
	}
	if _, err = os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary state file remains", err)
	}
}

func TestConcurrentWriteUsesUniqueTemporaryFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(value byte) {
			defer wg.Done()
			if err := Write(path, []byte{value}, 0o600); err != nil {
				t.Error(err)
			}
		}(byte('a' + i))
	}
	wg.Wait()
	if b, err := os.ReadFile(path); err != nil || len(b) != 1 {
		t.Fatal(err, b)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "state.json" {
			t.Fatalf("temporary file remains: %s", entry.Name())
		}
	}
}
