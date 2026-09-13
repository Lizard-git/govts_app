package wailsui

import (
	"math"
	"testing"

	voiceclient "example.com/go-voice-mvp/internal/client"
	"example.com/go-voice-mvp/internal/domain"
)

func TestViewDTOKeepsUint64IdentifiersExact(t *testing.T) {
	view := voiceclient.ClientViewState{
		ConnectionStatus: voiceclient.ConnectionConnected,
		Revision:         domain.StateRevision(math.MaxUint64),
		SessionID:        math.MaxUint64,
		ChannelID:        domain.ChannelID(uint64(1) << 53),
		Channels: []domain.Channel{{
			ID:       domain.ChannelID(math.MaxUint64),
			ParentID: domain.ChannelID(uint64(1) << 53),
			Audio:    domain.DefaultAudioProfile(),
		}},
		Participants: []domain.Participant{{
			SessionID:   math.MaxUint64,
			DisplayName: "alice",
			ChannelID:   domain.ChannelID(math.MaxUint64),
		}},
		Speaking: map[uint64]bool{math.MaxUint64: true},
	}

	dto := viewDTO(view, "")
	if dto.Revision != "18446744073709551615" || dto.SessionID != "18446744073709551615" {
		t.Fatalf("top-level identifiers lost precision: %#v", dto)
	}
	if dto.ChannelID != "9007199254740992" || dto.Channels[0].ID != "18446744073709551615" {
		t.Fatalf("channel identifiers lost precision: %#v", dto.Channels[0])
	}
	if !dto.Participants[0].Speaking || !dto.Participants[0].Local {
		t.Fatalf("participant flags = %#v", dto.Participants[0])
	}
}

func TestViewDTOUsesNonNilArrays(t *testing.T) {
	dto := viewDTO(voiceclient.ClientViewState{}, "")
	if dto.Channels == nil || dto.Participants == nil {
		t.Fatalf("nil arrays in DTO: %#v", dto)
	}
}

func TestParseUint64RejectsMalformedAndZeroIdentifiers(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1.5", "18446744073709551616"} {
		if _, err := parseUint64(value, "channelId", false); err == nil {
			t.Fatalf("accepted channel ID %q", value)
		}
	}
	if got, err := parseUint64("18446744073709551615", "channelId", false); err != nil || got != math.MaxUint64 {
		t.Fatalf("max uint64 = %d, %v", got, err)
	}
}
