package identity

import (
	"bytes"
	"crypto/ed25519"
	"path/filepath"
	"testing"
)

func TestPersistentSeedAndServerPin(t *testing.T) {
	directory := t.TempDir()
	seedPath := filepath.Join(directory, "client.seed")
	first, err := LoadOrCreate(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("identity changed after restart")
	}
	firstPublic := first.Public().(ed25519.PublicKey)
	pinsPath := filepath.Join(directory, "pins.json")
	if err := CheckOrTrust(pinsPath, "127.0.0.1:9000", firstPublic); err != nil {
		t.Fatal(err)
	}
	if err := CheckOrTrust(pinsPath, "127.0.0.1:9000", firstPublic); err != nil {
		t.Fatal(err)
	}
	if err := CheckOrTrust(pinsPath, "127.0.0.1:9001", firstPublic); err != nil {
		t.Fatal(err)
	}
	other, err := LoadOrCreate(filepath.Join(directory, "other.seed"))
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckOrTrust(pinsPath, "127.0.0.1:9000", other.Public().(ed25519.PublicKey)); err == nil {
		t.Fatal("changed server identity was trusted")
	}
}
