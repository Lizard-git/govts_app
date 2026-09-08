//go:build windows

package udp

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isMessageTooLong(err error) bool {
	return errors.Is(err, windows.WSAEMSGSIZE)
}
