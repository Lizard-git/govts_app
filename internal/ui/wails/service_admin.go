package wailsui

import (
	"context"
	"encoding/hex"
	"errors"

	"uniclog.io/sonoryx/internal/protocol"
)

type AdminAccountDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	JoinLevel   uint16 `json:"joinLevel"`
	Kick        bool   `json:"kick"`
	Ban         bool   `json:"ban"`
	Drag        bool   `json:"drag"`
	Admin       bool   `json:"admin"`
	Owner       bool   `json:"owner"`
	Banned      bool   `json:"banned"`
	Online      bool   `json:"online"`
	BanReason   string `json:"banReason"`
}

type AdminAccountsPageDTO struct {
	Accounts []AdminAccountDTO `json:"accounts"`
	Next     string            `json:"next"`
}

type AdminAuditEntryDTO struct {
	ID           string `json:"id"`
	HappenedAtMS int64  `json:"happenedAtMs"`
	Actor        string `json:"actor"`
	Action       string `json:"action"`
	TargetUserID string `json:"targetUserId"`
	Detail       string `json:"detail"`
}

type AdminAuditPageDTO struct {
	Entries []AdminAuditEntryDTO `json:"entries"`
	Next    string               `json:"next"`
}

func adminAccountDTO(a protocol.AdminAccount) AdminAccountDTO {
	return AdminAccountDTO{
		ID:          formatUint64(a.ID),
		Name:        a.Name,
		Fingerprint: hex.EncodeToString(a.Fingerprint[:]),
		JoinLevel:   a.JoinLevel,
		Kick:        a.Permissions&protocol.PrivilegeKick != 0,
		Ban:         a.Permissions&protocol.PrivilegeBan != 0,
		Drag:        a.Permissions&protocol.PrivilegeDrag != 0,
		Admin:       a.Permissions&protocol.PrivilegeAdmin != 0,
		Owner:       a.Flags&protocol.AdminAccountOwner != 0,
		Banned:      a.Flags&protocol.AdminAccountBanned != 0,
		Online:      a.Flags&protocol.AdminAccountOnline != 0,
		BanReason:   a.BanReason,
	}
}

func (s *Service) administer(request protocol.AdminRequest) (protocol.AdminResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()
	return s.client.Administer(ctx, request)
}

func (s *Service) administerAccount(request protocol.AdminRequest) (AdminAccountDTO, error) {
	response, err := s.administer(request)
	if err != nil {
		return AdminAccountDTO{}, err
	}
	if len(response.Accounts) != 1 {
		return AdminAccountDTO{}, errors.New("некорректный ответ сервера")
	}
	return adminAccountDTO(response.Accounts[0]), nil
}

// AdminAccounts returns the page of accounts after the account ID in cursor.
func (s *Service) AdminAccounts(cursor string) (AdminAccountsPageDTO, error) {
	after, err := parseUint64(cursor, "cursor", true)
	if err != nil {
		return AdminAccountsPageDTO{}, err
	}
	response, err := s.administer(protocol.AdminRequest{Operation: protocol.AdminAccounts, Cursor: after})
	if err != nil {
		return AdminAccountsPageDTO{}, err
	}
	page := AdminAccountsPageDTO{Accounts: make([]AdminAccountDTO, 0, len(response.Accounts))}
	for _, account := range response.Accounts {
		page.Accounts = append(page.Accounts, adminAccountDTO(account))
	}
	if response.Next != 0 {
		page.Next = formatUint64(response.Next)
	}
	return page, nil
}

// AdminSetPermission grants or revokes one of "kick", "ban", "drag", "admin".
func (s *Service) AdminSetPermission(accountID, permission string, enabled bool) (AdminAccountDTO, error) {
	id, err := parseUint64(accountID, "accountId", false)
	if err != nil {
		return AdminAccountDTO{}, err
	}
	var bit uint8
	switch permission {
	case "kick":
		bit = protocol.PrivilegeKick
	case "ban":
		bit = protocol.PrivilegeBan
	case "drag":
		bit = protocol.PrivilegeDrag
	case "admin":
		bit = protocol.PrivilegeAdmin
	default:
		return AdminAccountDTO{}, errors.New("неизвестное право")
	}
	return s.administerAccount(protocol.AdminRequest{Operation: protocol.AdminSetPermission, TargetID: id, Permission: bit, Enabled: enabled})
}

func (s *Service) AdminSetJoinLevel(accountID string, level uint16) (AdminAccountDTO, error) {
	id, err := parseUint64(accountID, "accountId", false)
	if err != nil {
		return AdminAccountDTO{}, err
	}
	return s.administerAccount(protocol.AdminRequest{Operation: protocol.AdminSetJoinLevel, TargetID: id, Level: level})
}

func (s *Service) AdminSetBan(accountID string, banned bool, reason string) (AdminAccountDTO, error) {
	id, err := parseUint64(accountID, "accountId", false)
	if err != nil {
		return AdminAccountDTO{}, err
	}
	if !banned {
		reason = ""
	}
	if len(reason) > protocol.MaxAdminReasonBytes {
		return AdminAccountDTO{}, errors.New("причина бана слишком длинная")
	}
	return s.administerAccount(protocol.AdminRequest{Operation: protocol.AdminSetBan, TargetID: id, Enabled: banned, Reason: reason})
}

func (s *Service) AdminSetChannelJoinLevel(channelID string, level uint16) error {
	id, err := parseUint64(channelID, "channelId", false)
	if err != nil {
		return err
	}
	_, err = s.administer(protocol.AdminRequest{Operation: protocol.AdminSetChannelJoinLevel, TargetID: id, Level: level})
	return err
}

// AdminAudit returns the page of audit entries older than the entry ID in
// cursor; an empty cursor starts from the newest entry.
func (s *Service) AdminAudit(cursor string) (AdminAuditPageDTO, error) {
	before, err := parseUint64(cursor, "cursor", true)
	if err != nil {
		return AdminAuditPageDTO{}, err
	}
	response, err := s.administer(protocol.AdminRequest{Operation: protocol.AdminAudit, Cursor: before})
	if err != nil {
		return AdminAuditPageDTO{}, err
	}
	page := AdminAuditPageDTO{Entries: make([]AdminAuditEntryDTO, 0, len(response.Audit))}
	for _, entry := range response.Audit {
		target := ""
		if entry.TargetUserID != 0 {
			target = formatUint64(entry.TargetUserID)
		}
		page.Entries = append(page.Entries, AdminAuditEntryDTO{ID: formatUint64(entry.ID), HappenedAtMS: entry.HappenedAtMS, Actor: entry.Actor, Action: entry.Action, TargetUserID: target, Detail: entry.Detail})
	}
	if response.Next != 0 {
		page.Next = formatUint64(response.Next)
	}
	return page, nil
}
