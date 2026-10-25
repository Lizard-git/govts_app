package protocol

import "testing"

func TestAudioStatePayloadRoundTrip(t *testing.T) {
	for _, want := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
		payload, err := EncodeAudioState(want[0], want[1], 17)
		if err != nil {
			t.Fatal(err)
		}
		muted, deafened, expected, err := DecodeAudioState(payload)
		if err != nil || muted != want[0] || deafened != want[1] || expected != 17 {
			t.Fatalf("round trip = (%t, %t, %v), want %v", muted, deafened, err, want)
		}
	}
	for _, payload := range [][]byte{nil, {}, {0, 0}, {4}} {
		if _, _, _, err := DecodeAudioState(payload); err == nil {
			t.Fatalf("accepted malformed audio state %x", payload)
		}
	}
}
