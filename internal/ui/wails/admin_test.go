package wailsui

import (
	"strings"
	"testing"

	voiceclient "uniclog.io/sonoryx/internal/client"
	"uniclog.io/sonoryx/internal/domain"
	"uniclog.io/sonoryx/internal/protocol"
)

func TestViewDTOExposesLevelsToAdministratorsOnly(t *testing.T) {
	channels := []domain.Channel{{ID: 2, Name: "private", MinJoinLevel: 25, Audio: domain.DefaultAudioProfile()}}
	for _, item := range []struct {
		name         string
		privileges   uint8
		admin, owner bool
	}{
		{"moderator", protocol.PrivilegeKick | protocol.PrivilegeBan | protocol.PrivilegeDrag, false, false},
		{"administrator", protocol.PrivilegeAdmin, true, false},
		{"owner", protocol.PrivilegeOwner, true, true},
	} {
		dto := viewDTO(voiceclient.ClientViewState{JoinLevel: 24, Permissions: item.privileges, Channels: channels}, "")
		if dto.CanAdmin != item.admin || dto.IsOwner != item.owner {
			t.Errorf("%s: canAdmin=%t isOwner=%t", item.name, dto.CanAdmin, dto.IsOwner)
		}
		wantLevel, wantThreshold := uint16(0), uint16(0)
		if item.admin {
			wantLevel, wantThreshold = 24, 25
		}
		if dto.JoinLevel != wantLevel || dto.Channels[0].MinJoinLevel != wantThreshold || dto.Channels[0].CanJoin {
			t.Errorf("%s: joinLevel=%d channel=%+v", item.name, dto.JoinLevel, dto.Channels[0])
		}
	}
}

func TestAdminAccountDTOFlags(t *testing.T) {
	dto := adminAccountDTO(protocol.AdminAccount{
		ID: 7, Fingerprint: [8]byte{0xab, 0xcd}, JoinLevel: 3, Name: "alice", BanReason: "spam",
		Permissions: protocol.PrivilegeBan | protocol.PrivilegeAdmin,
		Flags:       protocol.AdminAccountBanned | protocol.AdminAccountOnline,
	})
	if dto.ID != "7" || dto.Fingerprint != "abcd000000000000" || dto.Kick || !dto.Ban || dto.Drag || !dto.Admin ||
		dto.Owner || !dto.Banned || !dto.Online || dto.BanReason != "spam" || dto.JoinLevel != 3 {
		t.Fatalf("account DTO = %+v", dto)
	}
}

func TestAdminServiceRejectsInvalidInputBeforeSending(t *testing.T) {
	service := &Service{}
	if _, err := service.AdminSetPermission("7", "owner", true); err == nil {
		t.Error("unknown permission accepted")
	}
	if _, err := service.AdminSetPermission("0", "kick", true); err == nil {
		t.Error("zero account accepted")
	}
	if _, err := service.AdminSetJoinLevel("x", 1); err == nil {
		t.Error("malformed account accepted")
	}
	if _, err := service.AdminSetBan("7", true, strings.Repeat("я", protocol.MaxAdminReasonBytes)); err == nil {
		t.Error("ban reason above the byte limit accepted")
	}
	if err := service.AdminSetChannelJoinLevel("", 1); err == nil {
		t.Error("empty channel accepted")
	}
	if _, err := service.AdminAccounts("-1"); err == nil {
		t.Error("negative cursor accepted")
	}
	if _, err := service.AdminAudit("abc"); err == nil {
		t.Error("malformed audit cursor accepted")
	}
}
