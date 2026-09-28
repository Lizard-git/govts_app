package identity

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var trustMu sync.Mutex

// CheckOrTrust pins the signing key on first contact with this voice endpoint.
func CheckOrTrust(path, endpoint string, publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize || endpoint == "" || path == "" {
		return errors.New("invalid voice server identity")
	}
	trustMu.Lock()
	defer trustMu.Unlock()
	pins := make(map[string]string)
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &pins); err != nil {
			return fmt.Errorf("decode voice server pins: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(publicKey)
	if previous, known := pins[endpoint]; known {
		if previous != encoded {
			return fmt.Errorf("voice server identity changed for %s", endpoint)
		}
		return nil
	}
	pins[endpoint] = encoded
	data, err = json.MarshalIndent(pins, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".voice-pins-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
