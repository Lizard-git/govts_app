package media

import (
	"context"
	"errors"
	"strings"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/nack"
	"github.com/pion/webrtc/v4"
	"uniclog.io/sonoryx/internal/domain"
	"uniclog.io/sonoryx/internal/mediasignal"
	"uniclog.io/sonoryx/internal/voice"
)

func NewManager(hub *voice.Hub) (*Manager, error) { return NewManagerWithConfig(hub, Config{}) }

func NewManagerWithConfig(hub *voice.Hub, config Config) (*Manager, error) {
	if hub == nil {
		return nil, errors.New("hub is required")
	}
	mediaEngine := &webrtc.MediaEngine{}
	for _, codec := range screenCodecs() {
		if err := mediaEngine.RegisterCodec(codec, webrtc.RTPCodecTypeVideo); err != nil {
			return nil, err
		}
	}
	registry := &interceptor.Registry{}
	meters := &transportMeters{}
	registry.Add(meters)
	if err := webrtc.RegisterDefaultInterceptorsWithOptions(mediaEngine, registry, webrtc.WithNackResponderOptions(nack.ResponderSize(nackResponderCache))); err != nil {
		return nil, err
	}
	settingEngine := webrtc.SettingEngine{}
	if config.MinUDPPort != 0 || config.MaxUDPPort != 0 {
		if config.MinUDPPort == 0 || config.MaxUDPPort < config.MinUDPPort {
			return nil, errors.New("invalid media UDP port range")
		}
		if err := settingEngine.SetEphemeralUDPPortRange(config.MinUDPPort, config.MaxUDPPort); err != nil {
			return nil, err
		}
	}
	if config.AdvertisedIP != "" {
		settingEngine.SetNAT1To1IPs([]string{config.AdvertisedIP}, webrtc.ICECandidateTypeHost)
	}
	m := &Manager{hub: hub, meters: meters, api: webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithInterceptorRegistry(registry), webrtc.WithSettingEngine(settingEngine)), publishers: make(map[domain.StreamID]*publisher), ownerStreams: make(map[uint64]domain.StreamID), done: make(chan struct{})}
	m.startWorker(m.reconcileLoop)
	return m, nil
}

func acceptOffer(ctx context.Context, pc *webrtc.PeerConnection, offer mediasignal.SessionDescription) (mediasignal.SessionDescription, error) {
	if offer.Type != "offer" || offer.SDP == "" {
		return mediasignal.SessionDescription{}, errors.New("invalid WebRTC offer")
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer.SDP}); err != nil {
		return mediasignal.SessionDescription{}, err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return mediasignal.SessionDescription{}, err
	}
	gathering := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return mediasignal.SessionDescription{}, err
	}
	select {
	case <-ctx.Done():
		return mediasignal.SessionDescription{}, ctx.Err()
	case <-gathering:
	}
	local := pc.LocalDescription()
	if local == nil {
		return mediasignal.SessionDescription{}, errors.New("missing local description")
	}
	if strings.Contains(local.SDP, "m=video 0 ") || !strings.Contains(local.SDP, "m=video ") {
		return mediasignal.SessionDescription{}, errors.New("no compatible video codec/profile in the offer")
	}
	return mediasignal.SessionDescription{Type: "answer", SDP: local.SDP}, nil
}
