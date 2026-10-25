package audio

import (
	"testing"

	pionopus "github.com/pion/opus"
	"uniclog.io/sonoryx/internal/domain"
	"uniclog.io/sonoryx/internal/protocol"
)

func TestOpusHighBitrateOutputFitsVoiceV2(t *testing.T) {
	config := CodecConfig{SampleRate: 48000, Channels: 1, SamplesPerFrame: 960, Bitrate: 472000, Application: domain.OpusApplicationAudio}
	encoder, err := NewOpusEncoder(config)
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := NewOpusDecoder(config)
	if err != nil {
		t.Fatal(err)
	}
	var noise uint32 = 42
	for range 8 {
		samples := make([]int16, 960)
		for i := range samples {
			noise ^= noise << 13
			noise ^= noise >> 17
			noise ^= noise << 5
			samples[i] = int16(noise)
		}
		packet, err := encoder.Encode(samples)
		if err != nil {
			t.Fatal(err)
		}
		if len(packet) == 0 || len(packet) > protocol.MaxVoiceFrameSize {
			t.Fatalf("invalid encoded size: %d", len(packet))
		}
		if _, err := decoder.Decode(packet); err != nil {
			t.Fatal("bounded output is not a valid Opus packet", err)
		}
	}
}

func TestNewOpusEncoderAcceptsConfiguredBitrate(t *testing.T) {
	_, err := NewOpusEncoder(CodecConfig{
		SampleRate:      48_000,
		Channels:        1,
		SamplesPerFrame: 960,
		Bitrate:         48_000,
	})
	if err != nil {
		t.Fatalf("create encoder with configured bitrate: %v", err)
	}
}

func TestNewOpusEncoderRejectsInvalidConfiguredBitrate(t *testing.T) {
	_, err := NewOpusEncoder(CodecConfig{
		SampleRate:      48_000,
		Channels:        1,
		SamplesPerFrame: 960,
		Bitrate:         5_999,
	})
	if err == nil {
		t.Fatal("expected invalid bitrate error")
	}
}

func TestNewOpusEncoderAllowsDefaultBitrate(t *testing.T) {
	_, err := NewOpusEncoder(CodecConfig{
		SampleRate:      48_000,
		Channels:        1,
		SamplesPerFrame: 960,
	})
	if err != nil {
		t.Fatalf("create encoder with library default bitrate: %v", err)
	}
}

func TestOpusApplication(t *testing.T) {
	tests := []struct {
		name string
		in   domain.OpusApplication
		want pionopus.Application
	}{
		{name: "legacy default", want: pionopus.ApplicationAudio},
		{name: "audio", in: domain.OpusApplicationAudio, want: pionopus.ApplicationAudio},
		{name: "voip", in: domain.OpusApplicationVoIP, want: pionopus.ApplicationVoIP},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := opusApplication(test.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("application = %d, want %d", got, test.want)
			}
		})
	}
}

func TestOpusApplicationRejectsUnsupportedValue(t *testing.T) {
	if _, err := opusApplication(domain.OpusApplication(255)); err == nil {
		t.Fatal("expected unsupported application error")
	}
}

func TestOpusEncoderConfigureChannelProfile(t *testing.T) {
	encoder, err := NewOpusEncoder(CodecConfig{SampleRate: 48_000, Channels: 1, SamplesPerFrame: 960})
	if err != nil {
		t.Fatal(err)
	}
	profile := domain.DefaultAudioProfile()
	profile.Bitrate = 32_000
	profile.Application = domain.OpusApplicationVoIP
	if err := encoder.Configure(profile); err != nil {
		t.Fatalf("configure encoder: %v", err)
	}
}
