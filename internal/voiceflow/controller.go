package voiceflow

import (
	"sort"
	"sync"
	"time"
	"uniclog.io/sonoryx/internal/protocol"
)

type rttPoint struct {
	value float64
	at    time.Time
}
type controller struct {
	lastRTTAt                                       time.Time
	enabled                                         bool
	desired, allowed                                byte
	reason                                          byte
	holdUntil, lastFresh, lastEvaluation, limitedAt time.Time
	good, recoveryGood                              time.Duration
	lastID                                          uint32
	started                                         bool
	bad                                             int
	rttID                                           uint32
	rttStarted                                      bool
	rttHistory                                      []rttPoint
	ewma                                            float64
	rttHigh                                         int
}

func (c *controller) overload(now time.Time) {
	if !c.enabled {
		return
	}
	c.allowed = 1
	c.reason = protocol.VoiceRestrictionCongestion
	c.good = 0
	c.recoveryGood = 0
	if c.limitedAt.IsZero() {
		c.limitedAt = now
	}
}
func (c *controller) rtt(sample protocol.VoiceRTTSample, now time.Time) {
	if !sample.Valid || sample.AgeMS > 10000 || (c.rttStarted && int32(sample.ID-c.rttID) <= 0) {
		return
	}
	c.rttID = sample.ID
	c.rttStarted = true
	if !c.lastRTTAt.IsZero() && now.Sub(c.lastRTTAt) > 10*time.Second {
		c.ewma = 0
		c.rttHigh = 0
	}
	c.lastRTTAt = now.Add(-time.Duration(sample.AgeMS) * time.Millisecond)
	value := float64(sample.Micros) / 1000
	if c.ewma == 0 {
		c.ewma = value
	} else {
		c.ewma = .8*c.ewma + .2*value
	}
	c.rttHistory = append(c.rttHistory, rttPoint{value, now.Add(-time.Duration(sample.AgeMS) * time.Millisecond)})
	kept := c.rttHistory[:0]
	baseline := value
	for _, point := range c.rttHistory {
		if now.Sub(point.at) <= 60*time.Second {
			kept = append(kept, point)
			baseline = min(baseline, point.value)
		}
	}
	if len(kept) > 128 {
		kept = kept[len(kept)-128:]
	}
	c.rttHistory = kept
	if c.ewma-baseline >= max(30, baseline*.5) {
		c.rttHigh++
		if c.rttHigh >= 2 {
			c.overload(now)
		}
	} else {
		c.rttHigh = 0
	}
}
func (c *controller) evaluate(e *Endpoint, now time.Time) {
	if !c.enabled {
		return
	}
	latest := -1
	for i := len(e.sent) - 1; i >= 0; i-- {
		if e.sent[i].report != nil && e.sent[i].eligible && now.Sub(e.sent[i].closed) <= 3*time.Second {
			latest = i
			break
		}
	}
	if latest < 0 {
		return
	}
	record := e.sent[latest]
	if c.started && int32(record.interval.ID-c.lastID) <= 0 {
		return
	}
	step := time.Duration(record.interval.ActiveMS) * time.Millisecond
	if !c.lastEvaluation.IsZero() {
		step = min(step, max(time.Duration(0), now.Sub(c.lastEvaluation)))
	}
	c.lastEvaluation = now
	c.lastID = record.interval.ID
	c.started = true
	c.lastFresh = now
	var expected, received, bursts uint64
	complete := true
	limit := 5
	if e.sent[0].interval.ID == 1 && record.interval.ID < 5 {
		limit = int(record.interval.ID)
	}
	for offset := 0; offset < limit; offset++ {
		found := false
		for _, r := range e.sent {
			if r.interval.ID != record.interval.ID-uint32(offset) {
				continue
			}
			found = true
			if r.report == nil || !r.eligible || r.report.Flags&protocol.VoiceIntervalComplete == 0 {
				complete = false
				break
			}
			expected += uint64(r.interval.SuccessfulSends)
			received += uint64(r.report.ReceivedUnique)
			if r.report.Flags&protocol.VoiceBurstValid != 0 {
				bursts += uint64(r.report.NewMultipleBursts)
			}
			break
		}
		if !found {
			complete = false
		}
	}
	loss := 0.0
	if expected > 0 {
		loss = float64(expected-received) * 100 / float64(expected)
	}
	if complete && expected >= 100 && loss > 3 {
		c.bad++
	} else {
		c.bad = 0
	}
	if c.desired == 2 && (c.bad >= 2 || bursts >= 2) {
		c.desired = 3
		c.holdUntil = now.Add(20 * time.Second)
		if c.reason == protocol.VoiceRestrictionNone {
			c.allowed = 3
		}
		c.good = 0
	}
	if record.interval.ActiveMS == 0 {
		return
	}
	if !complete || expected < 100 {
		c.good = 0
		c.recoveryGood = 0
		return
	}
	if loss < 1 && bursts == 0 {
		c.good += step
	} else {
		c.good = 0
	}
	if c.desired == 3 && !now.Before(c.holdUntil) && c.good >= 20*time.Second {
		c.desired = 2
		c.allowed = min(c.allowed, 2)
		c.good = 0
	}
	effectiveRate := e.budgetRate
	if e.haveGlobalShare {
		effectiveRate = min(effectiveRate, e.globalShare)
	}
	needed := e.lastReserveDemand[min(c.desired, c.allowed+1)]
	if c.reason != protocol.VoiceRestrictionNone && record.interval.LocalSendFailures == 0 && c.rttHigh < 2 && needed <= effectiveRate {
		c.recoveryGood += step
		if c.recoveryGood >= 10*time.Second {
			c.allowed = min(c.desired, c.allowed+1)
			c.recoveryGood = 0
			if c.allowed == c.desired {
				c.reason = protocol.VoiceRestrictionNone
				c.limitedAt = time.Time{}
			}
		}
	} else {
		c.recoveryGood = 0
	}
}
func (c *controller) refresh(e *Endpoint, now time.Time) {
	if !c.lastRTTAt.IsZero() && now.Sub(c.lastRTTAt) > 10*time.Second {
		c.rttHigh = 0
	}
	if !c.lastFresh.IsZero() && now.Sub(c.lastFresh) > 3*time.Second && (e.activeSlots != 0 || (len(e.sent) > 0 && e.sent[len(e.sent)-1].interval.ActiveMS > 0)) {
		c.good = 0
		c.recoveryGood = 0
	}
	mode := protocol.VoiceMode{DesiredFrames: c.desired, AllowedFrames: c.allowed, Reason: c.reason}
	if !c.enabled {
		mode.DesiredFrames = 1
		mode.AllowedFrames = 1
		mode.Reason = protocol.VoiceRestrictionDisabled
	}
	mode.Revision = e.mode.Revision
	if mode != e.mode {
		mode.Revision++
		e.mode = mode
	}
}
func (e *Endpoint) SetEnabled(enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if enabled == e.controller.enabled {
		return
	}
	e.controller = controller{enabled: enabled, desired: 2, allowed: 2}
	e.controller.refresh(e, time.Now())
}
func (e *Endpoint) FrameCount(now time.Time) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.controller.refresh(e, now)
	return int(e.mode.DesiredFrames)
}
func (e *Endpoint) QueueDrop(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.drops.Queue++
	e.advance(now)
	slot := int(now.Sub(e.start) / (20 * time.Millisecond))
	if slot >= 0 && slot < 50 {
		e.activeSlots |= 1 << slot
	}
	e.controller.overload(now)
	e.controller.refresh(e, now)
}
func (e *Endpoint) ProtocolDrop() { e.mu.Lock(); defer e.mu.Unlock(); e.drops.Protocol++ }

type bucket struct {
	at          time.Time
	tokens      float64
	initialized bool
}

func (b *bucket) available(now time.Time, rate int) float64 {
	burst := max(2500, float64(rate)*.2)
	if !b.initialized {
		b.initialized = true
		b.at = now
		b.tokens = burst
	}
	b.tokens = min(burst, b.tokens+max(0, now.Sub(b.at).Seconds())*float64(rate))
	b.at = now
	return b.tokens
}
func (b *bucket) spend(now time.Time, rate, cost int) bool {
	if cost < 0 || b.available(now, rate) < float64(cost) {
		return false
	}
	b.tokens -= float64(cost)
	return true
}
func (e *Endpoint) SetBudget(bytesPerSecond int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.budgetRate = max(1, bytesPerSecond)
}
func (e *Endpoint) Select(frames []protocol.VoiceBundleFrame, primary bool, now time.Time) []protocol.VoiceBundleFrame {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.advance(now)
	e.controller.refresh(e, now)
	if len(frames) == 0 {
		return nil
	}
	if !primary {
		if !e.budget.spend(now, e.budgetRate, 50+protocol.VoiceBundleV2Size(frames)) {
			e.drops.RepairBudget++
			e.limitBudget(1, now)
			return nil
		}
		return frames
	}
	for count := 2; count <= int(e.mode.DesiredFrames); count++ {
		cost := 0
		for _, f := range frames[max(0, len(frames)-count) : len(frames)-1] {
			cost += 6 + len(f.Payload)
		}
		e.reserveDemand[count] += cost
	}
	for len(frames) > int(e.mode.AllowedFrames) {
		frames = frames[1:]
	}
	for len(frames) > 1 {
		cost := 0
		for _, f := range frames[:len(frames)-1] {
			cost += 6 + len(f.Payload)
		}
		if e.budget.spend(now, e.budgetRate, cost) {
			break
		}
		frames = frames[1:]
		e.drops.Budget++
		e.limitBudget(byte(len(frames)), now)
	}
	e.actual = byte(len(frames))
	return frames
}
func (e *Endpoint) limitBudget(allowed byte, now time.Time) {
	c := &e.controller
	if !c.enabled {
		return
	}
	c.allowed = min(c.allowed, allowed)
	if c.reason != protocol.VoiceRestrictionCongestion {
		c.reason = protocol.VoiceRestrictionBudget
	}
	c.recoveryGood = 0
	if c.limitedAt.IsZero() {
		c.limitedAt = now
	}
	c.refresh(e, now)
}
func (e *Endpoint) GlobalDrop(primary bool, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if primary {
		e.drops.Budget++
	} else {
		e.drops.RepairBudget++
	}
	e.limitBudget(1, now)
}

type poolMember struct {
	budget        bucket
	last          time.Time
	demand, share int
}

// Pool returns unused demand to the other active recipients with max-min fair
// quotas recomputed once per second. The global bucket also covers joins.
type Pool struct {
	mu        sync.Mutex
	rate      int
	global    bucket
	members   map[uint64]*poolMember
	lastSweep time.Time
}

func NewPool(rate int) *Pool { return &Pool{rate: max(1, rate), members: make(map[uint64]*poolMember)} }
func (p *Pool) member(id uint64, demand int, now time.Time) *poolMember {
	m := p.members[id]
	if m == nil {
		if demand < 0 {
			demand = p.rate
		}
		m = &poolMember{demand: max(1, demand)}
		p.members[id] = m
		m.share = min(m.demand, p.rate/max(1, len(p.members)))
	}
	m.last = now
	if demand >= 0 {
		m.demand = demand
	}
	if p.lastSweep.IsZero() || now.Sub(p.lastSweep) >= time.Second {
		var active []*poolMember
		for id, member := range p.members {
			if now.Sub(member.last) > time.Second {
				delete(p.members, id)
			} else {
				active = append(active, member)
			}
		}
		sort.Slice(active, func(i, j int) bool { return active[i].demand < active[j].demand })
		remaining := p.rate
		for i, member := range active {
			member.share = min(member.demand, remaining/max(1, len(active)-i))
			remaining -= member.share
		}
		p.lastSweep = now
	}
	return m
}
func (p *Pool) Share(id uint64, demand int, now time.Time) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.member(id, demand, now).share
}
func (p *Pool) Allow(id uint64, cost int, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := p.member(id, -1, now)
	if m.share <= 0 || m.budget.available(now, m.share) < float64(cost) || p.global.available(now, p.rate) < float64(cost) {
		return false
	}
	m.budget.tokens -= float64(cost)
	p.global.tokens -= float64(cost)
	return true
}
func (e *Endpoint) ReserveDemand() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	need := e.lastReserveDemand[e.mode.DesiredFrames]
	if need == 0 {
		need = e.budgetRate
	}
	return min(e.budgetRate, need)
}
func (e *Endpoint) SetGlobalShare(rate int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.globalShare = max(0, rate)
	e.haveGlobalShare = true
}
func (e *Endpoint) SizeDrops(count uint64) {
	if count == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.drops.Size += count
}
