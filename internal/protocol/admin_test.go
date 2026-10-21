package protocol

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAdminRequestRoundTrip(t *testing.T) {
	for _, request := range []AdminRequest{
		{Operation: AdminAccounts},
		{Operation: AdminAccounts, Cursor: 42},
		{Operation: AdminAudit, Cursor: 7},
		{Operation: AdminSetPermission, TargetID: 3, Permission: PrivilegeAdmin, Enabled: true},
		{Operation: AdminSetPermission, TargetID: 3, Permission: PrivilegeKick},
		{Operation: AdminSetJoinLevel, TargetID: 3, Level: 65535},
		{Operation: AdminSetJoinLevel, TargetID: 3},
		{Operation: AdminSetBan, TargetID: 3, Enabled: true, Reason: "спам"},
		{Operation: AdminSetBan, TargetID: 3},
		{Operation: AdminSetChannelJoinLevel, TargetID: 9, Level: 25},
	} {
		encoded, err := EncodeAdminRequest(request)
		if err != nil {
			t.Fatalf("EncodeAdminRequest(%+v) error = %v", request, err)
		}
		decoded, err := DecodeAdminRequest(encoded)
		if err != nil {
			t.Fatalf("DecodeAdminRequest(%+v) error = %v", request, err)
		}
		if decoded != request {
			t.Fatalf("round trip = %+v, want %+v", decoded, request)
		}
	}
}

func TestAdminRequestRejectsFieldsForeignToTheOperation(t *testing.T) {
	for name, request := range map[string]AdminRequest{
		"unknown operation":         {Operation: 0},
		"operation past the end":    {Operation: AdminAudit + 1},
		"list with target":          {Operation: AdminAccounts, TargetID: 1},
		"list with reason":          {Operation: AdminAudit, Reason: "x"},
		"permission without bit":    {Operation: AdminSetPermission, TargetID: 1},
		"permission with two bits":  {Operation: AdminSetPermission, TargetID: 1, Permission: PrivilegeKick | PrivilegeBan},
		"permission without target": {Operation: AdminSetPermission, Permission: PrivilegeKick, Enabled: true},
		"mutation with cursor":      {Operation: AdminSetJoinLevel, TargetID: 1, Cursor: 5},
		"join level with enabled":   {Operation: AdminSetJoinLevel, TargetID: 1, Enabled: true},
		"unban with reason":         {Operation: AdminSetBan, TargetID: 1, Reason: "x"},
		"ban with level":            {Operation: AdminSetBan, TargetID: 1, Enabled: true, Level: 1},
		"target above int64":        {Operation: AdminSetJoinLevel, TargetID: 1 << 63},
		"reason too long":           {Operation: AdminSetBan, TargetID: 1, Enabled: true, Reason: strings.Repeat("a", MaxAdminReasonBytes+1)},
	} {
		if _, err := EncodeAdminRequest(request); err == nil {
			t.Errorf("%s: EncodeAdminRequest(%+v) accepted", name, request)
		}
	}
}

func TestDecodeAdminRequestRejectsMalformedPayloads(t *testing.T) {
	valid, err := EncodeAdminRequest(AdminRequest{Operation: AdminSetBan, TargetID: 3, Enabled: true, Reason: "spam"})
	if err != nil {
		t.Fatal(err)
	}
	wrongVersion := append([]byte(nil), valid...)
	wrongVersion[0] = AdminSchemaVersion + 1
	badFlag := append([]byte(nil), valid...)
	badFlag[3] = 2
	for name, payload := range map[string][]byte{
		"empty":         nil,
		"truncated":     valid[:len(valid)-1],
		"trailing":      append(append([]byte(nil), valid...), 0),
		"wrong version": wrongVersion,
		"bad flag":      badFlag,
	} {
		if _, err := DecodeAdminRequest(payload); err == nil {
			t.Errorf("%s: DecodeAdminRequest accepted %x", name, payload)
		}
	}
}

func TestAdminResponseRoundTripAndSizes(t *testing.T) {
	response := AdminResponse{
		Operation: AdminAccounts,
		Next:      12,
		Accounts: []AdminAccount{
			{ID: 3, Fingerprint: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}, JoinLevel: 25, Permissions: PrivilegeKick | PrivilegeAdmin, Flags: AdminAccountOnline, Name: "Алиса", BanReason: ""},
			{ID: 12, JoinLevel: 0, Flags: AdminAccountBanned | AdminAccountOwner, Name: "bob", BanReason: "spam"},
		},
	}
	encoded, err := EncodeAdminResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	want := adminResponseHeaderSize
	for _, account := range response.Accounts {
		want += AdminAccountSize(account)
	}
	if len(encoded) != want {
		t.Fatalf("encoded size = %d, AdminAccountSize sum = %d", len(encoded), want)
	}
	decoded, err := DecodeAdminResponse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, response) {
		t.Fatalf("round trip = %+v, want %+v", decoded, response)
	}

	audit := AdminResponse{Operation: AdminAudit, Audit: []AdminAuditEntry{
		{ID: 9, HappenedAtMS: 1_760_000_000_000, TargetUserID: 3, Actor: "local-console", Action: "set-ban", Detail: "local console"},
		{ID: 8, HappenedAtMS: 1_759_999_999_000, Actor: "4", Action: "set-channel-join-level", Detail: "2=25"},
	}}
	encoded, err = EncodeAdminResponse(audit)
	if err != nil {
		t.Fatal(err)
	}
	want = adminResponseHeaderSize
	for _, entry := range audit.Audit {
		want += AdminAuditEntrySize(entry)
	}
	if len(encoded) != want {
		t.Fatalf("encoded audit size = %d, AdminAuditEntrySize sum = %d", len(encoded), want)
	}
	decodedAudit, err := DecodeAdminResponse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decodedAudit, audit) {
		t.Fatalf("audit round trip = %+v, want %+v", decodedAudit, audit)
	}
}

func TestAdminResponseRejectsInvalidContent(t *testing.T) {
	for name, response := range map[string]AdminResponse{
		"unknown operation": {Operation: 0},
		"zero account ID":   {Operation: AdminAccounts, Accounts: []AdminAccount{{Name: "x"}}},
		"unknown flags":     {Operation: AdminAccounts, Accounts: []AdminAccount{{ID: 1, Flags: 1 << 7}}},
		"long name":         {Operation: AdminAccounts, Accounts: []AdminAccount{{ID: 1, Name: strings.Repeat("a", 65)}}},
		"zero audit ID":     {Operation: AdminAudit, Audit: []AdminAuditEntry{{Action: "x"}}},
		"long detail":       {Operation: AdminAudit, Audit: []AdminAuditEntry{{ID: 1, Detail: strings.Repeat("a", MaxAdminDetailBytes+1)}}},
	} {
		if _, err := EncodeAdminResponse(response); err == nil {
			t.Errorf("%s: EncodeAdminResponse accepted %+v", name, response)
		}
	}
	tooLarge := AdminResponse{Operation: AdminAccounts}
	for id := uint64(1); id <= 20; id++ {
		tooLarge.Accounts = append(tooLarge.Accounts, AdminAccount{ID: id, Name: strings.Repeat("n", 64)})
	}
	if _, err := EncodeAdminResponse(tooLarge); err == nil {
		t.Error("EncodeAdminResponse accepted a payload above MaxPayloadSize")
	}

	valid, err := EncodeAdminResponse(AdminResponse{Operation: AdminAccounts, Accounts: []AdminAccount{{ID: 1, Name: "a"}}})
	if err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string][]byte{
		"truncated": valid[:len(valid)-1],
		"trailing":  append(append([]byte(nil), valid...), 0),
	} {
		if _, err := DecodeAdminResponse(payload); err == nil {
			t.Errorf("%s: DecodeAdminResponse accepted %x", name, payload)
		}
	}
}

func TestTruncateUTF8KeepsWholeRunes(t *testing.T) {
	if got := TruncateUTF8("short", 10); got != "short" {
		t.Fatalf("TruncateUTF8 short = %q", got)
	}
	got := TruncateUTF8("ааааа", 5) // two-byte runes
	if got != "аа" || !utf8.ValidString(got) {
		t.Fatalf("TruncateUTF8 cyrillic = %q", got)
	}
}
