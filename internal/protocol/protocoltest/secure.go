// Package protocoltest provides secure transport fixtures for tests that
// exercise packet handling without running the authentication handshake.
package protocoltest

import (
	"crypto/sha256"
	"encoding/binary"
	"testing"

	"uniclog.io/sonoryx/internal/protocol"
)

// Install gives every codec the same deterministic keys for session id, so a
// server codec and a client codec can exchange that session's records.
func Install(t testing.TB, id uint64, codecs ...*protocol.SecureDatagramCodec) {
	t.Helper()
	clientToServer, serverToClient := sessionKeys(id)
	for _, codec := range codecs {
		if err := codec.Install(id, clientToServer, serverToClient); err != nil {
			t.Fatalf("install secure session %d: %v", id, err)
		}
	}
}

// Pair returns a server and a client codec that share keys for each id.
func Pair(t testing.TB, ids ...uint64) (server, client *protocol.SecureDatagramCodec) {
	t.Helper()
	server = protocol.NewSecureDatagramCodec(true)
	client = protocol.NewSecureDatagramCodec(false)
	for _, id := range ids {
		Install(t, id, server, client)
	}
	return server, client
}

func sessionKeys(id uint64) (clientToServer, serverToClient [32]byte) {
	var seed [9]byte
	binary.BigEndian.PutUint64(seed[1:], id)
	seed[0] = 'c'
	clientToServer = sha256.Sum256(seed[:])
	seed[0] = 's'
	serverToClient = sha256.Sum256(seed[:])
	return clientToServer, serverToClient
}
