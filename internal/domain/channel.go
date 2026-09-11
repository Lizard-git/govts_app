package domain

const (
	MaxChannelNameBytes        = 64
	MaxChannelTopicBytes       = 128
	MaxChannelDescriptionBytes = 512
	MaxParticipantNameBytes    = 64
	MaxServerNameBytes         = 128
	MaxChannelDepth            = 8
)

type ChannelID uint64

type StateRevision uint64

type ChannelType uint8

const (
	ChannelTypePermanent ChannelType = iota + 1
)

type AudioCodec uint8

const (
	AudioCodecOpus AudioCodec = iota + 1
)

type OpusApplication uint8

const (
	OpusApplicationAudio OpusApplication = iota + 1
	OpusApplicationVoIP
)

type AudioProfile struct {
	Codec           AudioCodec
	SampleRate      uint32
	Channels        uint8
	FrameDurationMS uint16
	Bitrate         uint32
	Application     OpusApplication
}

func DefaultAudioProfile() AudioProfile {
	return AudioProfile{
		Codec:           AudioCodecOpus,
		SampleRate:      48_000,
		Channels:        1,
		FrameDurationMS: 20,
		Bitrate:         24_000,
		Application:     OpusApplicationAudio,
	}
}

type Channel struct {
	ID          ChannelID
	ParentID    ChannelID
	Name        string
	Topic       string
	Description string
	Position    uint32
	MaxUsers    uint32
	Type        ChannelType
	Audio       AudioProfile
}

type Participant struct {
	SessionID   uint64
	DisplayName string
	ChannelID   ChannelID
}

type ServerInfo struct {
	Name string
}

// ServerSnapshot is the immutable, client-safe view of authoritative server state.
type ServerSnapshot struct {
	Revision     StateRevision
	Info         ServerInfo
	Channels     []Channel
	Participants []Participant
}

func (snapshot ServerSnapshot) Clone() ServerSnapshot {
	snapshot.Channels = append([]Channel(nil), snapshot.Channels...)
	snapshot.Participants = append([]Participant(nil), snapshot.Participants...)
	return snapshot
}
