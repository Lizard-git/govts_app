package filetransfer

import (
	"errors"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const ChunkSize = 4 << 20

type Config struct {
	Directory            string
	MaxFileBytes         int64
	UserQuotaBytes       int64
	ServerQuotaBytes     int64
	UserBytesPerSecond   int
	ServerBytesPerSecond int
	MaxTransfers         int
	UploadTTL            time.Duration
	Retention            time.Duration
	MinFreeBytes         uint64
	UploadsEnabled       bool
	DownloadsEnabled     bool
	Revision             uint64
}

func DefaultConfig(dir string) Config {
	return Config{Directory: dir, MaxFileBytes: 1 << 30, UserQuotaBytes: 10 << 30, ServerQuotaBytes: 100 << 30, UserBytesPerSecond: 512 << 10, ServerBytesPerSecond: 4 << 20, MaxTransfers: 8, UploadTTL: 24 * time.Hour, MinFreeBytes: 1 << 30, UploadsEnabled: true, DownloadsEnabled: true}
}
func (c Config) Validate() error {
	if c.Directory == "" || c.MaxFileBytes < 1 || c.MaxFileBytes > 1<<40 || c.UserQuotaBytes < c.MaxFileBytes || c.ServerQuotaBytes < c.UserQuotaBytes || c.ServerQuotaBytes > 1<<50 || c.UserBytesPerSecond < 16<<10 || c.ServerBytesPerSecond < c.UserBytesPerSecond || c.ServerBytesPerSecond > 1<<30 || c.MaxTransfers < 1 || c.MaxTransfers > 256 || c.UploadTTL < time.Minute || c.Retention < 0 || c.MinFreeBytes > 1<<50 {
		return errors.New("invalid file transfer configuration")
	}
	return nil
}
func SafeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '/' || r == '\\' || strings.ContainsRune("<>:\"|?*", r) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return '_'
		}
		return r
	}, strings.ToValidUTF8(name, "_"))
	for len(name) > 128 {
		_, n := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-n]
	}
	name = strings.Trim(name, " .")
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if base == "" || base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9') {
		name = "_" + name
	}
	for len(name) > 128 {
		_, n := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-n]
	}
	return name
}
