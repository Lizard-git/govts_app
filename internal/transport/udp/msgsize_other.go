//go:build !windows

package udp

func isMessageTooLong(error) bool {
	return false
}
