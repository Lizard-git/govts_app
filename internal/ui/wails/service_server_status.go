package wailsui

import (
	"context"
	"net/netip"

	"github.com/wailsapp/wails/v3/pkg/application"
	"uniclog.io/govts/internal/clientapp"
)

func (s *Service) SetServerListVisible(consumer string, visible bool) {
	if s.serverStatus != nil {
		s.serverStatus.SetVisible(consumer, visible)
	}
}

func SetServerStatusSuspended(s *Service, value bool) { s.serverStatus.SetSuspended(value) }

func (s *Service) statusTargets() ([]netip.AddrPort, netip.AddrPort) {
	view := s.client.Snapshot()
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	var current netip.AddrPort
	if connectionStatus(view.ConnectionStatus) == "connected" && view.SnapshotFresh {
		current, _ = clientapp.ParseServerEndpoint(s.serverAddress)
	}
	addresses := make([]netip.AddrPort, 0, len(s.recentServers))
	seen := make(map[netip.AddrPort]bool)
	for _, server := range s.recentServers {
		address, err := clientapp.ParseServerEndpoint(server.Address)
		if err == nil && !seen[address] {
			addresses = append(addresses, address)
			seen[address] = true
		}
	}
	return addresses, current
}

// StartServerStatus owns the monitor lifetime independently of connection state.
func StartServerStatus(ctx context.Context, app *application.App, s *Service) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.serverStatus.Run(ctx, s.statusTargets, func() { app.Event.Emit("server-status-changed", true) })
	}()
	return func() { cancel(); <-done }
}
