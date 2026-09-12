package client

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"example.com/go-voice-mvp/internal/domain"
	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

const (
	MaxSnapshotChannels     = 4096
	MaxSnapshotParticipants = 65535
	snapshotSyncAttempts    = 3
	snapshotRequestTimeout  = 3 * time.Second
)

var errSnapshotRevisionChanged = errors.New("snapshot revision changed")

func LoadServerSnapshot(ctx context.Context, conn *udp.ClientPacketConn, state *State) (domain.ServerSnapshot, error) {
	generation := state.Generation()
	for attempt := 1; attempt <= snapshotSyncAttempts; attempt++ {
		snapshot, err := loadServerSnapshotOnce(ctx, conn, state)
		if err == nil {
			if !state.ReplaceSnapshotForGeneration(generation, snapshot) {
				return domain.ServerSnapshot{}, errors.New("client session changed during snapshot load")
			}
			return snapshot, nil
		}
		if !errors.Is(err, errSnapshotRevisionChanged) {
			return domain.ServerSnapshot{}, err
		}
	}
	return domain.ServerSnapshot{}, fmt.Errorf("server state changed during %d snapshot attempts", snapshotSyncAttempts)
}

func loadServerSnapshotOnce(ctx context.Context, conn *udp.ClientPacketConn, state *State) (domain.ServerSnapshot, error) {
	metadata, err := requestSnapshot(ctx, conn, state, protocol.SnapshotRequest{Kind: protocol.SnapshotKindMetadata})
	if err != nil {
		return domain.ServerSnapshot{}, err
	}
	if metadata.Kind != protocol.SnapshotKindMetadata || metadata.Status != protocol.SnapshotStatusOK {
		return domain.ServerSnapshot{}, errors.New("metadata response is not OK")
	}
	if metadata.ChannelCount > MaxSnapshotChannels || metadata.ParticipantCount > MaxSnapshotParticipants {
		return domain.ServerSnapshot{}, fmt.Errorf("snapshot totals exceed client limits: channels=%d participants=%d", metadata.ChannelCount, metadata.ParticipantCount)
	}
	snapshot := domain.ServerSnapshot{Revision: metadata.Revision, Info: metadata.ServerInfo, Channels: make([]domain.Channel, 0, int(metadata.ChannelCount)), Participants: make([]domain.Participant, 0, int(metadata.ParticipantCount))}
	channels, err := loadChannelPages(ctx, conn, state, metadata.Revision, metadata.ChannelCount)
	if err != nil {
		return domain.ServerSnapshot{}, err
	}
	snapshot.Channels = channels
	participants, err := loadParticipantPages(ctx, conn, state, metadata.Revision, metadata.ParticipantCount)
	if err != nil {
		return domain.ServerSnapshot{}, err
	}
	snapshot.Participants = participants
	if err := validateServerSnapshot(snapshot); err != nil {
		return domain.ServerSnapshot{}, fmt.Errorf("validate server snapshot: %w", err)
	}
	return snapshot, nil
}
func requestSnapshot(ctx context.Context, conn *udp.ClientPacketConn, state *State, r protocol.SnapshotRequest) (protocol.SnapshotResponse, error) {
	p, err := protocol.EncodeSnapshotRequest(r)
	if err != nil {
		return protocol.SnapshotResponse{}, err
	}
	response, err := DoRequest(ctx, conn, state, protocol.VoicePacket{Type: protocol.PacketStateSnapshotRequest, Payload: p}, snapshotRequestTimeout)
	if err != nil {
		return protocol.SnapshotResponse{}, err
	}
	if response.Type != protocol.PacketStateSnapshotAck {
		return protocol.SnapshotResponse{}, fmt.Errorf("unexpected snapshot response type: %d", response.Type)
	}
	decoded, err := protocol.DecodeSnapshotResponse(response.Payload)
	if err != nil {
		return protocol.SnapshotResponse{}, fmt.Errorf("decode snapshot response: %w", err)
	}
	return decoded, nil
}
func loadChannelPages(ctx context.Context, conn *udp.ClientPacketConn, state *State, revision domain.StateRevision, total uint32) ([]domain.Channel, error) {
	items := make([]domain.Channel, 0, int(total))
	offset := uint32(0)
	for uint32(len(items)) < total {
		r, err := requestSnapshot(ctx, conn, state, protocol.SnapshotRequest{Kind: protocol.SnapshotKindChannels, ExpectedRevision: revision, Offset: offset, Limit: protocol.MaxSnapshotPageItems})
		if err != nil {
			return nil, err
		}
		if r.Status == protocol.SnapshotStatusRevisionChanged {
			return nil, errSnapshotRevisionChanged
		}
		if r.Kind != protocol.SnapshotKindChannels || r.Revision != revision {
			return nil, errors.New("inconsistent channel page")
		}
		next, err := validatePageProgress(offset, len(r.Channels), r.NextOffset, r.HasMore, uint32(len(items)), total)
		if err != nil {
			return nil, err
		}
		items = append(items, r.Channels...)
		offset = next
	}
	return items, nil
}
func loadParticipantPages(ctx context.Context, conn *udp.ClientPacketConn, state *State, revision domain.StateRevision, total uint32) ([]domain.Participant, error) {
	items := make([]domain.Participant, 0, int(total))
	offset := uint32(0)
	for uint32(len(items)) < total {
		r, err := requestSnapshot(ctx, conn, state, protocol.SnapshotRequest{Kind: protocol.SnapshotKindParticipants, ExpectedRevision: revision, Offset: offset, Limit: protocol.MaxSnapshotPageItems})
		if err != nil {
			return nil, err
		}
		if r.Status == protocol.SnapshotStatusRevisionChanged {
			return nil, errSnapshotRevisionChanged
		}
		if r.Kind != protocol.SnapshotKindParticipants || r.Revision != revision {
			return nil, errors.New("inconsistent participant page")
		}
		next, err := validatePageProgress(offset, len(r.Participants), r.NextOffset, r.HasMore, uint32(len(items)), total)
		if err != nil {
			return nil, err
		}
		items = append(items, r.Participants...)
		offset = next
	}
	return items, nil
}
func validatePageProgress(offset uint32, count int, next uint32, more bool, accumulated, total uint32) (uint32, error) {
	if count == 0 {
		return 0, errors.New("snapshot page made no progress")
	}
	if uint64(accumulated)+uint64(count) > uint64(total) {
		return 0, errors.New("snapshot page exceeds advertised total")
	}
	expected := offset + uint32(count)
	if more {
		if next != expected || next <= offset {
			return 0, errors.New("invalid next snapshot offset")
		}
	} else {
		if next != 0 || expected != total {
			return 0, errors.New("snapshot ended before advertised total")
		}
	}
	return expected, nil
}

func ResolveChannel(snapshot domain.ServerSnapshot, selector string) (domain.ChannelID, error) {
	if id, err := strconv.ParseUint(selector, 10, 64); err == nil && id != 0 {
		for _, c := range snapshot.Channels {
			if uint64(c.ID) == id {
				return c.ID, nil
			}
		}
		return 0, fmt.Errorf("channel %d not found", id)
	}
	var found domain.ChannelID
	for _, c := range snapshot.Channels {
		if strings.EqualFold(c.Name, selector) {
			if found != 0 {
				return 0, fmt.Errorf("channel name %q is ambiguous; use channel ID", selector)
			}
			found = c.ID
		}
	}
	if found == 0 {
		return 0, fmt.Errorf("channel %q not found", selector)
	}
	return found, nil
}

func validateServerSnapshot(s domain.ServerSnapshot) error {
	if s.Revision == 0 {
		return errors.New("zero revision")
	}
	if !utf8.ValidString(s.Info.Name) || s.Info.Name == "" || len(s.Info.Name) > domain.MaxServerNameBytes || strings.TrimSpace(s.Info.Name) != s.Info.Name {
		return errors.New("invalid server name")
	}
	ids := make(map[domain.ChannelID]domain.Channel, len(s.Channels))
	for i, c := range s.Channels {
		if c.ID == 0 {
			return errors.New("zero channel ID")
		}
		if _, ok := ids[c.ID]; ok {
			return fmt.Errorf("duplicate channel ID %d", c.ID)
		}
		if !validSnapshotText(c.Name, domain.MaxChannelNameBytes, true) || !validSnapshotText(c.Topic, domain.MaxChannelTopicBytes, false) || !validSnapshotText(c.Description, domain.MaxChannelDescriptionBytes, false) || c.Type != domain.ChannelTypePermanent || c.Audio != domain.DefaultAudioProfile() {
			return fmt.Errorf("invalid channel %d", c.ID)
		}
		ids[c.ID] = c
		if i > 0 {
			p := s.Channels[i-1]
			if p.ParentID > c.ParentID || (p.ParentID == c.ParentID && (p.Position > c.Position || (p.Position == c.Position && p.ID >= c.ID))) {
				return errors.New("channels are not canonically sorted")
			}
		}
	}
	for _, c := range s.Channels {
		seen := map[domain.ChannelID]bool{}
		id := c.ID
		depth := 0
		for id != 0 {
			if seen[id] {
				return errors.New("channel hierarchy cycle")
			}
			seen[id] = true
			node, ok := ids[id]
			if !ok {
				return fmt.Errorf("missing channel parent %d", id)
			}
			depth++
			if depth > domain.MaxChannelDepth {
				return errors.New("channel hierarchy too deep")
			}
			id = node.ParentID
		}
	}
	participantIDs := make(map[uint64]bool, len(s.Participants))
	for i, p := range s.Participants {
		if p.SessionID == 0 || participantIDs[p.SessionID] || !validSnapshotText(p.DisplayName, domain.MaxParticipantNameBytes, true) {
			return fmt.Errorf("invalid participant %d", p.SessionID)
		}
		participantIDs[p.SessionID] = true
		if p.ChannelID != 0 {
			if _, ok := ids[p.ChannelID]; !ok {
				return fmt.Errorf("participant references missing channel %d", p.ChannelID)
			}
		}
		if i > 0 && s.Participants[i-1].SessionID >= p.SessionID {
			return errors.New("participants are not sorted")
		}
	}
	return nil
}
func validSnapshotText(v string, max int, required bool) bool {
	return utf8.ValidString(v) && len(v) <= max && (!required || v != "") && (!required || strings.TrimSpace(v) == v)
}
