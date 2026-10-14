package client

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"uniclog.io/sonoryx/internal/domain"
)

type ClientViewState struct {
	ConnectionStatus   ConnectionStatus
	ServerInfo         domain.ServerInfo
	Revision           domain.StateRevision
	Channels           []domain.Channel
	Participants       []domain.Participant
	ScreenStreams      []domain.ScreenStream
	SessionID          uint64
	UserID             int64
	ChatRevision       uint64
	JoinLevel          uint16
	Permissions        uint8
	ChannelID          domain.ChannelID
	SnapshotFresh      bool
	Muted, Deafened    bool
	CaptureAvailable   bool
	RNNoiseEnabled     bool
	RNNoiseSensitivity float32
	MicrophoneGain     float32
	VADEnabled         bool
	VADMode            string
	VADSensitivity     float32
	VADOpen            bool
	Speaking           map[uint64]bool
	SoundPack          string
	SoundVolume        float32
	MuteOnAway         bool
}

type NotificationSound struct {
	Kind          NotificationSoundKind
	PlaybackEpoch uint64
	Pack          string
	Volume        float32
}

type NotificationSoundKind uint8

const (
	NotificationJoined NotificationSoundKind = iota + 1
	NotificationLeft
	NotificationChannelChanged
	NotificationMessageSent
	NotificationMessageReceived
	NotificationScreenStarted
	NotificationScreenStopped
	NotificationWake
	NotificationAway
	NotificationReturned
)

const minWakeVolume = float32(0.5)

func (s *State) NotificationSounds() <-chan NotificationSound { return s.notificationSounds }

func (s *State) PlayNotification(kind NotificationSoundKind) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queueNotificationSoundLocked(kind)
}

func (s *State) queueNotificationSoundLocked(sound NotificationSoundKind) {
	deafened, epoch := s.Audio.PlaybackSnapshot()
	if deafened {
		return
	}
	pack, volume := s.Audio.SoundSnapshot()
	// A wake exists to reach someone who is not listening, so a low or muted
	// cue volume does not silence it. Deafen still does.
	if sound == NotificationWake && volume < minWakeVolume {
		volume = minWakeVolume
	}
	if volume <= 0 {
		return
	}
	notice := NotificationSound{Kind: sound, PlaybackEpoch: epoch, Pack: pack, Volume: volume}
	select {
	case s.notificationSounds <- notice:
		return
	default:
	}
	// The mixer is behind. Drop the oldest cue so the action just taken is the one that plays.
	select {
	case <-s.notificationSounds:
	default:
	}
	select {
	case s.notificationSounds <- notice:
	default:
	}
}

func screenChannel(snapshot domain.ServerSnapshot, sessionID uint64, fallback domain.ChannelID) domain.ChannelID {
	for _, p := range snapshot.Participants {
		if p.SessionID == sessionID {
			if p.ChannelID != 0 {
				return p.ChannelID
			}
			break
		}
	}
	return fallback
}

// queueScreenDiffLocked plays start/stop cues for streams that appeared or
// disappeared since the previous fresh snapshot. A lost state event is
// recovered here; an unchanged resync stays silent.
func (s *State) queueScreenDiffLocked(previous []domain.ScreenStream) {
	if s.channelID == 0 {
		return
	}
	nextIDs := make(map[domain.StreamID]struct{}, len(s.snapshot.ScreenStreams))
	prevIDs := make(map[domain.StreamID]struct{}, len(previous))
	for _, stream := range previous {
		prevIDs[stream.ID] = struct{}{}
	}
	for _, stream := range s.snapshot.ScreenStreams {
		nextIDs[stream.ID] = struct{}{}
		if _, seen := prevIDs[stream.ID]; seen || stream.ChannelID != s.channelID {
			continue
		}
		s.queueNotificationSoundLocked(NotificationScreenStarted)
	}
	for _, stream := range previous {
		if _, seen := nextIDs[stream.ID]; seen || stream.ChannelID != s.channelID || stream.OwnerSessionID == s.sessionID {
			continue
		}
		s.queueNotificationSoundLocked(NotificationScreenStopped)
	}
}

func (s *State) ConfirmChannel(generation uint64, channel domain.ChannelID, revision domain.StateRevision) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != generation {
		return false
	}
	if s.syncedGeneration == generation && s.snapshot.Revision >= revision {
		return true
	}
	// The join ACK and its ParticipantMoved event are sent independently and
	// may arrive in either order. When the ACK announces exactly the next
	// revision, keep the snapshot eligible for that event instead of forcing a
	// resync. A lost event is still recovered by the periodic revision check.
	if s.syncedGeneration == generation && s.snapshotFresh && revision == s.snapshot.Revision+1 {
		if s.channelID != channel {
			s.measurements.resetIncoming()
		}
		s.channelID = channel
		if revision > s.observedRevision {
			s.observedRevision = revision
		}
		clear(s.speaking)
		s.notifyLocked()
		return true
	}
	if s.channelID != channel {
		s.measurements.resetIncoming()
	}
	s.channelID = channel
	if revision > s.observedRevision {
		s.observedRevision = revision
	}
	clear(s.speaking)
	s.requestResyncLocked()
	return true
}

func (s *State) SnapshotView() ClientViewState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := s.snapshot.Clone()
	muted, deafened, _ := s.Audio.Snapshot()
	captureAvailable := s.Audio.CaptureAvailable()
	rnnoiseEnabled := s.Audio.RNNoiseEnabled()
	rnnoiseSensitivity := s.Audio.RNNoiseSensitivity()
	microphoneGain := s.Audio.MicrophoneGain()
	vadSettings := s.Audio.VADSnapshot()
	soundPack, soundVolume := s.Audio.SoundSnapshot()
	v := ClientViewState{ConnectionStatus: s.status, ServerInfo: snapshot.Info, Revision: snapshot.Revision,
		Channels: snapshot.Channels, Participants: snapshot.Participants, ScreenStreams: snapshot.ScreenStreams, SessionID: s.sessionID, ChannelID: s.channelID,
		JoinLevel: s.joinLevel, Permissions: s.permissions,
		ChatRevision:  s.chatRevision,
		SnapshotFresh: s.snapshotFresh, Muted: muted, Deafened: deafened, CaptureAvailable: captureAvailable, RNNoiseEnabled: rnnoiseEnabled, RNNoiseSensitivity: rnnoiseSensitivity, MicrophoneGain: microphoneGain,
		VADEnabled: vadSettings.Enabled, VADMode: string(vadSettings.Mode), VADSensitivity: vadSettings.Sensitivity,
		VADOpen: vadSettings.Open, Speaking: make(map[uint64]bool),
		SoundPack: soundPack, SoundVolume: soundVolume, MuteOnAway: s.Audio.MuteOnAway()}
	// The view's local channel belongs to the same snapshot as its participants.
	// A newer Join ACK marks it stale until that revision is synchronized.
	v.ChannelID = 0
	if s.syncedGeneration == s.generation {
		for _, p := range snapshot.Participants {
			if p.SessionID == s.sessionID {
				v.ChannelID = p.ChannelID
				v.UserID = p.UserID
				break
			}
		}
	}
	for id := range s.speaking {
		v.Speaking[id] = true
	}
	return v
}

func (s *State) notifyLocked() {
	for ch := range s.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Only State closes subscriptions; consumers unsubscribe instead of closing.
func (s *State) Subscribe(ctx context.Context) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	ch <- struct{}{}
	s.mu.Unlock()
	unsubscribe := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subscribers[ch]; ok {
			delete(s.subscribers, ch)
			close(ch)
		}
	}
	stop := context.AfterFunc(ctx, unsubscribe)
	return ch, func() { stop(); unsubscribe() }
}

func (s *State) noticeLocked(text string) {
	select {
	case s.notices <- text:
	default:
	} // Console history is deliberately best effort.
}

func (s *State) requestResyncLocked() {
	s.snapshotFresh = false
	select {
	case s.resync <- struct{}{}:
	default:
	}
	s.notifyLocked()
}

func (s *State) RequestResync(generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation == generation {
		s.requestResyncLocked()
	}
}

func (s *State) ApplyEvent(generation uint64, e domain.StateEvent) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.generation {
		return false
	}
	if err := e.Validate(); err != nil {
		s.requestResyncLocked()
		return false
	}
	if e.Revision > s.observedRevision {
		s.observedRevision = e.Revision
	}
	if s.syncedGeneration == generation && e.Revision <= s.snapshot.Revision {
		return false
	}
	if !s.snapshotFresh || s.syncedGeneration != generation || e.Revision != s.snapshot.Revision+1 {
		s.requestResyncLocked()
		return false
	}
	next := s.snapshot.Clone()
	id := e.SessionID
	if e.Kind == domain.ParticipantJoined {
		id = e.Participant.SessionID
	}
	index := -1
	for i, p := range next.Participants {
		if p.SessionID == id {
			index = i
			break
		}
	}
	localChannel := s.channelID
	oldChannel := domain.ChannelID(0)
	if index >= 0 {
		oldChannel = next.Participants[index].ChannelID
	}
	channelName := func(id domain.ChannelID) string {
		for _, c := range next.Channels {
			if c.ID == id {
				return terminalText(c.Name)
			}
		}
		return "Unjoined"
	}
	var line string
	var notification NotificationSoundKind
	var removeVolumeID uint64
	switch e.Kind {
	case domain.ParticipantJoined:
		if index >= 0 || len(next.Participants) >= MaxSnapshotParticipants {
			s.requestResyncLocked()
			return false
		}
		next.Participants = append(next.Participants, e.Participant)
		sort.Slice(next.Participants, func(i, j int) bool { return next.Participants[i].SessionID < next.Participants[j].SessionID })
		line = fmt.Sprintf("+ %s connected (%s)", terminalText(e.Participant.DisplayName), channelName(e.Participant.ChannelID))
		if e.Participant.SessionID != s.sessionID && e.Participant.ChannelID == localChannel {
			notification = NotificationJoined
		}
	case domain.ParticipantLeft:
		if index < 0 {
			s.requestResyncLocked()
			return false
		}
		line = fmt.Sprintf("- %s left", terminalText(next.Participants[index].DisplayName))
		if e.SessionID != s.sessionID && oldChannel == localChannel {
			notification = NotificationLeft
		}
		removeVolumeID = e.SessionID
		next.Participants = append(next.Participants[:index], next.Participants[index+1:]...)
	case domain.ParticipantMoved:
		if index < 0 {
			s.requestResyncLocked()
			return false
		}
		p := &next.Participants[index]
		line = fmt.Sprintf("→ %s moved %s → %s", terminalText(p.DisplayName), channelName(p.ChannelID), channelName(e.ChannelID))
		if e.SessionID == s.sessionID {
			if oldChannel != 0 && e.ChannelID != 0 && oldChannel != e.ChannelID {
				notification = NotificationChannelChanged
			}
		} else if oldChannel != localChannel && e.ChannelID == localChannel {
			notification = NotificationJoined
		} else if oldChannel == localChannel && e.ChannelID != localChannel {
			notification = NotificationLeft
		}
		p.ChannelID = e.ChannelID
	case domain.ScreenStreamStarted:
		if len(next.ScreenStreams) >= MaxSnapshotScreenStreams {
			s.requestResyncLocked()
			return false
		}
		for _, stream := range next.ScreenStreams {
			if stream.ID == e.ScreenStream.ID || stream.OwnerSessionID == e.ScreenStream.OwnerSessionID {
				s.requestResyncLocked()
				return false
			}
		}
		next.ScreenStreams = append(next.ScreenStreams, e.ScreenStream)
		sort.Slice(next.ScreenStreams, func(i, j int) bool { return next.ScreenStreams[i].ID < next.ScreenStreams[j].ID })
		line = fmt.Sprintf("%d started screen sharing", e.ScreenStream.OwnerSessionID)
		if e.ScreenStream.ChannelID == screenChannel(next, s.sessionID, localChannel) {
			notification = NotificationScreenStarted
		}
	case domain.ScreenStreamStopped:
		streamIndex := -1
		for i, stream := range next.ScreenStreams {
			if stream.ID == e.ScreenStream.ID {
				streamIndex = i
				break
			}
		}
		if streamIndex < 0 {
			s.requestResyncLocked()
			return false
		}
		stopped := next.ScreenStreams[streamIndex]
		ownerID := stopped.OwnerSessionID
		next.ScreenStreams = append(next.ScreenStreams[:streamIndex], next.ScreenStreams[streamIndex+1:]...)
		line = fmt.Sprintf("%d stopped screen sharing", ownerID)
		// The owner already played this cue in Service.StopScreen, before the
		// round trip. Playing the event too stacks a second copy on top.
		if ownerID != s.sessionID && stopped.ChannelID == screenChannel(next, s.sessionID, localChannel) {
			notification = NotificationScreenStopped
		}
	case domain.ParticipantAudio:
		if index < 0 {
			s.requestResyncLocked()
			return false
		}
		next.Participants[index].Muted = e.Muted
		next.Participants[index].Deafened = e.Deafened
		name := terminalText(next.Participants[index].DisplayName)
		switch {
		case e.Muted && e.Deafened:
			line = fmt.Sprintf("%s turned microphone and sound off", name)
		case e.Muted:
			line = fmt.Sprintf("%s turned microphone off", name)
		case e.Deafened:
			line = fmt.Sprintf("%s turned sound off", name)
		default:
			line = fmt.Sprintf("%s turned microphone and sound on", name)
		}
	case domain.ParticipantPresence:
		if index < 0 {
			s.requestResyncLocked()
			return false
		}
		p := &next.Participants[index]
		if e.Away {
			line = fmt.Sprintf("%s stepped away", terminalText(p.DisplayName))
		} else {
			line = fmt.Sprintf("%s is back", terminalText(p.DisplayName))
		}
		if p.Away != e.Away && e.SessionID != s.sessionID && p.ChannelID == localChannel && localChannel != 0 {
			if e.Away {
				notification = NotificationAway
			} else {
				notification = NotificationReturned
			}
		}
		p.Away = e.Away
	}
	next.Revision = e.Revision
	if err := validateServerSnapshot(next); err != nil {
		s.requestResyncLocked()
		return false
	}
	s.snapshot = next
	if removeVolumeID != 0 {
		s.Audio.RemoveParticipantVolume(removeVolumeID)
	}
	if notification != 0 {
		s.queueNotificationSoundLocked(notification)
	}
	if e.Kind == domain.ParticipantAudio {
		if e.Muted {
			delete(s.speaking, id)
		}
	} else if id == s.sessionID {
		if e.Kind == domain.ParticipantMoved {
			if s.channelID != e.ChannelID {
				s.measurements.resetIncoming()
			}
			s.channelID = e.ChannelID
		}
		if e.Kind == domain.ParticipantLeft {
			s.measurements.resetIncoming()
			s.channelID = 0
		}
		clear(s.speaking)
	} else {
		if e.Kind == domain.ParticipantMoved || e.Kind == domain.ParticipantLeft {
			s.measurements.resetSender(id)
		}
		delete(s.speaking, id)
	}
	s.noticeLocked(line)
	s.notifyLocked()
	return true
}

func (s *State) audioChanged() {
	muted, _, _ := s.Audio.Snapshot()
	s.mu.Lock()
	defer s.mu.Unlock()
	if muted {
		delete(s.speaking, s.sessionID)
	}
	s.notifyLocked()
}

// Notices are separate from coalesced view notifications: intermediate console
// events can be displayed, but an overloaded console never blocks network work.
func ConsoleStateLoop(ctx context.Context, state *State, output io.Writer) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line := <-state.notices:
			if _, err := fmt.Fprintln(output, line); err != nil {
				return err
			}
		}
	}
}

func (s *State) clearSpeakingAt(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for id, until := range s.speaking {
		if !now.Before(until) {
			delete(s.speaking, id)
			changed = true
		}
	}
	if changed {
		s.notifyLocked()
	}
}
