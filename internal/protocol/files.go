package protocol

type FileAccess struct {
	Port        uint16 `json:"port"`
	Fingerprint string `json:"fingerprint"`
	Credential  string `json:"credential"`
	Expires     int64  `json:"expires"`
	Schema      uint8  `json:"schema"`
}
