package voiceflow

import (
	"math"
	"testing"
	"time"
	"uniclog.io/sonoryx/internal/protocol"
)

func TestTelemetryCountsMissingHeadTailAndAllVoice(t *testing.T) {
	for _, received := range [][]uint32{{2}, {}} {
		now := time.Unix(100, 0)
		send := NewEndpoint(protocol.VoiceUpstream, now)
		recv := NewEndpoint(protocol.VoiceDownstream, now)
		for seq := uint32(1); seq <= 3; seq++ {
			send.Sent(seq, true, true, now.Add(time.Duration(seq)*20*time.Millisecond))
		}
		for _, seq := range received {
			recv.Receive(seq, now)
		}
		summary, _ := send.Tick(now.Add(time.Second))
		if summary == nil {
			t.Fatal("missing summary")
		}
		if err := recv.AcceptSummary(*summary, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		_, early := recv.Tick(now.Add(1099 * time.Millisecond))
		if early != nil {
			t.Fatal("reorder grace skipped")
		}
		_, report := recv.Tick(now.Add(1100 * time.Millisecond))
		if report == nil {
			t.Fatal("missing report")
		}
		payload, err := protocol.EncodeVoiceReport(*report)
		if err != nil {
			t.Fatal(err)
		}
		*report, err = protocol.DecodeVoiceReport(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := send.AcceptReport(*report, now.Add(1200*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		stats := send.Stats(now.Add(1200 * time.Millisecond))
		expected := float64(3-len(received)) * 100 / 3
		if !stats.Outgoing.Known || math.Abs(stats.Outgoing.Loss-expected) > .001 {
			t.Fatalf("loss %+v, want %v", stats.Outgoing, expected)
		}
		if err := send.AcceptReport(*report, now.Add(1300*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if len(send.outUI) != 1 {
			t.Fatal("duplicate report inflated UI")
		}
	}
}

func TestTelemetryLocalFailureAndBurstBoundary(t *testing.T) {
	now := time.Unix(100, 0)
	recv := NewEndpoint(protocol.VoiceDownstream, now)
	recv.Receive(1, now)
	first := protocol.VoiceSummary{Direction: protocol.VoiceUpstream, Mode: protocol.VoiceMode{DesiredFrames: 2, AllowedFrames: 2}, Intervals: []protocol.VoiceSendInterval{{ID: 1, FirstSequence: 1, LastSequence: 3, SuccessfulSends: 2, LocalSendFailures: 1, ActiveMS: 60}}}
	if err := recv.AcceptSummary(first, now); err != nil {
		t.Fatal(err)
	}
	_, report := recv.Tick(now.Add(100 * time.Millisecond))
	if report == nil || report.Intervals[0].Flags&protocol.VoiceBurstValid != 0 {
		t.Fatal("local failure became network burst")
	}
	recv.Receive(5, now.Add(time.Second))
	first.Intervals = []protocol.VoiceSendInterval{{ID: 2, FirstSequence: 4, LastSequence: 5, SuccessfulSends: 2, ActiveMS: 40}}
	if err := recv.AcceptSummary(first, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, report = recv.Tick(now.Add(1100 * time.Millisecond))
	if report == nil {
		t.Fatal("missing second report")
	}
	last := report.Intervals[len(report.Intervals)-1]
	if last.NewMultipleBursts != 0 || last.LongestBurst != 0 {
		t.Fatal("local failure crossed interval boundary as a network series", last)
	}
}

func TestTelemetryWrapOverflowAndImmutableSummary(t *testing.T) {
	now := time.Unix(100, 0)
	recv := NewEndpoint(protocol.VoiceDownstream, now)
	for _, seq := range []uint32{math.MaxUint32, 0, 1} {
		recv.Receive(seq, now)
	}
	summary := protocol.VoiceSummary{Direction: 0, Mode: protocol.VoiceMode{DesiredFrames: 2, AllowedFrames: 2}, Intervals: []protocol.VoiceSendInterval{{ID: 1, FirstSequence: math.MaxUint32, LastSequence: 1, SuccessfulSends: 3}}}
	if err := recv.AcceptSummary(summary, now); err != nil {
		t.Fatal(err)
	}
	changed := summary
	changed.Intervals = append([]protocol.VoiceSendInterval(nil), summary.Intervals...)
	changed.Intervals[0].ActiveMS = 20
	if err := recv.AcceptSummary(changed, now); err == nil {
		t.Fatal("changed duplicate accepted")
	}
	_, report := recv.Tick(now.Add(100 * time.Millisecond))
	if report == nil || report.Intervals[0].ReceivedUnique != 3 {
		t.Fatal("uint32 wrap lost arrivals")
	}
	recv.Receive(100000, now.Add(time.Second))
	summary.Intervals = []protocol.VoiceSendInterval{{ID: 2, FirstSequence: 2, LastSequence: 2, SuccessfulSends: 1}}
	if err := recv.AcceptSummary(summary, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, report = recv.Tick(now.Add(1100 * time.Millisecond))
	if report.Intervals[len(report.Intervals)-1].Flags&protocol.VoiceIntervalComplete != 0 {
		t.Fatal("overwritten bitmap treated as a complete loss sample")
	}
}

func feedInterval(t *testing.T, send, recv *Endpoint, origin time.Time, second int, seq *uint32, drop func(uint32) bool, active bool) {
	t.Helper()
	start := origin.Add(time.Duration(second-1) * time.Second)
	if active {
		for i := 0; i < 50; i++ {
			*seq++
			at := start.Add(time.Duration(i) * 20 * time.Millisecond)
			send.Sent(*seq, true, true, at)
			send.Select([]protocol.VoiceBundleFrame{{Sequence: *seq - 1, Payload: []byte{1}}, {Sequence: *seq, Payload: []byte{1}}}, true, at)
			if drop == nil || !drop(*seq) {
				recv.Receive(*seq, at)
			}
		}
	}
	summary, _ := send.Tick(start.Add(time.Second))
	if summary == nil {
		t.Fatal("summary missing")
	}
	if err := recv.AcceptSummary(*summary, start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, report := recv.Tick(start.Add(1100 * time.Millisecond))
	if report == nil {
		t.Fatal("report missing")
	}
	if err := send.AcceptReport(*report, start.Add(1150*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
}
func TestAdaptiveProtectionHoldSilenceFreshnessAndRecovery(t *testing.T) {
	now := time.Unix(100, 0)
	send := NewEndpoint(0, now)
	recv := NewEndpoint(1, now)
	send.SetEnabled(true)
	var seq uint32
	for second := 1; second <= 3; second++ {
		feedInterval(t, send, recv, now, second, &seq, func(n uint32) bool { return n%10 == 0 }, true)
	}
	if send.Stats(now.Add(3200*time.Millisecond)).Local.DesiredFrames != 3 {
		t.Fatal("persistent loss did not enable 3 frames")
	}
	for second := 4; second <= 30; second++ {
		feedInterval(t, send, recv, now, second, &seq, nil, false)
	}
	if send.Stats(now.Add(30*time.Second)).Local.DesiredFrames != 3 {
		t.Fatal("silence advanced improvement timer")
	}
	for second := 31; second <= 55; second++ {
		feedInterval(t, send, recv, now, second, &seq, nil, true)
	}
	if send.Stats(now.Add(56*time.Second)).Local.DesiredFrames != 2 {
		t.Fatal("20 active good seconds did not reduce protection")
	}
	send.QueueDrop(now.Add(56 * time.Second))
	send.FrameCount(now.Add(56 * time.Second))
	if send.Stats(now.Add(56*time.Second)).Local.AllowedFrames != 1 {
		t.Fatal("local congestion did not limit copies")
	}
	for second := 57; second <= 81; second++ {
		feedInterval(t, send, recv, now, second, &seq, nil, true)
	}
	if send.Stats(now.Add(82*time.Second)).Local.AllowedFrames != 2 {
		t.Fatal("allowed mode did not recover")
	}
}

func TestReserveBucketsNeverDelayPrimaryAndBoundRepair(t *testing.T) {
	now := time.Unix(100, 0)
	e := NewEndpoint(0, now)
	e.SetEnabled(true)
	e.SetBudget(1)
	frames := []protocol.VoiceBundleFrame{{Sequence: 1, Payload: make([]byte, 600)}, {Sequence: 2, Payload: make([]byte, 500)}}
	for range 5 {
		out := e.Select(frames, true, now)
		if len(out) == 0 || out[len(out)-1].Sequence != 2 {
			t.Fatal("primary dropped for reserve budget")
		}
	}
	if e.Stats(now).Local.AllowedFrames != 1 {
		t.Fatal("reserve exhaustion did not limit mode")
	}
	if out := e.Select([]protocol.VoiceBundleFrame{{Sequence: 1, Payload: make([]byte, 1180)}}, false, now); out != nil {
		t.Fatal("repair bypassed exhausted bucket")
	}
	p := NewPool(10000)
	spent := 0
	for i := 0; i < 100; i++ {
		if p.Allow(uint64(i%2+1), 100, now) {
			spent += 100
		}
	}
	if spent > 2500 {
		t.Fatalf("global burst exceeded: %d", spent)
	}
}

func TestRTTDuplicateDoesNotAdvanceCongestion(t *testing.T) {
	now := time.Unix(100, 0)
	e := NewEndpoint(0, now)
	e.SetEnabled(true)
	e.RTT(protocol.VoiceRTTSample{ID: 1, Micros: 20000, Valid: true}, now)
	for i := 0; i < 20; i++ {
		e.RTT(protocol.VoiceRTTSample{ID: 2, Micros: 500000, Valid: true}, now.Add(time.Second))
	}
	if e.controller.rttHigh != 1 {
		t.Fatal("repeated RTT probe advanced EWMA")
	}
	e.RTT(protocol.VoiceRTTSample{ID: 3, Micros: 500000, Valid: true}, now.Add(2*time.Second))
	e.FrameCount(now.Add(2 * time.Second))
	if e.Stats(now.Add(2*time.Second)).Local.AllowedFrames != 1 {
		t.Fatal("two different high RTT probes did not limit protection")
	}
	e.FrameCount(now.Add(13 * time.Second))
	if e.controller.rttHigh != 0 {
		t.Fatal("expired RTT permanently blocks budget recovery")
	}
}

func TestContradictoryReportIsAtomicAndOldDataCannotRefreshAdaptation(t *testing.T) {
	now := time.Unix(100, 0)
	e := NewEndpoint(0, now)
	e.SetEnabled(true)
	for i := 0; i < 3; i++ {
		e.Sent(uint32(i+1), true, true, now.Add(time.Duration(i)*time.Second))
		e.Tick(now.Add(time.Duration(i+1) * time.Second))
	}
	valid := protocol.VoiceReceiveInterval{ID: 1, FirstSequence: 1, LastSequence: 1, ReceivedUnique: 1, Flags: protocol.VoiceIntervalComplete | protocol.VoiceBurstValid}
	bad := valid
	bad.ID = 2
	bad.FirstSequence = 2
	bad.LastSequence = 2
	bad.ReceivedUnique = 2
	if err := e.AcceptReport(protocol.VoiceReport{Direction: 0, Intervals: []protocol.VoiceReceiveInterval{valid, bad}}, now.Add(3200*time.Millisecond)); err == nil {
		t.Fatal("inconsistent report accepted")
	}
	if e.sent[0].report != nil {
		t.Fatal("partial report mutation")
	}
	valid.ReceivedUnique = 0
	if err := e.AcceptReport(protocol.VoiceReport{Direction: 0, Intervals: []protocol.VoiceReceiveInterval{valid}}, now.Add(7*time.Second)); err != nil {
		t.Fatal(err)
	}
	if !e.controller.lastFresh.IsZero() {
		t.Fatal("old interval refreshed adaptation")
	}
}

func TestRepeatedSummaryDoesNotRefreshRemoteMode(t *testing.T) {
	now := time.Unix(100, 0)
	e := NewEndpoint(1, now)
	summary := protocol.VoiceSummary{Direction: 0, Mode: protocol.VoiceMode{Revision: 1, DesiredFrames: 2, AllowedFrames: 2}, Intervals: []protocol.VoiceSendInterval{{ID: 1}}}
	if err := e.AcceptSummary(summary, now); err != nil {
		t.Fatal(err)
	}
	if err := e.AcceptSummary(summary, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if e.Stats(now.Add(4 * time.Second)).RemoteKnown {
		t.Fatal("duplicate refreshed remote mode")
	}
	summary.Intervals[0].ID = 2
	summary.Mode.DesiredFrames = 3
	summary.Mode.AllowedFrames = 3
	if err := e.AcceptSummary(summary, now.Add(5*time.Second)); err == nil {
		t.Fatal("same mode revision changed contents")
	}
}

func TestFairPoolReturnsUnusedAndIdleShares(t *testing.T) {
	now := time.Unix(100, 0)
	pool := NewPool(10000)
	pool.Share(1, 1000, now)
	pool.Share(2, 10000, now)
	pool.Share(1, 1000, now.Add(time.Second))
	if got := pool.Share(2, 10000, now.Add(time.Second)); got != 9000 {
		t.Fatalf("unused share was not returned: %d", got)
	}
	if got := pool.Share(2, 10000, now.Add(3*time.Second)); got != 10000 {
		t.Fatalf("idle peer retained quota: %d", got)
	}
}

func TestBudgetRecoveryRequiresSustainedHeadroom(t *testing.T) {
	now := time.Unix(100, 0)
	send, recv := NewEndpoint(0, now), NewEndpoint(1, now)
	send.SetEnabled(true)
	send.SetBudget(100)
	var seq uint32
	limited := false
	for second := 1; second <= 35; second++ {
		feedInterval(t, send, recv, now, second, &seq, nil, true)
		mode := send.Stats(now.Add(time.Duration(second) * time.Second)).Local
		if mode.AllowedFrames == 1 {
			limited = true
		} else if limited {
			t.Fatal("protection recovered without budget headroom", second, mode)
		}
	}
	if !limited {
		t.Fatal("low reserve did not limit copies")
	}
}
