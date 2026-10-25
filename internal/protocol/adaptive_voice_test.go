package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func adaptiveStateHeader() VoiceStatePage {
	return VoiceStatePage{Flags: VoiceStateFull, Revision: 7, OwnControlRevision: 2,
		OwnSourceEpoch: 3, OwnReceiverEpoch: 4, OwnChannelID: 5,
		AudioProfileRevision: 6, EnabledDirections: VoiceDirectionUp | VoiceDirectionDown, PageCount: 1}
}

func adaptiveSources(n int) []VoiceSourceState {
	sources := make([]VoiceSourceState, n)
	for i := range sources {
		sources[i] = VoiceSourceState{SenderID: uint64(i + 1), SourceEpoch: 1, ChannelID: 5}
	}
	return sources
}

func TestAdaptiveVoicePacketIDs(t *testing.T) {
	if PacketVoice != 2 || PacketVoiceBundle != 31 || PacketAdminAck != 39 || PacketVoiceBundleV2 != 40 || PacketVoiceStateRequest != 45 {
		t.Fatal("existing packet IDs changed or adaptive types were not appended")
	}
}

func TestVoiceBundleV2RoundTripAndWireLimit(t *testing.T) {
	for count := 1; count <= 3; count++ {
		bundle := VoiceBundleV2{DatagramSequence: 19, SourceEpoch: 2}
		for i := 0; i < count; i++ {
			bundle.Frames = append(bundle.Frames, VoiceBundleFrame{Sequence: uint32(i + 1), Payload: bytes.Repeat([]byte{byte(i)}, 60)})
		}
		packet, err := NewVoiceBundleV2Packet(8, bundle, true)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeVoiceBundleV2Packet(packet, true)
		if err != nil || !reflect.DeepEqual(got, bundle) {
			t.Fatalf("%d frames: got %+v, %v", count, got, err)
		}
	}
	bundle := VoiceBundleV2{SourceEpoch: 1, Frames: []VoiceBundleFrame{{Sequence: 1, Payload: make([]byte, MaxVoiceFrameSize)}}}
	payload, err := EncodeVoiceBundleV2(bundle)
	if err != nil || len(payload) != MaxPayloadSize {
		t.Fatalf("maximum single frame: len=%d err=%v", len(payload), err)
	}
	bundle.Frames[0].Payload = make([]byte, MaxVoiceFrameSize+1)
	if _, err := EncodeVoiceBundleV2(bundle); err == nil {
		t.Fatal("accepted oversized Opus frame")
	}
	bundle.Frames = []VoiceBundleFrame{{Sequence: 1, Payload: make([]byte, 585)}, {Sequence: 2, Payload: make([]byte, 589)}}
	if payload, err := EncodeVoiceBundleV2(bundle); err != nil || len(payload) != MaxPayloadSize {
		t.Fatalf("exactly full two-frame bundle: len=%d err=%v", len(payload), err)
	}
	bundle.Frames[1].Payload = make([]byte, 590)
	if _, err := EncodeVoiceBundleV2(bundle); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversized bundle: %v", err)
	}
}

func TestVoiceBundleV2RejectsMalformedAndWrongDirection(t *testing.T) {
	bundle := VoiceBundleV2{DatagramSequence: 0, SourceEpoch: 1, Frames: []VoiceBundleFrame{
		{Sequence: math.MaxUint32, Payload: []byte{1}}, {Sequence: 0, Payload: []byte{2}}, {Sequence: 1, Payload: []byte{3}},
	}}
	packet, err := NewVoiceBundleV2Packet(9, bundle, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeVoiceBundleV2Packet(packet, true); err != nil {
		t.Fatalf("wraparound rejected: %v", err)
	}
	for n := 0; n < len(packet.Payload); n++ {
		if _, err := DecodeVoiceBundleV2(packet.Payload[:n]); err == nil {
			t.Fatalf("accepted truncation at %d", n)
		}
	}
	for name, mutate := range map[string]func([]byte){
		"version": func(p []byte) { p[0] = 1 }, "zero count": func(p []byte) { p[1] = 0 },
		"four frames": func(p []byte) { p[1] = 4 }, "zero epoch": func(p []byte) { clear(p[6:10]) },
		"zero frame length": func(p []byte) { clear(p[18:20]) },
		"span":              func(p []byte) { binary.BigEndian.PutUint32(p[len(p)-7:len(p)-3], 64) },
	} {
		t.Run(name, func(t *testing.T) {
			payload := bytes.Clone(packet.Payload)
			mutate(payload)
			if _, err := DecodeVoiceBundleV2(payload); err == nil {
				t.Fatal("accepted malformed bundle")
			}
		})
	}
	if _, err := DecodeVoiceBundleV2(append(bytes.Clone(packet.Payload), 0)); err == nil {
		t.Fatal("accepted trailing byte")
	}
	for _, mutate := range []func(*VoicePacket){
		func(p *VoicePacket) { p.RequestID = 1 }, func(p *VoicePacket) { p.SessionID = 0 },
		func(p *VoicePacket) { p.Sequence++ }, func(p *VoicePacket) { p.Type = PacketVoiceBundle },
	} {
		bad := packet
		mutate(&bad)
		if _, err := DecodeVoiceBundleV2Packet(bad, true); err == nil {
			t.Fatal("accepted invalid outer header")
		}
	}
	if _, err := DecodeVoiceBundleV2Packet(packet, false); err == nil {
		t.Fatal("accepted upstream epoch downstream")
	}
	bundle.ReceiverEpoch = 2
	if _, err := NewVoiceBundleV2Packet(9, bundle, true); err == nil {
		t.Fatal("accepted downstream epoch upstream")
	}
}

func TestVoiceStatePaginationAndAtomicAssembly(t *testing.T) {
	for _, count := range []int{0, 1, 17, MaxVoiceSources} {
		sources := adaptiveSources(count)
		pages, err := PaginateVoiceState(adaptiveStateHeader(), sources)
		if err != nil {
			t.Fatal(err)
		}
		var a VoiceStateAssembler
		now := time.Unix(100, 0)
		for i := len(pages) - 1; i >= 0; i-- {
			payload, err := EncodeVoiceState(pages[i])
			if err != nil || len(payload) > 396 {
				t.Fatalf("page size %d, %v", len(payload), err)
			}
			decoded, err := DecodeVoiceState(payload)
			if err != nil {
				t.Fatal(err)
			}
			got, reack, err := a.Add(decoded, 0, now)
			if err != nil || reack || (got != nil) != (i == 0) {
				t.Fatalf("page %d/%d: snapshot=%v reack=%v err=%v", i, len(pages), got != nil, reack, err)
			}
			if got != nil && !reflect.DeepEqual(got.Sources, sources) {
				t.Fatal("source ordering/content changed")
			}
		}
		a.Reset()
		if got, reack, err := a.Add(pages[0], pages[0].Revision, now); err != nil || got != nil || !reack {
			t.Fatalf("already installed revision: %+v %v %v", got, reack, err)
		}
	}
	if _, err := PaginateVoiceState(adaptiveStateHeader(), adaptiveSources(MaxVoiceSources+1)); err == nil {
		t.Fatal("accepted oversized snapshot")
	}
}

func TestVoiceStateAssemblyLossConflictsAndDelta(t *testing.T) {
	pages, err := PaginateVoiceState(adaptiveStateHeader(), adaptiveSources(17))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	var a VoiceStateAssembler
	if _, _, err := a.Add(pages[0], 0, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Add(pages[0], 0, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if !a.Expire(now.Add(3*time.Second)) || a.Expire(now.Add(4*time.Second)) {
		t.Fatal("duplicates extended timeout or expired assembly was retained")
	}
	for _, mutate := range []func(*VoiceStatePage){
		func(p *VoiceStatePage) { p.DownstreamBarrier++ },
		func(p *VoiceStatePage) {
			p.Sources = append([]VoiceSourceState(nil), p.Sources...)
			p.Sources[0].SourceEpoch++
		},
	} {
		_, _, _ = a.Add(pages[0], 0, now)
		bad := pages[0]
		mutate(&bad)
		if _, _, err := a.Add(bad, 0, now); err == nil {
			t.Fatal("accepted contradictory page")
		}
	}
	_, _, _ = a.Add(pages[0], 0, now)
	bad := pages[1]
	bad.Sources = append([]VoiceSourceState(nil), bad.Sources...)
	bad.Sources[0].SenderID = pages[0].Sources[0].SenderID
	if _, _, err := a.Add(bad, 0, now); err == nil {
		t.Fatal("accepted duplicate source between pages")
	}
	_, _, _ = a.Add(pages[0], 0, now)
	old := pages[0]
	old.Flags, old.Revision, old.BaseRevision = 0, 6, 4
	if _, _, err := a.Add(old, 5, now); err != nil {
		t.Fatal("stale delta interrupted newer assembly")
	}
	if got, _, err := a.Add(pages[1], 5, now); err != nil || got == nil {
		t.Fatalf("newer assembly lost: %v", err)
	}
	a.Reset()
	delta := pages[0]
	delta.Flags, delta.BaseRevision = 0, 5
	if _, _, err := a.Add(delta, 4, now); err == nil {
		t.Fatal("accepted mismatched delta base")
	}
	if _, _, err := a.Add(delta, 5, now); err != nil {
		t.Fatal(err)
	}
}

func TestVoiceStateRejectsTruncationAndInvalidFlags(t *testing.T) {
	page := adaptiveStateHeader()
	page.Flags |= VoiceStateOwnMuted | VoiceStateOwnDeafened
	page.Sources = adaptiveSources(1)
	payload, err := EncodeVoiceState(page)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, err := DecodeVoiceState(payload); err != nil || decoded.Flags != page.Flags {
		t.Fatalf("own mute/deafen flags lost: %+v %v", decoded, err)
	}
	for n := 0; n < len(payload); n++ {
		if _, err := DecodeVoiceState(payload[:n]); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	for _, offset := range []int{0, 1, 54, 80} {
		bad := bytes.Clone(payload)
		bad[offset] = 0xff
		if _, err := DecodeVoiceState(bad); err == nil {
			t.Fatalf("accepted invalid version/flags at %d", offset)
		}
	}
	if _, err := DecodeVoiceState(append(payload, 0)); err == nil {
		t.Fatal("accepted trailing byte")
	}
	if _, err := EncodeVoiceStateAck(0); err == nil {
		t.Fatal("accepted zero ack revision")
	}
	if _, err := DecodeVoiceStateAck(EncodeVoiceStateRequest(0)); err == nil {
		t.Fatal("accepted initial request as ack")
	}
	for _, rev := range []uint64{1, math.MaxUint64} {
		ack, err := EncodeVoiceStateAck(rev)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := DecodeVoiceStateAck(ack); err != nil || got != rev {
			t.Fatalf("ack round trip: %d %v", got, err)
		}
	}
}

func TestVoiceTelemetryRoundTripAndMalformed(t *testing.T) {
	summary := VoiceSummary{Direction: VoiceUpstream, Mode: VoiceMode{DesiredFrames: 3, AllowedFrames: 2, Reason: VoiceRestrictionBudget}, Intervals: []VoiceSendInterval{
		{ID: math.MaxUint32, FirstSequence: math.MaxUint32, LastSequence: 0, SuccessfulSends: 1, LocalSendFailures: 1, ActiveMS: 20},
		{ID: 0}, {ID: 1, FirstSequence: 1, LastSequence: 1, SuccessfulSends: 1, ActiveMS: 20},
	}}
	encoded, err := EncodeVoiceSummary(summary)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeVoiceSummary(encoded); err != nil || !reflect.DeepEqual(got, summary) {
		t.Fatalf("summary round trip: %+v %v", got, err)
	}
	report := VoiceReport{Direction: VoiceDownstream, RTT: VoiceRTTSample{ID: 8, Micros: 12345, AgeMS: 10000, Valid: true}, Intervals: []VoiceReceiveInterval{
		{ID: 9, FirstSequence: math.MaxUint32, LastSequence: 1, ReceivedUnique: 1, NewMultipleBursts: 1,
			LongestBurst: 2, RestoredUnique: 2, LateFrames: 1, PLCFrames: 1, Flags: VoiceIntervalComplete | VoiceBurstValid},
	}}
	reportBytes, err := EncodeVoiceReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeVoiceReport(reportBytes); err != nil || !reflect.DeepEqual(got, report) {
		t.Fatalf("report round trip: %+v %v", got, err)
	}
	for _, item := range []struct {
		data   []byte
		decode func([]byte) error
	}{
		{encoded, func(p []byte) error { _, err := DecodeVoiceSummary(p); return err }},
		{reportBytes, func(p []byte) error { _, err := DecodeVoiceReport(p); return err }},
	} {
		for n := 0; n < len(item.data); n++ {
			if err := item.decode(item.data[:n]); err == nil {
				t.Fatalf("accepted truncation %d", n)
			}
		}
		if err := item.decode(append(bytes.Clone(item.data), 0)); err == nil {
			t.Fatal("accepted telemetry trailing byte")
		}
	}
	for name, mutate := range map[string]func(*VoiceSummary){
		"direction": func(s *VoiceSummary) { s.Direction = 2 }, "mode": func(s *VoiceSummary) { s.Mode.AllowedFrames = 4 },
		"gap": func(s *VoiceSummary) { s.Intervals[1].ID = 2 }, "count": func(s *VoiceSummary) { s.Intervals[0].SuccessfulSends++ },
		"activity": func(s *VoiceSummary) { s.Intervals[0].ActiveMS = 1001 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := summary
			bad.Intervals = append([]VoiceSendInterval(nil), summary.Intervals...)
			mutate(&bad)
			if _, err := EncodeVoiceSummary(bad); err == nil {
				t.Fatal("accepted invalid summary")
			}
		})
	}
	badFlags := bytes.Clone(reportBytes)
	badFlags[39] = 0x80
	if _, err := DecodeVoiceReport(badFlags); err == nil {
		t.Fatal("accepted reserved report flags")
	}
	report.RTT.AgeMS++
	if _, err := EncodeVoiceReport(report); err == nil {
		t.Fatal("accepted stale RTT probe")
	}
}

func TestSecureVoiceBundleV2RecipientAndSender(t *testing.T) {
	server, client := NewSecureDatagramCodec(true), NewSecureDatagramCodec(false)
	var c2s, s2c [32]byte
	c2s[0], s2c[0] = 1, 2
	for _, codec := range []*SecureDatagramCodec{server, client} {
		if err := codec.Install(9, c2s, s2c); err != nil {
			t.Fatal(err)
		}
	}
	bundle := VoiceBundleV2{SourceEpoch: 1, ReceiverEpoch: 1, Frames: []VoiceBundleFrame{{Sequence: 1, Payload: make([]byte, MaxVoiceFrameSize)}}}
	packet, err := NewVoiceBundleV2Packet(8, bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := server.Encode(DatagramContext{KeyOwnerID: 9}, packet)
	if err != nil || len(wire) != MaxWireDatagramSize {
		t.Fatalf("protected maximum packet: len=%d err=%v", len(wire), err)
	}
	if got, err := client.Decode(DatagramContext{KeyOwnerID: 9}, wire); err != nil || got.SessionID != 8 {
		t.Fatalf("relay: %+v %v", got, err)
	}
	if _, err := client.Decode(DatagramContext{KeyOwnerID: 9}, wire); !errors.Is(err, ErrRejectedDatagram) {
		t.Fatalf("replay accepted: %v", err)
	}
	spoofed, err := client.Encode(DatagramContext{KeyOwnerID: 9}, packet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Decode(DatagramContext{KeyOwnerID: 9}, spoofed); !errors.Is(err, ErrRejectedDatagram) {
		t.Fatalf("upstream sender spoof accepted: %v", err)
	}
}

func FuzzAdaptiveVoiceDecoders(f *testing.F) {
	f.Add([]byte{})
	if p, err := EncodeVoiceState(adaptiveStateHeader()); err == nil {
		f.Add(p)
	}
	if p, err := EncodeVoiceBundleV2(VoiceBundleV2{SourceEpoch: 1, Frames: []VoiceBundleFrame{{Sequence: 1, Payload: []byte{1}}}}); err == nil {
		f.Add(p)
	}
	f.Fuzz(func(t *testing.T, p []byte) {
		if len(p) > MaxWireDatagramSize+1 {
			return
		}
		_, _ = DecodeVoiceBundleV2(p)
		_, _ = DecodeVoiceState(p)
		_, _ = DecodeVoiceStateAck(p)
		_, _ = DecodeVoiceStateRequest(p)
		_, _ = DecodeVoiceSummary(p)
		_, _ = DecodeVoiceReport(p)
	})
}
