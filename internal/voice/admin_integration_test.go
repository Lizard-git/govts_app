package voice_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"uniclog.io/sonoryx/internal/appversion"
	"uniclog.io/sonoryx/internal/client"
	"uniclog.io/sonoryx/internal/persist"
	"uniclog.io/sonoryx/internal/protocol"
	"uniclog.io/sonoryx/internal/transport/udp"
	"uniclog.io/sonoryx/internal/voice"
)

type adminClient struct {
	t      *testing.T
	conn   *udp.ClientPacketConn
	id     uint64
	result client.SecureHandshakeResult
	next   uint32
}

func (c *adminClient) request(request protocol.AdminRequest) protocol.VoicePacket {
	c.t.Helper()
	payload, err := protocol.EncodeAdminRequest(request)
	if err != nil {
		c.t.Fatal(err)
	}
	c.next++
	if err := c.conn.SendPacket(protocol.VoicePacket{Type: protocol.PacketAdminRequest, SessionID: c.id, RequestID: c.next, Payload: payload}); err != nil {
		c.t.Fatal(err)
	}
	return c.receive(func(packet protocol.VoicePacket) bool { return packet.RequestID == c.next })
}

func (c *adminClient) receive(match func(protocol.VoicePacket) bool) protocol.VoicePacket {
	c.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			c.t.Fatal(err)
		}
		packet, err := c.conn.ReceivePacket()
		if err != nil {
			c.t.Fatalf("receive: %v", err)
		}
		if match(packet) {
			return packet
		}
	}
}

func (c *adminClient) ack(request protocol.AdminRequest) protocol.AdminResponse {
	c.t.Helper()
	packet := c.request(request)
	if packet.Type != protocol.PacketAdminAck {
		c.t.Fatalf("admin %d response = type %d %q", request.Operation, packet.Type, packet.Payload)
	}
	response, err := protocol.DecodeAdminResponse(packet.Payload)
	if err != nil {
		c.t.Fatal(err)
	}
	return response
}

func TestRemoteAdministrationOverSecureUDP(t *testing.T) {
	directory := t.TempDir()
	store, err := persist.Open(filepath.Join(directory, "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hub := voice.NewHub()
	version, err := appversion.Parse("0.3.0")
	if err != nil {
		t.Fatal(err)
	}
	hub.SetServerVersion(version)
	_, serverKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverCodec := protocol.NewSecureDatagramCodec(true)
	authenticator, err := voice.NewAuthenticator(serverKey, store, serverCodec)
	if err != nil {
		t.Fatal(err)
	}
	rawServer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	server, err := udp.NewServerPacketConn(rawServer, serverCodec)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	administration := voice.NewAdministration(store, hub)
	administration.SetPrivilegeNotifier(func(userID int64, level uint16, privileges uint8) {
		for _, session := range hub.Inspect().Sessions {
			if session.UserID == userID {
				_ = voice.SendToSession(server, session, protocol.VoicePacket{Type: protocol.PacketAccountPrivileges, SessionID: session.ID, Payload: []byte{byte(level >> 8), byte(level), privileges}})
			}
		}
	})
	authenticator.SetAdministration(administration)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = voice.ServeUDP(ctx, server, hub, voice.NewRequestCache(), authenticator) }()

	endpoint := server.LocalAddr().(*net.UDPAddr).AddrPort()
	connect := func(name string, private ed25519.PrivateKey) *adminClient {
		t.Helper()
		raw, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(endpoint))
		if err != nil {
			t.Fatal(err)
		}
		codec := protocol.NewSecureDatagramCodec(false)
		connection, err := udp.NewClientPacketConn(raw, codec)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = connection.Close() })
		result, err := client.PerformSecureHandshakeAttempts(ctx, connection, codec, name, private, filepath.Join(directory, "pins.json"), endpoint.String(), time.Second, 3, "0.3.0")
		if err != nil {
			t.Fatal(err)
		}
		if err := connection.BindSession(result.SessionID); err != nil {
			t.Fatal(err)
		}
		return &adminClient{t: t, conn: connection, id: result.SessionID, result: result, next: 1000}
	}
	account := func(name string) (ed25519.PrivateKey, persist.Account) {
		t.Helper()
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		var key [32]byte
		copy(key[:], public)
		created, err := store.FindOrCreateAccount(ctx, key, name)
		if err != nil {
			t.Fatal(err)
		}
		return private, created
	}
	ownerKey, ownerAccount := account("owner")
	if err := store.SetOwner(ctx, ownerAccount.ID); err != nil {
		t.Fatal(err)
	}
	userKey, userAccount := account("user")

	owner := connect("owner", ownerKey)
	if owner.result.Permissions&protocol.PrivilegeOwner == 0 {
		t.Fatalf("owner AuthAck privileges = %08b", owner.result.Permissions)
	}
	user := connect("user", userKey)
	if user.result.Permissions != 0 {
		t.Fatalf("user AuthAck privileges = %08b", user.result.Permissions)
	}

	denied := user.request(protocol.AdminRequest{Operation: protocol.AdminAccounts})
	if denied.Type != protocol.PacketError || !strings.Contains(string(denied.Payload), "permission denied") {
		t.Fatalf("non-admin response = type %d %q", denied.Type, denied.Payload)
	}

	page := owner.ack(protocol.AdminRequest{Operation: protocol.AdminAccounts})
	if len(page.Accounts) != 2 || page.Next != 0 {
		t.Fatalf("accounts page = %+v", page)
	}
	for _, listed := range page.Accounts {
		if listed.Flags&protocol.AdminAccountOnline == 0 {
			t.Fatalf("connected account not online: %+v", listed)
		}
		if (listed.ID == uint64(ownerAccount.ID)) != (listed.Flags&protocol.AdminAccountOwner != 0) {
			t.Fatalf("owner flag on %+v", listed)
		}
	}

	granted := owner.ack(protocol.AdminRequest{Operation: protocol.AdminSetPermission, TargetID: uint64(userAccount.ID), Permission: protocol.PrivilegeAdmin, Enabled: true})
	if len(granted.Accounts) != 1 || granted.Accounts[0].Permissions != protocol.PrivilegeAdmin {
		t.Fatalf("grant response = %+v", granted)
	}
	notice := user.receive(func(packet protocol.VoicePacket) bool { return packet.Type == protocol.PacketAccountPrivileges })
	if len(notice.Payload) != 3 || notice.Payload[2] != protocol.PrivilegeAdmin {
		t.Fatalf("privileges notification = %x", notice.Payload)
	}
	if session, ok := hub.Get(user.id); !ok || session.Permissions != persist.PermissionAdmin {
		t.Fatalf("user session after grant = %+v, %t", session, ok)
	}

	// The new administrator can read but cannot touch the owner.
	if audit := user.ack(protocol.AdminRequest{Operation: protocol.AdminAudit}); len(audit.Audit) == 0 || audit.Audit[0].Action != "set-permission" || audit.Audit[0].Actor != strconv.FormatInt(ownerAccount.ID, 10) {
		t.Fatalf("audit page = %+v", audit)
	}
	refused := user.request(protocol.AdminRequest{Operation: protocol.AdminSetJoinLevel, TargetID: uint64(ownerAccount.ID), Level: 1})
	if refused.Type != protocol.PacketError || !strings.Contains(string(refused.Payload), "permission denied") {
		t.Fatalf("admin changes owner = type %d %q", refused.Type, refused.Payload)
	}

	// A retransmission is answered from the cache, not executed twice.
	payload, err := protocol.EncodeAdminRequest(protocol.AdminRequest{Operation: protocol.AdminAudit})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := owner.conn.SendPacket(protocol.VoicePacket{Type: protocol.PacketAdminRequest, SessionID: owner.id, RequestID: 77, Payload: payload}); err != nil {
			t.Fatal(err)
		}
		if packet := owner.receive(func(packet protocol.VoicePacket) bool { return packet.RequestID == 77 }); packet.Type != protocol.PacketAdminAck {
			t.Fatalf("cached response = type %d %q", packet.Type, packet.Payload)
		}
	}

	banned := owner.ack(protocol.AdminRequest{Operation: protocol.AdminSetBan, TargetID: uint64(userAccount.ID), Enabled: true, Reason: "test"})
	if len(banned.Accounts) != 1 || banned.Accounts[0].Flags&protocol.AdminAccountBanned == 0 || banned.Accounts[0].BanReason != "test" {
		t.Fatalf("ban response = %+v", banned)
	}
	if _, ok := hub.Get(user.id); ok {
		t.Fatal("banned session survived")
	}

	// The per-session budget counts new requests only.
	var limited protocol.VoicePacket
	for range 40 {
		limited = owner.request(protocol.AdminRequest{Operation: protocol.AdminAccounts})
		if limited.Type == protocol.PacketError {
			break
		}
	}
	if limited.Type != protocol.PacketError || !strings.Contains(string(limited.Payload), "too many admin requests") {
		t.Fatalf("rate limit response = type %d %q", limited.Type, limited.Payload)
	}
}
