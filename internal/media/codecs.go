package media

import (
	"slices"
	"strconv"
	"strings"

	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

func codecParameters(line string) map[string]string {
	parameters := make(map[string]string)
	for _, field := range strings.Split(line, ";") {
		pair := strings.SplitN(strings.TrimSpace(field), "=", 2)
		if len(pair) == 2 {
			parameters[strings.ToLower(strings.TrimSpace(pair[0]))] = strings.ToLower(strings.TrimSpace(pair[1]))
		}
	}
	return parameters
}

func supportedScreenCodec(codec webrtc.RTPCodecCapability) bool {
	parameters := codecParameters(codec.SDPFmtpLine)
	switch strings.ToLower(codec.MimeType) {
	case "video/vp8":
		return true
	case "video/vp9":
		return parameters["profile-id"] == "" || parameters["profile-id"] == "0"
	case "video/av1":
		return parameters["profile"] == "" || parameters["profile"] == "0"
	case "video/h264":
		profile := parameters["profile-level-id"]
		if len(profile) != 6 || profile[:4] != "42e0" || parameters["packetization-mode"] != "1" {
			return false
		}
		level, err := strconv.ParseUint(profile[4:], 16, 8)
		return err == nil && level <= 52
	}
	return false
}

// Pion's fuzzy matching may fall back to MIME alone. An SFU cannot safely do
// that for a viewer: it forwards the publisher's actual profile without encoding.
func offerSupportsCodec(offer string, codec webrtc.RTPCodecCapability) bool {
	var description sdp.SessionDescription
	if description.Unmarshal([]byte(offer)) != nil {
		return false
	}
	wanted := codecParameters(codec.SDPFmtpLine)
	for _, media := range description.MediaDescriptions {
		if media.MediaName.Media != "video" || media.MediaName.Port.Value == 0 {
			continue
		}
		formats := make(map[string]string)
		for _, attribute := range media.Attributes {
			if attribute.Key == "fmtp" {
				parts := strings.SplitN(attribute.Value, " ", 2)
				if len(parts) == 2 {
					formats[parts[0]] = parts[1]
				}
			}
		}
		for _, attribute := range media.Attributes {
			if attribute.Key != "rtpmap" {
				continue
			}
			parts := strings.Fields(attribute.Value)
			if len(parts) != 2 || !slices.Contains(media.MediaName.Formats, parts[0]) {
				continue
			}
			encoding := strings.Split(parts[1], "/")
			if len(encoding) < 2 || !strings.EqualFold("video/"+encoding[0], codec.MimeType) || encoding[1] != "90000" {
				continue
			}
			peer := codecParameters(formats[parts[0]])
			switch strings.ToLower(codec.MimeType) {
			case "video/h264":
				a, b := wanted["profile-level-id"], peer["profile-level-id"]
				if len(a) != 6 || len(b) != 6 || a[:4] != b[:4] || wanted["packetization-mode"] != peer["packetization-mode"] {
					continue
				}
				level, err := strconv.ParseUint(a[4:], 16, 8)
				peerLevel, peerErr := strconv.ParseUint(b[4:], 16, 8)
				if err != nil || peerErr != nil || peerLevel < level {
					continue
				}
			case "video/vp9":
				a, b := wanted["profile-id"], peer["profile-id"]
				if a == "" {
					a = "0"
				}
				if b == "" {
					b = "0"
				}
				if a != b {
					continue
				}
			case "video/av1":
				profile := peer["profile"]
				if profile != "" && profile != "0" {
					continue
				}
			}
			return true
		}
	}
	return false
}

// Constrained baseline H.264 level 5.2 covers the offered 4K/60 ceiling;
// negotiation may select a lower level, which the client validates separately.
func screenCodecs() []webrtc.RTPCodecParameters {
	feedback := []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "ccm", Parameter: "fir"}, {Type: "goog-remb"}}
	return []webrtc.RTPCodecParameters{
		{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000, RTCPFeedback: feedback}, PayloadType: 96},
		{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeRTX, ClockRate: 90000, SDPFmtpLine: "apt=96"}, PayloadType: 97},
		{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e034", RTCPFeedback: feedback}, PayloadType: 102},
		{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeRTX, ClockRate: 90000, SDPFmtpLine: "apt=102"}, PayloadType: 103},
		{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP9, ClockRate: 90000, SDPFmtpLine: "profile-id=0", RTCPFeedback: feedback}, PayloadType: 98},
		{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeRTX, ClockRate: 90000, SDPFmtpLine: "apt=98"}, PayloadType: 99},
		{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeAV1, ClockRate: 90000, RTCPFeedback: feedback}, PayloadType: 104},
		{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeRTX, ClockRate: 90000, SDPFmtpLine: "apt=104"}, PayloadType: 105},
	}
}
