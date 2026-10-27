package clientapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	voiceclient "uniclog.io/sonoryx/internal/client"
	"uniclog.io/sonoryx/internal/persist"
	"uniclog.io/sonoryx/internal/protocol"
	"uniclog.io/sonoryx/internal/protocol/protocoltest"
	"uniclog.io/sonoryx/internal/transport/udp"
)

func fileHTTPApp(t *testing.T, handler http.HandlerFunc) (*App, string) {
	t.Helper()
	https := httptest.NewTLSServer(handler)
	t.Cleanup(https.Close)
	endpoint := netip.MustParseAddrPort(https.Listener.Addr().String())
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { socket.Close() })
	raw, err := net.DialUDP("udp4", nil, socket.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	serverCodec, clientCodec := protocoltest.Pair(t, 9)
	server, err := udp.NewServerPacketConn(socket, serverCodec)
	if err != nil {
		t.Fatal(err)
	}
	client, err := udp.NewClientPacketConn(raw, clientCodec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	if err = client.BindSession(9); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t)
	t.Cleanup(a.cancelLifetime)
	a.state = voiceclient.NewState(9, "alice")
	a.state.SetConnectionStatus(voiceclient.ConnectionConnected)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.chatSession = &chatSession{ctx: ctx, cancel: cancel}
	a.currentConn = client
	a.serverEndpoint = endpoint
	payload, _ := json.Marshal(protocol.FileAccess{Port: endpoint.Port(), Fingerprint: certificateFingerprint(https.Certificate()), Credential: "secret"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			packet, _, err := server.ReadPacket()
			if err != nil {
				return
			}
			a.state.CompleteRequest(voiceclient.ControlResponse{Type: protocol.PacketFileAccessAck, RequestID: packet.RequestID, Payload: payload})
		}
	}()
	t.Cleanup(func() { socket.Close(); <-done })
	return a, a.ChatContext() + "|0"
}

func TestFileHTTPBudgetWaitsUntilResponseClosedAndCanCancel(t *testing.T) {
	var calls atomic.Int32
	a, expected := fileHTTPApp(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write([]byte("ok")) })
	var closes []func()
	defer func() {
		for _, close := range closes {
			close()
		}
	}()
	for i := 0; i < 2; i++ {
		_, close, err := a.fileRequest(context.Background(), expected, "GET", "/files/v1/metadata", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		closes = append(closes, close)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := a.fileRequest(ctx, expected, "GET", "/files/v1/metadata", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting request error: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatal("exceeded HTTP budget", calls.Load())
	}
	closes[0]()
	closes[0]() // Closing a response twice must not consume another request's slot.
	closes = closes[1:]
	_, close, err := a.fileRequest(context.Background(), expected, "GET", "/files/v1/metadata", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	close()
}

func TestFileHTTPBusyRetriesAndRewindsJSON(t *testing.T) {
	var calls atomic.Int32
	a, expected := fileHTTPApp(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != `["1","2"]` {
			t.Errorf("request body not replayed: %q", body)
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(429)
			w.Write([]byte(`{"code":"busy"}`))
			return
		}
		w.Write([]byte(`[]`))
	})
	if _, err := a.FileMetadata(context.Background(), expected, []string{"1", "2"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("busy was not retried", calls.Load())
	}
}

func TestFileHTTPBusyBackoffCanCancel(t *testing.T) {
	var calls atomic.Int32
	a, expected := fileHTTPApp(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(429)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := a.FileMetadata(ctx, expected, []string{"1"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("backoff ignored cancellation: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("retried after cancellation", calls.Load())
	}
}

func TestVerifiedUploadRejectsInvalidMetadata(t *testing.T) {
	for _, state := range []string{"ready", "attached"} {
		for _, field := range []string{"id", "size", "hash", "attachment"} {
			t.Run(state+"/"+field, func(t *testing.T) {
				task := FileTask{UploadID: "upload", Size: 10, SHA256: "hash", State: "running"}
				upload := persist.FileUpload{ID: "upload", Size: 10, SHA256: "hash", State: state, AttachmentID: 1}
				switch field {
				case "id":
					upload.ID = "other"
				case "size":
					upload.Size++
				case "hash":
					upload.SHA256 = "other"
				case "attachment":
					upload.AttachmentID = 0
				}
				if err := (&FileQueue{}).waitVerified(context.Background(), &task, upload); err == nil {
					t.Fatal("accepted invalid completed upload")
				}
				if task.State != "running" || task.AttachmentID != 0 {
					t.Fatal("invalid response changed the task")
				}
			})
		}
	}
}

func TestAttachmentCompletionRetriesFailedLocalCheckpoint(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	q := &FileQueue{path: filepath.Join(blocked, "queue.json"), tasks: []FileTask{{ID: "one", Context: "ctx", State: "ready"}}}
	if err := q.Control("ctx", "one", "attached"); err == nil {
		t.Fatal("accepted failed checkpoint")
	}
	if q.tasks[0].State != "ready" {
		t.Fatal("failed save prevented retry")
	}
	q.path = filepath.Join(t.TempDir(), "queue.json")
	if err := q.Control("ctx", "one", "attached"); err != nil {
		t.Fatal(err)
	}
	if err := q.Control("ctx", "one", "attached"); err != nil {
		t.Fatal("completion not idempotent", err)
	}
	data, err := os.ReadFile(q.path)
	if err != nil {
		t.Fatal(err)
	}
	var tasks []FileTask
	if err = json.Unmarshal(data, &tasks); err != nil || len(tasks) != 1 || tasks[0].State != "done" {
		t.Fatal("completion was not persisted", err)
	}
}

func TestFilePinCheckedBeforeSendingCredentialOrBody(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing credential")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "file bytes" {
			t.Error("unexpected body")
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	wrong := pinnedFileTransport(strings.Repeat("0", 64))
	defer wrong.CloseIdleConnections()
	client := &http.Client{Transport: wrong}
	request, _ := http.NewRequest("POST", server.URL, strings.NewReader("file bytes"))
	request.Header.Set("Authorization", "Bearer secret")
	if response, err := client.Do(request); err == nil {
		response.Body.Close()
		t.Fatal("accepted wrong pin")
	}
	if calls.Load() != 0 {
		t.Fatal("sent HTTP credentials before pin validation")
	}
	correct := pinnedFileTransport(certificateFingerprint(server.Certificate()))
	defer correct.CloseIdleConnections()
	client.Transport = correct
	request, _ = http.NewRequest("POST", server.URL, strings.NewReader("file bytes"))
	request.Header.Set("Authorization", "Bearer secret")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if calls.Load() != 1 {
		t.Fatal("correct pin request missing")
	}
}
