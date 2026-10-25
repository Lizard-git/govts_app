package voiceflow

import (
	"errors"
	"math/bits"
	"sync"
	"time"
	"uniclog.io/sonoryx/internal/protocol"
)

const receiveWindow = 65536

var ErrTelemetry = errors.New("inconsistent voice telemetry")

type receiveBits struct {
	bits        [receiveWindow / 64]uint64
	tags        [receiveWindow / 64]uint64
	metrics     [receiveWindow]uint16
	high        uint32
	absolute    uint64
	initialized bool
}

func (b *receiveBits) anchor(seq uint32) {
	if !b.initialized {
		b.initialized = true
		b.high = seq
		b.absolute = 1<<32 + uint64(seq)
	} else if delta := int32(seq - b.high); delta > 0 {
		b.absolute += uint64(delta)
		b.high = seq
	}
}
func (b *receiveBits) observe(seq uint32) bool {
	b.anchor(seq)
	distance := uint32(b.high - seq)
	if int32(b.high-seq) < 0 || distance >= receiveWindow {
		return false
	}
	absolute := b.absolute - uint64(distance)
	word := absolute / 64
	index := word % (receiveWindow / 64)
	if b.tags[index] != word+1 {
		b.tags[index] = word + 1
		b.bits[index] = 0
		clear(b.metrics[index*64 : index*64+64])
	}
	mask := uint64(1) << (seq % 64)
	if b.bits[index]&mask != 0 {
		return false
	}
	b.bits[index] |= mask
	return true
}
func (b *receiveBits) has(seq uint32) bool {
	if !b.initialized || int32(b.high-seq) < 0 || uint32(b.high-seq) >= receiveWindow {
		return false
	}
	word := (b.absolute - uint64(uint32(b.high-seq))) / 64
	index := word % (receiveWindow / 64)
	return b.tags[index] == word+1 && b.bits[index]&(uint64(1)<<(seq%64)) != 0
}

type sentRecord struct {
	eligible bool
	interval protocol.VoiceSendInterval
	closed   time.Time
	report   *protocol.VoiceReceiveInterval
}
type receivedRecord struct {
	interval protocol.VoiceSendInterval
	deadline time.Time
	report   *protocol.VoiceReceiveInterval
}
type Rate struct {
	tokens float64
	at     time.Time
}

func (r *Rate) Allow(now time.Time) bool {
	if r.at.IsZero() {
		r.at = now
		r.tokens = 4
	}
	r.tokens = min(4, r.tokens+max(0, now.Sub(r.at).Seconds())*2)
	r.at = now
	if r.tokens < 1 {
		return false
	}
	r.tokens--
	return true
}

// Endpoint owns a session's two independent directions. It survives audio
// epoch transitions; a new authenticated session gets a new endpoint.
// There is no per-datagram timestamp or unbounded history.
type Endpoint struct {
	receivedCopies                   uint64
	reserveDemand, lastReserveDemand [4]int
	globalShare                      int
	haveGlobalShare                  bool
	remoteLimitedAt                  time.Time
	receivedFrames                   byte
	mu                               sync.Mutex
	direction                        byte
	start                            time.Time
	current                          protocol.VoiceSendInterval
	activeSlots                      uint64
	sent                             []sentRecord
	received                         []receivedRecord
	bitmap                           receiveBits
	recvNext                         uint32
	recvStarted                      bool
	burst                            uint32
	burstBad                         bool
	outUI, inUI                      []timedInterval
	summaryRate, reportRate          Rate
	lastSummary, lastReport          time.Time
	remoteMode                       protocol.VoiceMode
	remoteModeAt                     time.Time
	remoteLatest                     uint32
	remoteStarted                    bool
	mode                             protocol.VoiceMode
	rtt                              protocol.VoiceRTTSample
	rttAt                            time.Time
	controller                       controller
	budget                           bucket
	budgetRate                       int
	actual                           byte
	drops                            DropStats
}
type timedInterval struct {
	send protocol.VoiceSendInterval
	recv protocol.VoiceReceiveInterval
	at   time.Time
}
type DropStats struct{ Size, Budget, RepairBudget, Protocol, Queue, Write uint64 }
type DirectionStats struct {
	Known               bool
	Loss                float64
	Restored, Late, PLC uint64
}
type Snapshot struct {
	ReceivedCopies       uint64
	RemoteLimited        bool
	RemoteActual         byte
	Incoming, Outgoing   DirectionStats
	Local, Remote        protocol.VoiceMode
	RemoteKnown, Limited bool
	Actual               byte
	Drops                DropStats
}

func NewEndpoint(direction byte, now time.Time) *Endpoint {
	return &Endpoint{direction: direction, start: now, current: protocol.VoiceSendInterval{ID: 1}, mode: protocol.VoiceMode{DesiredFrames: 1, AllowedFrames: 1, Reason: protocol.VoiceRestrictionDisabled}, budgetRate: 128 * 1024}
}
func (e *Endpoint) advance(now time.Time) {
	steps := int(now.Sub(e.start) / time.Second)
	if steps <= 0 {
		return
	}
	if steps > 30 {
		e.current = protocol.VoiceSendInterval{ID: e.current.ID + uint32(steps-30)}
		e.activeSlots = 0
		e.start = e.start.Add(time.Duration(steps-30) * time.Second)
		steps = 30
	}
	for range steps {
		e.lastReserveDemand = e.reserveDemand
		e.reserveDemand = [4]int{}
		e.current.ActiveMS = uint16(bits.OnesCount64(e.activeSlots) * 20)
		e.sent = append(e.sent, sentRecord{interval: e.current, closed: e.start.Add(time.Second)})
		e.current = protocol.VoiceSendInterval{ID: e.current.ID + 1}
		e.activeSlots = 0
		e.start = e.start.Add(time.Second)
	}
	if len(e.sent) > 10 {
		e.sent = append([]sentRecord(nil), e.sent[len(e.sent)-10:]...)
	}
	e.closeReceived(now)
}
func (e *Endpoint) Sent(seq uint32, success, primary bool, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.advance(now)
	if e.current.SuccessfulSends+e.current.LocalSendFailures == 0 {
		e.current.FirstSequence = seq
	}
	e.current.LastSequence = seq
	if success {
		e.current.SuccessfulSends++
	} else {
		e.current.LocalSendFailures++
		e.drops.Write++
		e.controller.overload(now)
	}
	if primary {
		slot := int(now.Sub(e.start) / (20 * time.Millisecond))
		if slot >= 0 && slot < 50 {
			e.activeSlots |= 1 << slot
		}
	}
}
func (e *Endpoint) Receive(seq uint32, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.bitmap.observe(seq)
}

// kind: 0 restored unique, 1 late, 2 PLC. Counts are bounded by three frames
// per datagram; a bitmap slot is cleared before its sequence is reused.
func (e *Endpoint) Frame(seq uint32, kind byte, count uint32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if kind > 2 || !e.bitmap.has(seq) {
		return
	}
	slot := &e.bitmap.metrics[seq%receiveWindow]
	shift := kind * 2
	mask := uint16(3)
	if kind == 2 {
		mask = 4095
	}
	old := (*slot >> shift) & mask
	*slot = (*slot &^ (mask << shift)) | (uint16(min(uint32(old)+count, uint32(mask))) << shift)
}

func (e *Endpoint) AcceptSummary(summary protocol.VoiceSummary, now time.Time) error {
	if _, err := protocol.EncodeVoiceSummary(summary); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if summary.Direction == e.direction || !e.summaryRate.Allow(now) {
		return ErrTelemetry
	}
	for _, s := range summary.Intervals {
		if uint64(s.SuccessfulSends)+uint64(s.LocalSendFailures) > receiveWindow {
			return ErrTelemetry
		}
	}
	latest := summary.Intervals[len(summary.Intervals)-1].ID
	if e.remoteStarted && summary.Mode.Revision == e.remoteMode.Revision && summary.Mode != e.remoteMode {
		return ErrTelemetry
	}
	if e.recvStarted && int32(latest-e.recvNext) < -30 {
		return nil
	}
	// Check every duplicate before mutating the pending assembly.
	for _, s := range summary.Intervals {
		for _, old := range e.received {
			if old.interval.ID == s.ID && old.interval != s {
				return ErrTelemetry
			}
		}
	}
	if !e.recvStarted {
		e.recvNext = summary.Intervals[0].ID
		e.recvStarted = true
	}
	if int32(latest-e.recvNext) > 9 {
		e.recvNext = latest - 9
		e.burst = 0
		e.burstBad = true
	}
	for _, s := range summary.Intervals {
		if int32(s.ID-e.recvNext) < 0 || int32(latest-s.ID) > 9 {
			continue
		}
		found := false
		for _, old := range e.received {
			if old.interval.ID == s.ID {
				found = true
				break
			}
		}
		if !found {
			e.received = append(e.received, receivedRecord{interval: s, deadline: now.Add(100 * time.Millisecond)})
		}
	}
	if !e.remoteStarted || int32(latest-e.remoteLatest) > 0 {
		if !e.remoteStarted || int32(summary.Mode.Revision-e.remoteMode.Revision) >= 0 {
			if summary.Mode.AllowedFrames < summary.Mode.DesiredFrames {
				if e.remoteLimitedAt.IsZero() {
					e.remoteLimitedAt = now
				}
			} else {
				e.remoteLimitedAt = time.Time{}
			}
			e.remoteMode = summary.Mode
			e.remoteModeAt = now
		}
		e.remoteLatest = latest
		e.remoteStarted = true
	}
	high := latest
	if e.remoteStarted && int32(e.remoteLatest-high) > 0 {
		high = e.remoteLatest
	}
	kept := e.received[:0]
	for _, record := range e.received {
		if int32(high-record.interval.ID) >= 0 && high-record.interval.ID < 10 {
			kept = append(kept, record)
		}
	}
	e.received = kept
	for _, record := range summary.Intervals {
		if record.SuccessfulSends+record.LocalSendFailures > 0 {
			e.bitmap.anchor(record.LastSequence)
		}
	}
	e.closeReceived(now)
	return nil
}

func (e *Endpoint) closeReceived(now time.Time) {
	for range 10 {
		index := -1
		for i := range e.received {
			if e.received[i].interval.ID == e.recvNext {
				index = i
				break
			}
		}
		if index < 0 || now.Before(e.received[index].deadline) {
			break
		}
		r := &e.received[index]
		s := r.interval
		if r.report != nil {
			e.recvNext++
			continue
		}
		report := protocol.VoiceReceiveInterval{ID: s.ID, FirstSequence: s.FirstSequence, LastSequence: s.LastSequence, Flags: protocol.VoiceIntervalComplete | protocol.VoiceBurstValid}
		attempts := s.SuccessfulSends + s.LocalSendFailures
		if e.burstBad {
			report.Flags &= ^protocol.VoiceBurstValid
		}
		if e.bitmap.initialized && attempts > 0 && int32(e.bitmap.high-s.FirstSequence) >= receiveWindow {
			report.Flags = 0
			e.burst = 0
			e.burstBad = true
		} else {
			if s.LocalSendFailures > 0 {
				e.burstBad = true
				report.Flags &= ^protocol.VoiceBurstValid
			}
			for i := uint32(0); i < attempts; i++ {
				seq := s.FirstSequence + i
				if e.bitmap.has(seq) {
					report.ReceivedUnique++
					if e.burst > 0 && !e.burstBad {
						report.LongestBurst = max(report.LongestBurst, e.burst)
						if e.burst >= 2 {
							report.NewMultipleBursts++
						}
					}
					e.burst = 0
					e.burstBad = s.LocalSendFailures > 0
					m := e.bitmap.metrics[seq%receiveWindow]
					report.RestoredUnique += uint32(m & 3)
					report.LateFrames += uint32((m >> 2) & 3)
					report.PLCFrames += uint32((m >> 4) & 4095)
				} else {
					e.burst = min(receiveWindow, e.burst+1)
				}
			}
			if e.burstBad || report.Flags&protocol.VoiceBurstValid == 0 {
				report.Flags &= ^protocol.VoiceBurstValid
				report.LongestBurst = 0
				report.NewMultipleBursts = 0
			}
		}
		if e.direction == protocol.VoiceDownstream {
			report.PLCFrames = 0
		}
		// Local failures cannot have arrived. An inconsistent claim cannot
		// create a negative loss or influence the regulator.
		if report.ReceivedUnique > s.SuccessfulSends {
			report.Flags = 0
			report.NewMultipleBursts = 0
			report.LongestBurst = 0
		}
		r.report = &report
		e.inUI = appendUI(e.inUI, timedInterval{s, report, now})
		e.recvNext++
	}
	kept := e.received[:0]
	for _, r := range e.received {
		if int32(e.recvNext-r.interval.ID) <= 10 {
			kept = append(kept, r)
		}
	}
	e.received = kept
}
func appendUI(list []timedInterval, item timedInterval) []timedInterval {
	for _, old := range list {
		if old.send.ID == item.send.ID {
			return list
		}
	}
	list = append(list, item)
	if len(list) > 30 {
		list = append([]timedInterval(nil), list[len(list)-30:]...)
	}
	return list
}

func (e *Endpoint) AcceptReport(report protocol.VoiceReport, now time.Time) error {
	if _, err := protocol.EncodeVoiceReport(report); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.advance(now)
	if report.Direction != e.direction || !e.reportRate.Allow(now) {
		return ErrTelemetry
	}
	for _, r := range report.Intervals {
		var record *sentRecord
		for i := range e.sent {
			if e.sent[i].interval.ID == r.ID {
				record = &e.sent[i]
				break
			}
		}
		if record == nil {
			if int32(r.ID-e.current.ID) >= 0 {
				return ErrTelemetry
			}
			continue
		}
		s := record.interval
		attempts := s.SuccessfulSends + s.LocalSendFailures
		if r.FirstSequence != s.FirstSequence || r.LastSequence != s.LastSequence || r.ReceivedUnique > s.SuccessfulSends || uint64(r.RestoredUnique) > uint64(attempts)*3 || uint64(r.LateFrames) > uint64(attempts)*3 || uint64(r.PLCFrames) > uint64(attempts)*4095 || r.NewMultipleBursts > attempts/2+1 || r.LongestBurst > receiveWindow || (s.LocalSendFailures > 0 && r.Flags&protocol.VoiceBurstValid != 0) {
			return ErrTelemetry
		}
		if record.report != nil && *record.report != r {
			return ErrTelemetry
		}
	}
	for _, r := range report.Intervals {
		for i := range e.sent {
			record := &e.sent[i]
			if record.interval.ID == r.ID && record.report == nil {
				copy := r
				record.report = &copy
				record.eligible = now.Sub(record.closed) <= 3*time.Second
				e.outUI = appendUI(e.outUI, timedInterval{record.interval, copy, record.closed})
				break
			}
		}
	}
	e.controller.evaluate(e, now)
	if report.RTT.Valid {
		e.controller.rtt(report.RTT, now)
	}
	e.controller.refresh(e, now)
	return nil
}

func (e *Endpoint) Tick(now time.Time) (*protocol.VoiceSummary, *protocol.VoiceReport) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.advance(now)
	e.closeReceived(now)
	e.controller.refresh(e, now)
	var summary *protocol.VoiceSummary
	var report *protocol.VoiceReport
	if len(e.sent) > 0 && (e.lastSummary.IsZero() || now.Sub(e.lastSummary) >= time.Second) {
		s := protocol.VoiceSummary{Direction: e.direction, Mode: e.mode}
		for _, r := range e.sent[max(0, len(e.sent)-5):] {
			s.Intervals = append(s.Intervals, r.interval)
		}
		summary = &s
		e.lastSummary = now
	}
	if e.lastReport.IsZero() || now.Sub(e.lastReport) >= time.Second {
		r := protocol.VoiceReport{Direction: 1 - e.direction}
		for n := uint32(5); n > 0; n-- {
			id := e.recvNext - n
			for _, record := range e.received {
				if record.interval.ID == id && record.report != nil {
					r.Intervals = append(r.Intervals, *record.report)
					break
				}
			}
		}
		if len(r.Intervals) > 0 {
			if e.rtt.Valid && now.Sub(e.rttAt) <= 10*time.Second {
				r.RTT = e.rtt
				r.RTT.AgeMS = uint32(now.Sub(e.rttAt) / time.Millisecond)
			}
			report = &r
			e.lastReport = now
		}
	}
	return summary, report
}
func (e *Endpoint) RTT(sample protocol.VoiceRTTSample, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rtt = sample
	e.rttAt = now
	e.controller.rtt(sample, now)
}
func directionStats(list []timedInterval, now time.Time) DirectionStats {
	var out DirectionStats
	var expected, received uint64
	for _, r := range list {
		if now.Sub(r.at) > 30*time.Second || r.recv.Flags&protocol.VoiceIntervalComplete == 0 {
			continue
		}
		expected += uint64(r.send.SuccessfulSends)
		received += uint64(r.recv.ReceivedUnique)
		out.Restored += uint64(r.recv.RestoredUnique)
		out.Late += uint64(r.recv.LateFrames)
		out.PLC += uint64(r.recv.PLCFrames)

	}
	if expected > 0 && received <= expected {
		out.Known = true
		out.Loss = float64(expected-received) * 100 / float64(expected)
	}
	return out
}
func (e *Endpoint) Stats(now time.Time) Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return Snapshot{ReceivedCopies: e.receivedCopies, RemoteLimited: !e.remoteLimitedAt.IsZero() && now.Sub(e.remoteLimitedAt) >= 3*time.Second, RemoteActual: e.receivedFrames, Incoming: directionStats(e.inUI, now), Outgoing: directionStats(e.outUI, now), Local: e.mode, Remote: e.remoteMode, RemoteKnown: e.remoteStarted && now.Sub(e.remoteModeAt) <= 3*time.Second, Limited: !e.controller.limitedAt.IsZero() && now.Sub(e.controller.limitedAt) >= 3*time.Second, Actual: e.actual, Drops: e.drops}
}

func (e *Endpoint) ReceivedFrames(count byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.receivedFrames = count
	if count > 1 {
		e.receivedCopies += uint64(count - 1)
	}
}
func (e *Endpoint) ActualFrames(count byte) { e.mu.Lock(); defer e.mu.Unlock(); e.actual = count }
