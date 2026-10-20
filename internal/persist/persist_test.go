package persist

import (
	"os"
	"path/filepath"
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
