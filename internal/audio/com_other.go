//go:build !windows

package audio

func withAudioThread(fn func()) { fn() }
