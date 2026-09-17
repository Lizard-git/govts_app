package clientapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"sync"
	"time"

	"example.com/go-voice-mvp/internal/audio"
	"example.com/go-voice-mvp/internal/audio/voicegate"
	voiceclient "example.com/go-voice-mvp/internal/client"
	"example.com/go-voice-mvp/internal/domain"
	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

const handshakeAttemptTimeout = 3 * time.Second
const audioDeviceChangeTimeout = 10 * time.Second

var (
	ErrAlreadyConnected = errors.New("a connection is already active")
	ErrNotConnected     = errors.New("client is not connected")
	ErrClosed           = errors.New("client application is closed")
)

type AudioDeviceSelection struct {
	CaptureID  string
	PlaybackID string
}

type audioDeviceChange struct {
	selection AudioDeviceSelection
	done      chan error
}

type Options struct {
	Commands <-chan voiceclient.Command
	Output   io.Writer
	Logger   *log.Logger
}

type ConnectOptions struct {
	Name           string
	Server         string
	InitialChannel string
}

type App struct {
	lifetimeCtx         context.Context
	cancelLifetime      context.CancelFunc
	state               *voiceclient.State
	events              *EventLog
	commands            <-chan voiceclient.Command
	output              io.Writer
	logger              *log.Logger
	listAudioDevices    func() (audio.DeviceList, error)
	audioDeviceChangeMu sync.Mutex
	audioDeviceChanges  chan audioDeviceChange
	captureDeviceID     string
	playbackDeviceID    string

	mu          sync.Mutex
	closed      bool
	active      bool
	runCancel   context.CancelFunc
	runDone     chan struct{}
	currentConn *udp.ClientPacketConn
	preference  *channelPreference
	lastError   string
}

func New(options Options) *App {
	ctx, cancel := context.WithCancel(context.Background())
	state := voiceclient.NewState(0, "")
	state.SetConnectionStatus(voiceclient.ConnectionDisconnected)
	if options.Output == nil {
		options.Output = io.Discard
	}
	if options.Logger == nil {
		options.Logger = log.New(io.Discard, "", 0)
	}
	return &App{
		lifetimeCtx:        ctx,
		cancelLifetime:     cancel,
		state:              state,
		events:             newEventLog(),
		commands:           options.Commands,
		output:             options.Output,
		logger:             options.Logger,
		listAudioDevices:   audio.ListDevices,
		audioDeviceChanges: make(chan audioDeviceChange),
	}
}

func (a *App) Connect(options ConnectOptions) error {
	if options.Name == "" {
		return errors.New("display name is required")
	}
	endpoint, err := ParseServerEndpoint(options.Server)
	if err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosed
	}
	if a.active {
		return ErrAlreadyConnected
	}
	if err := a.state.PrepareConnection(); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(a.lifetimeCtx)
	done := make(chan struct{})
	a.active = true
	a.runCancel = cancel
	a.runDone = done
	a.lastError = ""
	a.preference = newChannelPreference(options.InitialChannel)
	a.events.clear()
	a.events.append("connection", "Подключение к "+endpoint.String(), 0)
	go a.run(runCtx, done, endpoint, options)
	return nil
}

func (a *App) run(ctx context.Context, done chan struct{}, endpoint netip.AddrPort, options ConnectOptions) {
	err := a.runConnection(ctx, endpoint, options.Name)

	a.mu.Lock()
	a.currentConn = nil
	a.active = false
	a.runCancel = nil
	if err != nil && !errors.Is(err, context.Canceled) {
		a.lastError = err.Error()
	}
	a.mu.Unlock()
	a.state.SetConnectionStatus(voiceclient.ConnectionDisconnected)
	if err != nil && !errors.Is(err, context.Canceled) {
		a.events.append("error", err.Error(), 0)
	} else {
		a.events.append("connection", "Отключено", 0)
	}
	close(done)
}

func (a *App) runConnection(ctx context.Context, endpoint netip.AddrPort, name string) (runErr error) {
	conn, err := openClientPacketConn(endpoint)
	if err != nil {
		return err
	}
	defer func() {
		if err := conn.Close(); err != nil && !shouldReplaceClientSocket(err) {
			runErr = errors.Join(runErr, fmt.Errorf("close UDP connection: %w", err))
		}
	}()

	backoff := newReconnectBackoff()
	everConnected := false
	for {
		if ctx.Err() != nil {
			return nil
		}
		if !everConnected {
			a.state.SetConnectionStatus(voiceclient.ConnectionConnecting)
		} else {
			a.state.SetConnectionStatus(voiceclient.ConnectionReconnecting)
		}
		sessionID, err := voiceclient.PerformHandshakeAttempt(ctx, conn, name, handshakeAttemptTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if shouldReplaceClientSocket(err) {
				replacement, replaceErr := openClientPacketConn(endpoint)
				if replaceErr == nil {
					_ = conn.Close()
					conn = replacement
				}
			}
			delay := backoff.Next()
			a.events.append("reconnect", fmt.Sprintf("Сервер недоступен; повтор через %s", delay), 0)
			if !a.waitForReconnect(ctx, delay) {
				return nil
			}
			continue
		}
		if err := conn.BindSession(sessionID); err != nil {
			return fmt.Errorf("bind UDP session: %w", err)
		}
		if _, err := a.state.StartSession(sessionID); err != nil {
			return fmt.Errorf("start client session: %w", err)
		}
		a.mu.Lock()
		a.currentConn = conn
		a.mu.Unlock()
		devices := a.AudioDeviceSelection()
		noticeOutput := io.MultiWriter(a.output, eventWriter{log: a.events})
		sessionErr := runSession(ctx, conn, a.state, devices, a.audioDeviceChanges, name, a.preference, a.commands, a.output, noticeOutput, func() { a.cancelRun() }, a.logger, !everConnected, func() {
			backoff.Reset()
			if everConnected {
				a.events.append("connection", "Соединение восстановлено", uint64(a.state.SnapshotView().Revision))
			} else {
				a.events.append("connection", "Подключено", uint64(a.state.SnapshotView().Revision))
			}
			everConnected = true
		})
		a.mu.Lock()
		a.currentConn = nil
		a.mu.Unlock()
		if ctx.Err() != nil {
			return nil
		}
		a.state.InvalidateSession(voiceclient.ConnectionReconnecting)
		if err := conn.ClearSession(sessionID); err != nil {
			return fmt.Errorf("clear UDP session: %w", err)
		}
		if !errors.Is(sessionErr, voiceclient.ErrConnectionLost) {
			return sessionErr
		}
		delay := backoff.Next()
		a.events.append("reconnect", fmt.Sprintf("Соединение потеряно; повтор через %s", delay), 0)
		if !a.waitForReconnect(ctx, delay) {
			return nil
		}
	}
}

func (a *App) AudioDevices() (audio.DeviceList, error) { return a.listAudioDevices() }

func (a *App) AudioDeviceSelection() AudioDeviceSelection {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AudioDeviceSelection{CaptureID: a.captureDeviceID, PlaybackID: a.playbackDeviceID}
}

func (a *App) SetCaptureDevice(id string) error {
	return a.setAudioDevice(id, true)
}

func (a *App) SetPlaybackDevice(id string) error {
	return a.setAudioDevice(id, false)
}

func (a *App) setAudioDevice(id string, capture bool) error {
	a.audioDeviceChangeMu.Lock()
	defer a.audioDeviceChangeMu.Unlock()

	devices, err := a.listAudioDevices()
	if err != nil {
		return err
	}
	available := devices.Playback
	if capture {
		available = devices.Capture
	}
	if id != "" {
		found := false
		for _, device := range available {
			if device.ID == id {
				found = true
				break
			}
		}
		if !found {
			return errors.New("audio device is no longer available")
		}
	}

	a.mu.Lock()
	selection := AudioDeviceSelection{CaptureID: a.captureDeviceID, PlaybackID: a.playbackDeviceID}
	changed := (capture && selection.CaptureID != id) || (!capture && selection.PlaybackID != id)
	active := a.active
	a.mu.Unlock()
	if !changed {
		return nil
	}
	if capture {
		selection.CaptureID = id
	} else {
		selection.PlaybackID = id
	}

	if active && a.state.ConnectionStatus() == voiceclient.ConnectionConnected {
		request := audioDeviceChange{selection: selection, done: make(chan error, 1)}
		timer := time.NewTimer(audioDeviceChangeTimeout)
		defer timer.Stop()
		select {
		case a.audioDeviceChanges <- request:
		case <-timer.C:
			return errors.New("timed out while applying audio device")
		case <-a.lifetimeCtx.Done():
			return ErrClosed
		}
		select {
		case err := <-request.done:
			if err != nil {
				return err
			}
		case <-timer.C:
			return errors.New("timed out while applying audio device")
		case <-a.lifetimeCtx.Done():
			return ErrClosed
		}
	}
	a.mu.Lock()
	a.captureDeviceID = selection.CaptureID
	a.playbackDeviceID = selection.PlaybackID
	a.mu.Unlock()
	a.events.append("audio", "Аудиоустройство изменено", 0)
	return nil
}

func (a *App) waitForReconnect(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		case command, ok := <-a.commands:
			if !ok {
				a.commands = nil
				continue
			}
			voiceclient.HandleOfflineCommand(command, a.output, func() { a.cancelRun() }, a.state)
		}
	}
}

func (a *App) cancelRun() {
	a.mu.Lock()
	cancel := a.runCancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *App) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	cancel := a.runCancel
	done := a.runDone
	a.mu.Unlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) Close(ctx context.Context) error {
	a.mu.Lock()
	if !a.closed {
		a.closed = true
		a.cancelLifetime()
	}
	a.mu.Unlock()
	return a.Disconnect(ctx)
}

func (a *App) Wait(ctx context.Context) error {
	a.mu.Lock()
	done := a.runDone
	a.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		a.mu.Lock()
		errText := a.lastError
		a.mu.Unlock()
		if errText != "" {
			return errors.New(errText)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) JoinChannel(ctx context.Context, channelID domain.ChannelID) error {
	if channelID == 0 {
		return errors.New("channel ID must not be zero")
	}
	a.mu.Lock()
	conn := a.currentConn
	preference := a.preference
	a.mu.Unlock()
	if conn == nil || a.state.ConnectionStatus() != voiceclient.ConnectionConnected {
		return ErrNotConnected
	}
	snapshot := a.state.Snapshot()
	locator, err := voiceclient.BuildChannelLocator(snapshot, channelID)
	if err != nil {
		return err
	}
	if err := voiceclient.JoinChannel(ctx, conn, a.state, channelID); err != nil {
		return err
	}
	preference.Set(locator)
	return nil
}

func (a *App) Snapshot() voiceclient.ClientViewState { return a.state.SnapshotView() }

func (a *App) AudioMeterSnapshot() voiceclient.AudioMeterSample {
	return a.state.Audio.AudioMeterSnapshot()
}

func (a *App) Subscribe(ctx context.Context) (<-chan struct{}, func()) {
	return a.state.Subscribe(ctx)
}

func (a *App) EventsAfter(sequence uint64) []ClientEvent { return a.events.after(sequence) }

func (a *App) SubscribeEvents(ctx context.Context) (<-chan struct{}, func()) {
	return a.events.subscribe(ctx)
}

func (a *App) LastError() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastError
}

func (a *App) SetMuted(value bool) { a.state.Audio.SetMuted(value) }

func (a *App) SetDeafened(value bool) error { return a.state.Audio.SetDeafened(value) }

func (a *App) SetRNNoiseEnabled(value bool) { a.state.Audio.SetRNNoiseEnabled(value) }

func (a *App) SetRNNoiseSensitivity(value float32) error {
	return a.state.Audio.SetRNNoiseSensitivity(value)
}

func (a *App) SetVADEnabled(value bool) { a.state.Audio.SetVADEnabled(value) }

func (a *App) SetVADMode(value string) error {
	mode, err := voicegate.ParseMode(value)
	if err != nil {
		return err
	}
	return a.state.Audio.SetVADMode(mode)
}

func (a *App) SetVADSensitivity(value float32) error {
	return a.state.Audio.SetVADSensitivity(value)
}

func ParseServerEndpoint(server string) (netip.AddrPort, error) {
	if ip, err := netip.ParseAddr(server); err == nil {
		return netip.AddrPortFrom(ip, 9000), nil
	}
	endpoint, err := netip.ParseAddrPort(server)
	if err != nil || endpoint.Port() == 0 {
		return netip.AddrPort{}, fmt.Errorf("invalid server address %q: use IP or IP:port with port 1..65535 (IPv6: [IP]:port)", server)
	}
	return endpoint, nil
}

func openClientPacketConn(endpoint netip.AddrPort) (*udp.ClientPacketConn, error) {
	rawConn, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(endpoint))
	if err != nil {
		return nil, fmt.Errorf("connect UDP: %w", err)
	}
	conn, err := udp.NewClientPacketConn(rawConn, protocol.PlainDatagramCodec{})
	if err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("configure UDP packet connection: %w", err)
	}
	return conn, nil
}
