package clientapp

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"example.com/go-voice-mvp/internal/audio"
)

func TestAppValidatesConnectAndClosesIdempotently(t *testing.T) {
	app := New(Options{})
	if err := app.Connect(ConnectOptions{Server: "127.0.0.1:9000"}); err == nil {
		t.Fatal("Connect accepted an empty display name")
	}
	if err := app.Connect(ConnectOptions{Name: "alice", Server: "invalid"}); err == nil {
		t.Fatal("Connect accepted an invalid server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := app.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := app.Connect(ConnectOptions{Name: "alice", Server: "127.0.0.1:9000"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Connect after Close error = %v, want ErrClosed", err)
	}
}

func TestAppConnectDisconnectAndReconnectLifecycle(t *testing.T) {
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(server.LocalAddr().(*net.UDPAddr).Port))

	app := New(Options{})
	connect := func() {
		if err := app.Connect(ConnectOptions{Name: "alice", Server: address}); err != nil {
			t.Fatal(err)
		}
	}
	connect()
	if err := app.Connect(ConnectOptions{Name: "alice", Server: address}); !errors.Is(err, ErrAlreadyConnected) {
		t.Fatalf("duplicate Connect error = %v, want ErrAlreadyConnected", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := app.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	connect()
	if err := app.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := app.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAppReusesAudioOutputAcrossConnections(t *testing.T) {
	app := New(Options{})
	want := &audio.OtoOutput{}
	created := 0
	app.newAudioOutput = func(audio.CodecConfig) (*audio.OtoOutput, error) {
		created++
		return want, nil
	}

	first, err := app.playbackOutput()
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.playbackOutput()
	if err != nil {
		t.Fatal(err)
	}
	if first != want || second != want {
		t.Fatal("playbackOutput did not reuse the application-owned output")
	}
	if created != 1 {
		t.Fatalf("audio output created %d times, want 1", created)
	}
}
