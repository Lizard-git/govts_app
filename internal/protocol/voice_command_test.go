package protocol

import (
	"bytes"
	"reflect"
	"testing"
)

func TestVoiceCommandAckRoundTripAndRejectsMalformed(t *testing.T) {
	a := VoiceControlAck{OwnControlRevision: 4, VoiceRevision: 8, DomainRevision: 9, ChannelID: 2, SourceEpoch: 3, ReceiverEpoch: 4, Muted: true, Deafened: true}
	for _, status := range []byte{VoiceControlOK, VoiceControlStale} {
		a.Status = status
		p, err := EncodeVoiceControlAck(a)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := DecodeVoiceControlAck(p); err != nil || !reflect.DeepEqual(got, a) {
			t.Fatalf("ack roundtrip: %+v %v", got, err)
		}
		for n := 0; n < len(p); n++ {
			if _, err := DecodeVoiceControlAck(p[:n]); err == nil {
				t.Fatalf("truncation %d accepted", n)
			}
		}
		for _, offset := range []int{0, 1, 42} {
			bad := bytes.Clone(p)
			bad[offset] = 255
			if _, err := DecodeVoiceControlAck(bad); err == nil {
				t.Fatal("invalid version/status/flags")
			}
		}
		if _, err := DecodeVoiceControlAck(append(p, 0)); err == nil {
			t.Fatal("trailing byte accepted")
		}
	}
	if _, _, err := DecodeJoinChannelRequest(make([]byte, 8)); err == nil {
		t.Fatal("old join format accepted")
	}
	if _, _, _, err := DecodeAudioState([]byte{0}); err == nil {
		t.Fatal("old audio format accepted")
	}
	if _, err := DecodeVoiceControlAck(make([]byte, 16)); err == nil {
		t.Fatal("old acknowledgement accepted")
	}
	if _, err := EncodeJoinChannelRequest(1, 0); err == nil {
		t.Fatal("zero precondition accepted")
	}
	if _, err := EncodeAudioState(false, false, 0); err == nil {
		t.Fatal("zero audio precondition accepted")
	}
}
