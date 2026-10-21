package protocol

import (
	"encoding/binary"
	"errors"
	"unicode/utf8"

	"uniclog.io/sonoryx/internal/domain"
)

const AdminSchemaVersion = 1

// Bits of the privileges byte in PacketAuthAck and PacketAccountPrivileges.
// The first four match the stored permissions of internal/persist; Owner is
// set for the server owner only.
const (
	PrivilegeKick uint8 = 1 << iota
	PrivilegeBan
	PrivilegeDrag
	PrivilegeAdmin
	PrivilegeOwner
)

// Bounded string fields of administration packets. The server shortens longer
// stored text before encoding.
const (
	MaxAdminReasonBytes = 128
	MaxAdminActorBytes  = 32
	MaxAdminActionBytes = 32
	MaxAdminDetailBytes = 160
)

type AdminOperation uint8

const (
	AdminAccounts AdminOperation = iota + 1
	AdminSetPermission
	AdminSetJoinLevel
	AdminSetBan
	AdminSetChannelJoinLevel
	AdminAudit
)

// AdminRequest is one PacketAdminRequest. Cursor pages AdminAccounts (after
// this account ID) and AdminAudit (before this entry ID, 0 = newest). TargetID
// is an account ID, or a channel ID for AdminSetChannelJoinLevel.
type AdminRequest struct {
	Operation  AdminOperation
	Cursor     uint64
	TargetID   uint64
	Permission uint8
	Enabled    bool
	Level      uint16
	Reason     string
}

const adminRequestHeaderSize = 22

func EncodeAdminRequest(r AdminRequest) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	b := []byte{AdminSchemaVersion, byte(r.Operation), r.Permission, 0}
	if r.Enabled {
		b[3] = 1
	}
	b = binary.BigEndian.AppendUint64(b, r.Cursor)
	b = binary.BigEndian.AppendUint64(b, r.TargetID)
	b = binary.BigEndian.AppendUint16(b, r.Level)
	return appendString(b, r.Reason, MaxAdminReasonBytes)
}

func DecodeAdminRequest(b []byte) (AdminRequest, error) {
	var r AdminRequest
	if len(b) < adminRequestHeaderSize+2 || len(b) > MaxPayloadSize || b[0] != AdminSchemaVersion || b[3] > 1 {
		return r, errors.New("invalid admin request")
	}
	r.Operation, r.Permission, r.Enabled = AdminOperation(b[1]), b[2], b[3] == 1
	r.Cursor = binary.BigEndian.Uint64(b[4:12])
	r.TargetID = binary.BigEndian.Uint64(b[12:20])
	r.Level = binary.BigEndian.Uint16(b[20:22])
	var err error
	r.Reason, b, err = takeString(b[adminRequestHeaderSize:], MaxAdminReasonBytes)
	if err != nil || len(b) != 0 {
		return r, errors.New("invalid admin request reason")
	}
	return r, r.Validate()
}

func (r AdminRequest) Validate() error {
	if r.Operation < AdminAccounts || r.Operation > AdminAudit {
		return errors.New("invalid admin operation")
	}
	target, permission, enabled, level, reason := r.TargetID != 0, r.Permission != 0, r.Enabled, r.Level != 0, r.Reason != ""
	var ok bool
	switch r.Operation {
	case AdminAccounts, AdminAudit:
		ok = !target && !permission && !enabled && !level && !reason
	case AdminSetPermission:
		ok = target && r.Cursor == 0 && permission && r.Permission&(r.Permission-1) == 0 && !level && !reason
	case AdminSetJoinLevel, AdminSetChannelJoinLevel:
		ok = target && r.Cursor == 0 && !permission && !enabled && !reason
	case AdminSetBan:
		ok = target && r.Cursor == 0 && !permission && !level && (enabled || !reason)
	}
	if !ok || r.Cursor > 1<<63-1 || r.TargetID > 1<<63-1 || !utf8.ValidString(r.Reason) {
		return errors.New("invalid admin request fields")
	}
	return nil
}

// Account flags of AdminAccount.
const (
	AdminAccountBanned uint8 = 1 << iota
	AdminAccountOwner
	AdminAccountOnline
)

type AdminAccount struct {
	ID          uint64
	Fingerprint [8]byte
	JoinLevel   uint16
	Permissions uint8
	Flags       uint8
	Name        string
	BanReason   string
}

type AdminAuditEntry struct {
	ID           uint64
	HappenedAtMS int64
	TargetUserID uint64
	Actor        string
	Action       string
	Detail       string
}

// AdminResponse is the payload of PacketAdminAck. Next is the cursor of the
// following page, 0 when the list is complete.
type AdminResponse struct {
	Operation AdminOperation
	Next      uint64
	Accounts  []AdminAccount
	Audit     []AdminAuditEntry
}

const adminResponseHeaderSize = 12

// AdminAccountSize is the encoded size of a, for filling one datagram.
func AdminAccountSize(a AdminAccount) int {
	return 8 + 8 + 2 + 1 + 1 + 2 + len(a.Name) + 2 + len(a.BanReason)
}

// AdminAuditEntrySize is the encoded size of e, for filling one datagram.
func AdminAuditEntrySize(e AdminAuditEntry) int {
	return 8 + 8 + 8 + 2 + len(e.Actor) + 2 + len(e.Action) + 2 + len(e.Detail)
}

// AdminResponseBudget is the space left for list entries in one datagram.
const AdminResponseBudget = MaxPayloadSize - adminResponseHeaderSize

func EncodeAdminResponse(r AdminResponse) ([]byte, error) {
	if r.Operation < AdminAccounts || r.Operation > AdminAudit || len(r.Accounts) > 255 || len(r.Audit) > 255 || r.Next > 1<<63-1 {
		return nil, errors.New("invalid admin response")
	}
	b := []byte{AdminSchemaVersion, byte(r.Operation), byte(len(r.Accounts)), byte(len(r.Audit))}
	b = binary.BigEndian.AppendUint64(b, r.Next)
	var err error
	for _, a := range r.Accounts {
		if a.ID == 0 || a.Flags > AdminAccountBanned|AdminAccountOwner|AdminAccountOnline {
			return nil, errors.New("invalid admin account")
		}
		b = binary.BigEndian.AppendUint64(b, a.ID)
		b = append(b, a.Fingerprint[:]...)
		b = binary.BigEndian.AppendUint16(b, a.JoinLevel)
		b = append(b, a.Permissions, a.Flags)
		if b, err = appendString(b, a.Name, domain.MaxParticipantNameBytes); err != nil {
			return nil, err
		}
		if b, err = appendString(b, a.BanReason, MaxAdminReasonBytes); err != nil {
			return nil, err
		}
	}
	for _, e := range r.Audit {
		if e.ID == 0 {
			return nil, errors.New("invalid admin audit entry")
		}
		b = binary.BigEndian.AppendUint64(b, e.ID)
		b = binary.BigEndian.AppendUint64(b, uint64(e.HappenedAtMS))
		b = binary.BigEndian.AppendUint64(b, e.TargetUserID)
		if b, err = appendString(b, e.Actor, MaxAdminActorBytes); err != nil {
			return nil, err
		}
		if b, err = appendString(b, e.Action, MaxAdminActionBytes); err != nil {
			return nil, err
		}
		if b, err = appendString(b, e.Detail, MaxAdminDetailBytes); err != nil {
			return nil, err
		}
	}
	if len(b) > MaxPayloadSize {
		return nil, ErrPayloadTooLarge
	}
	return b, nil
}

func DecodeAdminResponse(b []byte) (AdminResponse, error) {
	var r AdminResponse
	if len(b) < adminResponseHeaderSize || len(b) > MaxPayloadSize || b[0] != AdminSchemaVersion {
		return r, errors.New("invalid admin response")
	}
	r.Operation = AdminOperation(b[1])
	accounts, entries := int(b[2]), int(b[3])
	r.Next = binary.BigEndian.Uint64(b[4:12])
	if r.Operation < AdminAccounts || r.Operation > AdminAudit || r.Next > 1<<63-1 {
		return r, errors.New("invalid admin response")
	}
	b = b[adminResponseHeaderSize:]
	var err error
	for range accounts {
		if len(b) < 20 {
			return r, errors.New("truncated admin account")
		}
		var a AdminAccount
		a.ID = binary.BigEndian.Uint64(b[:8])
		copy(a.Fingerprint[:], b[8:16])
		a.JoinLevel = binary.BigEndian.Uint16(b[16:18])
		a.Permissions, a.Flags = b[18], b[19]
		if a.Name, b, err = takeString(b[20:], domain.MaxParticipantNameBytes); err != nil {
			return r, err
		}
		if a.BanReason, b, err = takeString(b, MaxAdminReasonBytes); err != nil {
			return r, err
		}
		r.Accounts = append(r.Accounts, a)
	}
	for range entries {
		if len(b) < 24 {
			return r, errors.New("truncated admin audit entry")
		}
		var e AdminAuditEntry
		e.ID = binary.BigEndian.Uint64(b[:8])
		e.HappenedAtMS = int64(binary.BigEndian.Uint64(b[8:16]))
		e.TargetUserID = binary.BigEndian.Uint64(b[16:24])
		if e.Actor, b, err = takeString(b[24:], MaxAdminActorBytes); err != nil {
			return r, err
		}
		if e.Action, b, err = takeString(b, MaxAdminActionBytes); err != nil {
			return r, err
		}
		if e.Detail, b, err = takeString(b, MaxAdminDetailBytes); err != nil {
			return r, err
		}
		r.Audit = append(r.Audit, e)
	}
	if len(b) != 0 {
		return r, errors.New("trailing admin response data")
	}
	return r, nil
}

// TruncateUTF8 shortens s to at most max bytes without splitting a rune.
func TruncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
