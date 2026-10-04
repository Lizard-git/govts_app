package appversion

import (
	"fmt"
	"strconv"
	"strings"
)

// SecureMinimumServerVersion is the oldest server a secure client accepts.
// The server treats a handshake at this level or newer as able to decode voice bundles.
const SecureMinimumServerVersion = "0.2.13"

// SecureMinimumServer is SecureMinimumServerVersion packed into a handshake Sequence.
var SecureMinimumServer = mustParse(SecureMinimumServerVersion)

// Number fits in the existing 32-bit Sequence field of Hello packets.
// Components are encoded as major:8, minor:8, patch:16.
type Number uint32

func mustParse(text string) Number {
	number, err := Parse(text)
	if err != nil {
		panic(err)
	}
	return number
}

func Parse(text string) (Number, error) {
	parts := strings.Split(text, ".")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid version %q: expected major.minor.patch", text)
	}
	var values [3]uint64
	limits := [3]uint64{255, 255, 65535}
	for i, part := range parts {
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil || strconv.FormatUint(value, 10) != part || value > limits[i] {
			return 0, fmt.Errorf("invalid version %q: components must be canonical non-negative integers within 255.255.65535", text)
		}
		values[i] = value
	}
	return Number(values[0]<<24 | values[1]<<16 | values[2]), nil
}

func (n Number) String() string {
	return fmt.Sprintf("%d.%d.%d", uint32(n)>>24, (uint32(n)>>16)&255, uint32(n)&65535)
}
