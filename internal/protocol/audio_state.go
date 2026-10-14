package protocol

import "errors"

const (
	audioFlagMuted    byte = 1 << 0
	audioFlagDeafened byte = 1 << 1
	audioFlagAway     byte = 1 << 2
)

func EncodeAudioState(muted, deafened bool) []byte {
	return []byte{audioStateFlags(muted, deafened)}
}

func DecodeAudioState(payload []byte) (bool, bool, error) {
	if len(payload) != 1 {
		return false, false, errors.New("invalid audio state payload")
	}
	return audioStateFromFlags(payload[0])
}

func audioStateFlags(muted, deafened bool) byte {
	var flags byte
	if muted {
		flags |= audioFlagMuted
	}
	if deafened {
		flags |= audioFlagDeafened
	}
	return flags
}

func audioStateFromFlags(flags byte) (bool, bool, error) {
	if flags&^(audioFlagMuted|audioFlagDeafened) != 0 {
		return false, false, errors.New("invalid audio state flags")
	}
	return flags&audioFlagMuted != 0, flags&audioFlagDeafened != 0, nil
}

func participantStateFlags(muted, deafened, away bool) byte {
	flags := audioStateFlags(muted, deafened)
	if away {
		flags |= audioFlagAway
	}
	return flags
}

func participantStateFromFlags(flags byte) (bool, bool, bool, error) {
	if flags&^(audioFlagMuted|audioFlagDeafened|audioFlagAway) != 0 {
		return false, false, false, errors.New("invalid participant flags")
	}
	return flags&audioFlagMuted != 0, flags&audioFlagDeafened != 0, flags&audioFlagAway != 0, nil
}

func EncodePresence(away bool) []byte {
	if away {
		return []byte{1}
	}
	return []byte{0}
}

func DecodePresence(payload []byte) (bool, error) {
	if len(payload) != 1 || payload[0] > 1 {
		return false, errors.New("invalid presence payload")
	}
	return payload[0] == 1, nil
}
