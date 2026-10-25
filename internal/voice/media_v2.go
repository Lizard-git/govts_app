package voice

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"time"

	"uniclog.io/sonoryx/internal/protocol"
	"uniclog.io/sonoryx/internal/transport/udp"
	"uniclog.io/sonoryx/internal/voiceflow"
)

func handleVoiceV2(conn *udp.ServerPacketConn, h *Hub, packet protocol.VoicePacket, addr *net.UDPAddr) error {
	if err := ValidateSessionAddr(h, packet.SessionID, addr); err != nil {
		return err
	}
	b, err := protocol.DecodeVoiceBundleV2Packet(packet, true)
	if err != nil {
		return err
	}
	// Transport reception is recorded before mute/epoch policy decisions.
	h.RecordVoicePacket(packet.SessionID, b.DatagramSequence)
	h.mu.Lock()
	sender := h.sessions[packet.SessionID]
	if sender == nil {
		h.mu.Unlock()
		return ErrSessionNotFound
	}
	if sender.Muted || sender.ChannelID == 0 || sender.SourceEpoch != b.SourceEpoch {
		h.mu.Unlock()
		return nil
	}
	sender.LastSeen = time.Now()
	if sender.voiceFrames == nil {
		sender.voiceFrames = &voiceflow.Window{}
	}
	now := time.Now()
	added, err := sender.voiceFrames.Accept(b.Frames, now)
	if err != nil {
		h.mu.Unlock()
		return fmt.Errorf("%w: %v", protocol.ErrInvalidVoiceBundle, err)
	}
	var groups [][]protocol.VoiceBundleFrame
	covered := make(map[uint32]bool)
	count := 1
	if h.voiceBundles {
		count = 2
	}
	for i := len(added) - 1; i >= 0; i-- {
		if covered[added[i].Sequence] {
			continue
		}
		frames := []protocol.VoiceBundleFrame{added[i]}
		// Newly restored candidates take the available slots before routine copies.
		for j := i - 1; j >= 0 && len(frames) < count; j-- {
			candidate := append([]protocol.VoiceBundleFrame{added[j]}, frames...)
			if protocol.VoiceBundleV2Size(candidate) <= protocol.MaxPayloadSize {
				frames = candidate
			}
		}
		for _, f := range sender.voiceFrames.ForFrame(added[i], count, now) {
			if len(frames) >= count {
				break
			}
			exists := false
			for _, old := range frames {
				if old.Sequence == f.Sequence {
					exists = true
					break
				}
			}
			if exists {
				continue
			}
			candidate := append(append([]protocol.VoiceBundleFrame(nil), frames...), f)
			if protocol.VoiceBundleV2Size(candidate) <= protocol.MaxPayloadSize {
				frames = candidate
			}
		}
		sort.Slice(frames, func(i, j int) bool { return int32(frames[i].Sequence-frames[j].Sequence) < 0 })
		if len(groups) > 0 {
			frames = []protocol.VoiceBundleFrame{added[i]}
		} // repair without redundant bytes
		for _, frame := range frames {
			covered[frame.Sequence] = true
		}
		groups = append(groups, frames)
	}
	var recipients []uint64
	for id, s := range h.sessions {
		if id != sender.ID && s.ChannelID == sender.ChannelID && !s.Deafened && s.voiceControl != nil {
			recipients = append(recipients, id)
		}
	}
	h.mu.Unlock()
	var failures []error
	for _, id := range recipients {
		for _, frames := range groups {
			if err := h.writeVoiceV2(conn, id, packet.SessionID, b.SourceEpoch, frames); err != nil {
				failures = append(failures, err)
				break
			}
		}
	}
	return errors.Join(failures...)
}

func (h *Hub) writeVoiceV2(conn *udp.ServerPacketConn, id, senderID uint64, epoch uint32, frames []protocol.VoiceBundleFrame) error {
	h.mu.RLock()
	recipient := h.sessions[id]
	var control *voiceControlSession
	if recipient != nil {
		control = recipient.voiceControl
	}
	h.mu.RUnlock()
	if control == nil {
		return nil
	}
	control.writeMu.Lock()
	defer control.writeMu.Unlock()
	h.mu.Lock()
	recipient, sender := h.sessions[id], h.sessions[senderID]
	if recipient == nil || recipient.voiceControl != control || sender == nil || sender.Muted || recipient.Deafened || sender.SourceEpoch != epoch || sender.ChannelID == 0 || sender.ChannelID != recipient.ChannelID || control.ackReceiverEpoch != recipient.ReceiverEpoch || control.ackSources[senderID] != epoch {
		h.mu.Unlock()
		return nil
	}
	control.lastDatagram++
	packet, err := protocol.NewVoiceBundleV2Packet(senderID, protocol.VoiceBundleV2{DatagramSequence: control.lastDatagram, SourceEpoch: epoch, ReceiverEpoch: recipient.ReceiverEpoch, Frames: frames}, false)
	dest := *cloneSession(recipient)
	h.mu.Unlock()
	if err != nil {
		return err
	}
	return SendToSession(conn, dest, packet)
}
