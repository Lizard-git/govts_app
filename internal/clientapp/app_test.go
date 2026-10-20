package clientapp

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"uniclog.io/sonoryx/internal/audio"
	voiceclient "uniclog.io/sonoryx/internal/client"
)

func TestAppValidatesConnectAndClosesIdempotently(t *testing.T) {
	app := newTestApp(t)
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

func TestChannelJoinErrorDoesNotRevealLevel(t *testing.T) {
	err := channelJoinError("Закрытый", errors.New("cannot join channel: channel join level is too low"))
	if err.Error() != "нет доступа к каналу \"Закрытый\"" {
		t.Fatalf("join error = %q", err)
	}
	if strings.Contains(err.Error(), "level") {
		t.Fatal("join error revealed a level")
	}
}

func TestAppConnectDisconnectAndReconnectLifecycle(t *testing.T) {
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(server.LocalAddr().(*net.UDPAddr).Port))

	app := newTestApp(t)
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

func TestAppDefaultsToSystemAudioDevices(t *testing.T) {
	selection := newTestApp(t).AudioDeviceSelection()
	if selection.CaptureID != "" || selection.PlaybackID != "" {
		t.Fatalf("default audio devices = %+v, want empty system-default IDs", selection)
	}
}

func TestAppSelectsOnlyAvailableAudioDevices(t *testing.T) {
	app := newTestApp(t)
	app.listAudioDevices = func() (audio.DeviceList, error) {
		return audio.DeviceList{
			Capture:  []audio.DeviceInfo{{ID: "capture-1", Name: "Microphone"}},
			Playback: []audio.DeviceInfo{{ID: "playback-1", Name: "Speakers"}},
		}, nil
	}

	if err := app.SetCaptureDevice("capture-1"); err != nil {
		t.Fatal(err)
	}
	if err := app.SetPlaybackDevice("playback-1"); err != nil {
		t.Fatal(err)
	}
	if got := app.AudioDeviceSelection(); got.CaptureID != "capture-1" || got.PlaybackID != "playback-1" {
		t.Fatalf("audio device selection = %+v", got)
	}
	if err := app.SetCaptureDevice("missing"); err == nil {
		t.Fatal("unavailable capture device was accepted")
	}
	if err := app.SetPlaybackDevice(""); err != nil {
		t.Fatal(err)
	}
	if got := app.AudioDeviceSelection().PlaybackID; got != "" {
		t.Fatalf("default playback device ID = %q, want empty", got)
	}
}

func TestAppHotSwitchesAudioDeviceWithoutChangingConnectionState(t *testing.T) {
	app := newTestApp(t)
	app.listAudioDevices = func() (audio.DeviceList, error) {
		return audio.DeviceList{Capture: []audio.DeviceInfo{{ID: "capture-1", Name: "Microphone"}}}, nil
	}
	app.mu.Lock()
	app.active = true
	app.mu.Unlock()
	app.state.SetConnectionStatus(voiceclient.ConnectionConnected)

	applied := make(chan AudioDeviceSelection, 1)
	go func() {
		request := <-app.audioDeviceChanges
		applied <- request.selection
		request.done <- nil
	}()

	if err := app.SetCaptureDevice("capture-1"); err != nil {
		t.Fatal(err)
	}
	selection := <-applied
	if selection.CaptureID != "capture-1" {
		t.Fatalf("applied capture device = %q", selection.CaptureID)
	}
	if status := app.state.ConnectionStatus(); status != voiceclient.ConnectionConnected {
		t.Fatalf("connection status after device switch = %v", status)
	}
	if got := app.AudioDeviceSelection().CaptureID; got != "capture-1" {
		t.Fatalf("stored capture device = %q", got)
	}
}

// newTestApp returns an App whose client identity and server pins live in the
// test's temporary directory instead of the user's Sonoryx profile.
func newTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	return New(Options{
		IdentityPath: filepath.Join(dir, "client.seed"),
		PinsPath:     filepath.Join(dir, "voice-pins.json"),
	})
}
