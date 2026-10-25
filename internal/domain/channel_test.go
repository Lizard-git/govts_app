package domain

import "testing"

func TestAudioProfileFitsVoiceV2FrameCeiling(t *testing.T) {
	profile := DefaultAudioProfile()
	for _, bitrate := range []uint32{6000, 472000} {
		profile.Bitrate = bitrate
		if err := ValidateAudioProfile(profile); err != nil {
			t.Fatal(err)
		}
	}
	for _, bitrate := range []uint32{5999, 472001, 510000} {
		profile.Bitrate = bitrate
		if err := ValidateAudioProfile(profile); err == nil {
			t.Fatalf("invalid bitrate accepted: %d", bitrate)
		}
	}
}
