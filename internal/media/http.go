package media

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"example.com/go-voice-mvp/internal/domain"
	"example.com/go-voice-mvp/internal/voice"
)

const maxSignalingBody = 256 * 1024

type HTTPHandler struct {
	hub     *voice.Hub
	manager *Manager
}

func NewHTTPHandler(hub *voice.Hub, manager *Manager) (*HTTPHandler, error) {
	if hub == nil || manager == nil {
		return nil, errors.New("hub and media manager are required")
	}
	return &HTTPHandler{hub: hub, manager: manager}, nil
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID, ok := h.authenticate(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/media/publish":
		h.publish(w, r, sessionID)
	case "/media/subscribe":
		h.subscribe(w, r, sessionID)
	case "/media/stop":
		h.stop(w, r, sessionID)
	case "/media/unsubscribe":
		h.unsubscribe(w, r, sessionID)
	default:
		http.NotFound(w, r)
	}
}

type publishRequest struct {
	Offer SessionDescription `json:"offer"`
}
type publishResponse struct {
	StreamID string             `json:"streamId"`
	Answer   SessionDescription `json:"answer"`
}
type subscribeRequest struct {
	StreamID     string             `json:"streamId"`
	SubscriberID string             `json:"subscriberId"`
	Offer        SessionDescription `json:"offer"`
}
type subscribeResponse struct {
	Answer SessionDescription `json:"answer"`
}
type streamRequest struct {
	StreamID     string `json:"streamId"`
	SubscriberID string `json:"subscriberId,omitempty"`
}

func (h *HTTPHandler) publish(w http.ResponseWriter, r *http.Request, sessionID uint64) {
	var request publishRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	ctx, cancel := signalingContext(r)
	defer cancel()
	result, err := h.manager.Publish(ctx, sessionID, request.Offer)
	if err != nil {
		writeMediaError(w, err)
		return
	}
	writeJSON(w, publishResponse{StreamID: strconv.FormatUint(uint64(result.StreamID), 10), Answer: result.Answer})
}

func (h *HTTPHandler) subscribe(w http.ResponseWriter, r *http.Request, sessionID uint64) {
	var request subscribeRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	streamID, err := parseStreamID(request.StreamID)
	if err != nil || request.SubscriberID == "" || len(request.SubscriberID) > 128 {
		http.Error(w, "invalid subscription", http.StatusBadRequest)
		return
	}
	ctx, cancel := signalingContext(r)
	defer cancel()
	result, err := h.manager.Subscribe(ctx, sessionID, streamID, request.SubscriberID, request.Offer)
	if err != nil {
		writeMediaError(w, err)
		return
	}
	writeJSON(w, subscribeResponse{Answer: result.Answer})
}

func (h *HTTPHandler) stop(w http.ResponseWriter, r *http.Request, sessionID uint64) {
	var request streamRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	streamID, err := parseStreamID(request.StreamID)
	if err != nil {
		http.Error(w, "invalid stream ID", http.StatusBadRequest)
		return
	}
	h.manager.StopPublisher(sessionID, streamID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) unsubscribe(w http.ResponseWriter, r *http.Request, sessionID uint64) {
	var request streamRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	streamID, err := parseStreamID(request.StreamID)
	if err != nil || request.SubscriberID == "" || len(request.SubscriberID) > 128 {
		http.Error(w, "invalid subscription", http.StatusBadRequest)
		return
	}
	h.manager.Unsubscribe(sessionID, streamID, request.SubscriberID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) authenticate(r *http.Request) (uint64, bool) {
	sessionID, err := strconv.ParseUint(r.Header.Get("X-Govts-Session"), 10, 64)
	if err != nil || sessionID == 0 {
		return 0, false
	}
	value := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return 0, false
	}
	var credential [32]byte
	copy(credential[:], decoded)
	return sessionID, h.hub.AuthenticateMedia(sessionID, credential)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	body := http.MaxBytesReader(w, r.Body, maxSignalingBody)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "trailing JSON", http.StatusBadRequest)
		return false
	}
	return true
}

func parseStreamID(value string) (domain.StreamID, error) {
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 {
		return 0, errors.New("invalid stream ID")
	}
	return domain.StreamID(id), nil
}
func writeJSON(w http.ResponseWriter, value any) {
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, "encode response", http.StatusInternalServerError)
	}
}
func signalingContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 15*time.Second)
}
func writeMediaError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, ErrSubscriptionDenied) {
		status = http.StatusForbidden
	}
	http.Error(w, fmt.Sprintf("%v", err), status)
}
