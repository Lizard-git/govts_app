package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadOrCreate stores only the 32-byte Ed25519 seed. The full private key is
// derived in memory and never written to the server's account database.
func LoadOrCreate(path string) (ed25519.PrivateKey, error) {
	if path == "" {
		return nil, errors.New("identity path is required")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		var seed [ed25519.SeedSize]byte
		if _, err := rand.Read(seed[:]); err != nil {
			return nil, err
		}
		file, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(createErr, os.ErrExist) {
			data, err = os.ReadFile(path)
		} else if createErr != nil {
			return nil, createErr
		} else {
			encoded := base64.StdEncoding.EncodeToString(seed[:]) + "\n"
			_, err = file.WriteString(encoded)
			closeErr := file.Close()
			if err != nil {
				return nil, err
			}
			if closeErr != nil {
				return nil, closeErr
			}
			data = []byte(encoded)
		}
	}
	if err != nil {
		return nil, err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("invalid Ed25519 seed at %q", path)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}
