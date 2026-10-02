package clientapp

import (
	"context"
	"errors"
	"strconv"
	voiceclient "uniclog.io/govts/internal/client"
	"uniclog.io/govts/internal/domain"
	"uniclog.io/govts/internal/protocol"
)

func (a *App) ChatRequest(ctx context.Context, expectedContext string, request protocol.ChatRequest) (domain.ChatPage, error) {
	a.mu.Lock()
	conn := a.currentConn
	currentContext := a.chatContextLocked() + "|" + strconv.FormatInt(a.state.SnapshotView().UserID, 10)
	a.mu.Unlock()
	if conn == nil || a.state.ConnectionStatus() != voiceclient.ConnectionConnected {
		return domain.ChatPage{}, ErrNotConnected
	}
	if expectedContext != currentContext {
		return domain.ChatPage{}, errors.New("сервер или учётная запись чата изменились")
	}
	return voiceclient.RequestChat(ctx, conn, a.state, request)
}

func (a *App) ChatContext() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.chatContextLocked()
}

func (a *App) chatContextLocked() string {
	return a.serverEndpoint.String() + "|" + a.chatServerIdentity + "|" + a.chatClientIdentity
}
