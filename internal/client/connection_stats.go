package client

import (
	"sync"
	"time"
)

const statsWindow = 30 * time.Second
const maxStatsPackets = 4096

type ConnectionStats struct {
	SessionID              uint64
	Generation             uint64
	Status                 ConnectionStatus
	PingMS                 float64
	PingVariationMS        float64
	PingAvailable          bool
	PingVariationAvailable bool
	PingSampleAtMS         int64
	IncomingLoss           float64
	IncomingKnown          bool
	IncomingSampleAtMS     int64
	OutgoingLoss           float64
	OutgoingKnown          bool
}

type voiceArrival struct {
	at   time.Time
	lost bool
}

type voiceSequence struct {
	last     uint32
	started  bool
	lastSeen time.Time
	missing  map[uint32]time.Time
}

type connectionMeasurements struct {
	mu               sync.Mutex
	pings            []float64
	pingAt           time.Time
	voiceAt          time.Time
	arrivals         []voiceArrival
	senders          map[uint64]*voiceSequence
	outgoingLoss     float64
	outgoingKnown    bool
	sentVoiceAt      []time.Time
	lastSentSequence uint32
}

func (m *connectionMeasurements) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pings = nil
	m.pingAt = time.Time{}
	m.voiceAt = time.Time{}
	m.arrivals = nil
	m.senders = nil
	m.outgoingLoss = 0
	m.outgoingKnown = false
	m.sentVoiceAt = nil
	m.lastSentSequence = 0
}

func (m *connectionMeasurements) resetIncoming() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.arrivals = nil
	m.senders = nil
	m.voiceAt = time.Time{}
}

func (m *connectionMeasurements) resetSender(sender uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.senders, sender)
}

func (m *connectionMeasurements) recordPing(elapsed time.Duration, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pings = append(m.pings, float64(elapsed)/float64(time.Millisecond))
	if len(m.pings) > 8 {
		m.pings = m.pings[len(m.pings)-8:]
	}
	m.pingAt = at
}

func (m *connectionMeasurements) recordOutgoingLoss(value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outgoingLoss = value
	m.outgoingKnown = true
}

func (m *connectionMeasurements) clearOutgoingLoss() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outgoingKnown = false
	m.outgoingLoss = 0
}

func (m *connectionMeasurements) recordVoiceSent(sequence uint32, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trimSent(at)
	m.sentVoiceAt = append(m.sentVoiceAt, at)
	m.lastSentSequence = sequence
	if len(m.sentVoiceAt) > maxStatsPackets {
		m.sentVoiceAt = m.sentVoiceAt[len(m.sentVoiceAt)-maxStatsPackets:]
	}
}

func (m *connectionMeasurements) sentInWindow(now time.Time) (uint16, uint16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trimSent(now)
	return uint16(len(m.sentVoiceAt)), uint16(m.lastSentSequence)
}

func (m *connectionMeasurements) trimSent(now time.Time) {
	cutoff := now.Add(-statsWindow)
	i := 0
	for i < len(m.sentVoiceAt) && m.sentVoiceAt[i].Before(cutoff) {
		i++
	}
	m.sentVoiceAt = m.sentVoiceAt[i:]
}

func (m *connectionMeasurements) recordVoice(sender uint64, sequence uint32, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trim(at)
	m.voiceAt = at
	if m.senders == nil {
		m.senders = make(map[uint64]*voiceSequence)
	}
	stream := m.senders[sender]
	if stream == nil {
		stream = &voiceSequence{missing: make(map[uint32]time.Time)}
		m.senders[sender] = stream
	}
	if !stream.started {
		stream.started = true
		stream.last = sequence
		stream.lastSeen = at
		m.arrivals = append(m.arrivals, voiceArrival{at: at})
		return
	}
	if missingAt, ok := stream.missing[sequence]; ok {
		delete(stream.missing, sequence)
		stream.lastSeen = at
		for i := range m.arrivals {
			if m.arrivals[i].lost && m.arrivals[i].at.Equal(missingAt) {
				m.arrivals[i].lost = false
				return
			}
		}
		return
	}
	if !sequenceBefore(stream.last, sequence) {
		return // duplicate or old packet
	}
	gap := sequence - stream.last - 1
	if gap > 1024 { // discontinuity: avoid treating a restarted stream as massive loss
		stream.missing = make(map[uint32]time.Time)
		gap = 0
	}
	for i := uint32(1); i <= gap; i++ {
		missing := stream.last + i
		stream.missing[missing] = at
		m.arrivals = append(m.arrivals, voiceArrival{at: at, lost: true})
	}
	stream.last = sequence
	stream.lastSeen = at
	m.arrivals = append(m.arrivals, voiceArrival{at: at})
}

func (m *connectionMeasurements) trim(now time.Time) {
	cutoff := now.Add(-statsWindow)
	i := 0
	for i < len(m.arrivals) && m.arrivals[i].at.Before(cutoff) {
		i++
	}
	m.arrivals = m.arrivals[i:]
	for id, stream := range m.senders {
		for sequence, at := range stream.missing {
			if at.Before(cutoff) {
				delete(stream.missing, sequence)
			}
		}
		if len(stream.missing) == 0 && stream.lastSeen.Before(cutoff) {
			delete(m.senders, id)
		}
	}
}

func (m *connectionMeasurements) snapshot(now time.Time) ConnectionStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trim(now)
	var result ConnectionStats
	result.OutgoingLoss = m.outgoingLoss
	result.OutgoingKnown = m.outgoingKnown
	if !m.pingAt.IsZero() && now.Sub(m.pingAt) <= 2*HeartbeatInterval && len(m.pings) > 0 {
		result.PingAvailable = true
		result.PingSampleAtMS = m.pingAt.UnixMilli()
		result.PingMS = m.pings[len(m.pings)-1]
		if len(m.pings) > 1 {
			result.PingVariationAvailable = true
			var sum float64
			for i := 1; i < len(m.pings); i++ {
				delta := m.pings[i] - m.pings[i-1]
				if delta < 0 {
					delta = -delta
				}
				sum += delta
			}
			result.PingVariationMS = sum / float64(len(m.pings)-1)
		}
	}
	if len(m.arrivals) > 0 {
		result.IncomingSampleAtMS = m.voiceAt.UnixMilli()
		var lost int
		for _, arrival := range m.arrivals {
			if arrival.lost {
				lost++
			}
		}
		result.IncomingKnown = true
		result.IncomingLoss = float64(lost) * 100 / float64(len(m.arrivals))
	}
	return result
}

func (s *State) ConnectionStats() ConnectionStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := s.measurements.snapshot(time.Now())
	result.SessionID = s.sessionID
	result.Generation = s.generation
	result.Status = s.status
	if s.status != ConnectionConnected {
		result.PingAvailable = false
		result.PingVariationAvailable = false
		result.IncomingKnown = false
		result.OutgoingKnown = false
	}
	return result
}

func (s *State) RecordVoiceArrival(sender uint64, sequence uint32) {
	s.measurements.recordVoice(sender, sequence, time.Now())
}

func (s *State) RecordVoiceSent(sequence uint32) {
	s.measurements.recordVoiceSent(sequence, time.Now())
}

func (s *State) VoiceSentInWindow() (uint16, uint16) {
	return s.measurements.sentInWindow(time.Now())
}
