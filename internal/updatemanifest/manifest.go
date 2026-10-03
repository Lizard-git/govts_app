package updatemanifest

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
)

const Filename = "GTS64.exe"
const AssetName = "signature"
const MaxSize int64 = 512 << 20

type Manifest struct {
	Version   string `json:"version"`
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	Digest    []byte `json:"sha256"`
	Signature []byte `json:"signature"`
}

type Envelope struct {
	Payload   []byte `json:"payload"`
	Signature []byte `json:"signature"`
}

func Sign(version string, binary []byte, key ed25519.PrivateKey) ([]byte, error) {
	digest := sha256.Sum256(binary)
	m := Manifest{version, Filename, int64(len(binary)), digest[:], ed25519.Sign(key, digest[:])}
	payload, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{payload, ed25519.Sign(key, payload)})
}

func Verify(data []byte, key ed25519.PublicKey) (Manifest, error) {
	var e Envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return Manifest{}, err
	}
	if len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, e.Payload, e.Signature) {
		return Manifest{}, errors.New("подпись манифеста обновления недействительна")
	}
	var m Manifest
	if err := json.Unmarshal(e.Payload, &m); err != nil {
		return m, err
	}
	if m.Filename != Filename || m.Size <= 0 || m.Size > MaxSize || len(m.Digest) != sha256.Size || !ed25519.Verify(key, m.Digest, m.Signature) {
		return m, errors.New("неверные параметры подписанного обновления")
	}
	return m, nil
}
