package domain

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const MaxChatTextBytes = 1000

type ChatKind uint8

const (
	ChatChannel ChatKind = iota + 1
	ChatDirect
)

type ChatTarget struct {
	Kind ChatKind
	ID   int64
}

func (t ChatTarget) Validate() error {
	if (t.Kind != ChatChannel && t.Kind != ChatDirect) || t.ID <= 0 {
		return errors.New("некорректный адресат сообщения")
	}
	return nil
}

func ValidateChatText(text string) error {
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || strings.ContainsRune(text, 0) {
		return errors.New("сообщение должно содержать текст UTF-8")
	}
	if len(text) > MaxChatTextBytes {
		return errors.New("сообщение превышает 1000 байт UTF-8")
	}
	return nil
}

type ChatMessage struct {
	ID          int64
	ChannelID   int64
	SenderID    int64
	RecipientID int64
	ClientID    [16]byte
	SentAtMS    int64
	SenderName  string
	Text        string
}

type ChatDialog struct {
	UserID      int64
	DisplayName string
	LatestID    int64
	ReadID      int64
	Unread      uint32
}

// ChatPage fits one protected control datagram. Cursor is exclusive.
type ChatPage struct {
	UserID   int64
	Unread   uint32
	LatestID int64
	ReadID   int64
	Cursor   int64
	HasMore  bool
	Messages []ChatMessage
	Dialogs  []ChatDialog
}
