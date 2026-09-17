package clientsettings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsMissingRNNoiseSensitivityToFull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	data := []byte(`{"version":1,"rnnoiseEnabled":true}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := NewStore(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if settings.RNNoiseSensitivity != 1 {
		t.Fatalf("RNNoise sensitivity = %v, want 1", settings.RNNoiseSensitivity)
	}
}
