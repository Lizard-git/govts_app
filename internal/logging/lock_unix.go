//go:build !windows

package logging

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockLogFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}
