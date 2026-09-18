// Package mediasignal contains transport-neutral DTOs shared by the desktop
// client and the media signaling server. It deliberately has no dependency on
// the SFU implementation or Pion.
package mediasignal

type SessionDescription struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

type PublishRequest struct {
	Offer SessionDescription `json:"offer"`
}

type PublishResponse struct {
	StreamID string             `json:"streamId"`
	Answer   SessionDescription `json:"answer"`
}

type SubscribeRequest struct {
	StreamID     string             `json:"streamId"`
	SubscriberID string             `json:"subscriberId"`
	Offer        SessionDescription `json:"offer"`
}

type SubscribeResponse struct {
	Answer SessionDescription `json:"answer"`
}

type StreamRequest struct {
	StreamID     string `json:"streamId"`
	SubscriberID string `json:"subscriberId,omitempty"`
}
