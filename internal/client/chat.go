package client

import (
	"context"
	"errors"
	"time"
	"uniclog.io/govts/internal/domain"
	"uniclog.io/govts/internal/protocol"
	"uniclog.io/govts/internal/transport/udp"
)

func (s *State) ChatChanged() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatRevision++
	s.notifyLocked()
}

func RequestChat(ctx context.Context, conn *udp.ClientPacketConn, state *State, request protocol.ChatRequest) (domain.ChatPage, error) {
	b, err := protocol.EncodeChatRequest(request)
	if err != nil {
		return domain.ChatPage{}, err
	}
	generation := state.Generation()
	response, err := doRequestAttempts(ctx, conn, state, protocol.VoicePacket{Type: protocol.PacketChatRequest, Payload: b}, time.Second, 3)
	if err != nil {
		return domain.ChatPage{}, err
	}
	if state.Generation() != generation {
		return domain.ChatPage{}, errors.New("сессия чата изменилась")
	}
	if response.Type != protocol.PacketChatAck {
		return domain.ChatPage{}, errors.New("неподдерживаемый ответ чата")
	}
	return protocol.DecodeChatPage(response.Payload)
}
