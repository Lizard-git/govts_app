package auth

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"unicode/utf8"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const MaxPasswordBytes = 256

func ValidatePassword(password string) error {
	if !utf8.ValidString(password) || len(password) > MaxPasswordBytes {
		return errors.New("server-password-invalid")
	}
	return nil
}

// Passwords are sent only after the signed server challenge and identity pin
// have been verified. A separate handshake key keeps this extension independent
// of the session traffic and acknowledgement keys.
func passwordCipher(ackKey, digest [32]byte) (cipher.AEAD, error) {
	var key [chacha20poly1305.KeySize]byte
	reader := hkdf.New(sha256.New, ackKey[:], digest[:], []byte("govts-auth-password-v1"))
	if _, err := io.ReadFull(reader, key[:]); err != nil {
		return nil, err
	}
	return chacha20poly1305.New(key[:])
}

// SealPassword returns nonce(12) || ciphertext || tag(16). The caller reuses
// this exact payload for UDP retries rather than encrypting it again.
func SealPassword(ackKey, digest [32]byte, password string) ([]byte, error) {
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	aead, err := passwordCipher(ackKey, digest)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(password), digest[:]), nil
}

func OpenPassword(ackKey, digest [32]byte, payload []byte) (string, error) {
	aead, err := passwordCipher(ackKey, digest)
	if err != nil {
		return "", err
	}
	minimum := aead.NonceSize() + aead.Overhead()
	if len(payload) < minimum || len(payload) > minimum+MaxPasswordBytes {
		return "", errors.New("invalid encrypted server password")
	}
	plain, err := aead.Open(nil, payload[:aead.NonceSize()], payload[aead.NonceSize():], digest[:])
	if err != nil {
		return "", errors.New("invalid encrypted server password")
	}
	password := string(plain)
	return password, ValidatePassword(password)
}
