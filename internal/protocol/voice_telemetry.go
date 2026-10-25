package protocol

import (
	"encoding/binary"
	"errors"
)

const (
	VoiceUpstream              byte = 0
	VoiceDownstream            byte = 1
	MaxVoiceIntervals               = 5
	VoiceIntervalComplete      byte = 1 << 0
	VoiceBurstValid            byte = 1 << 1
	VoiceRestrictionNone       byte = 0
	VoiceRestrictionBudget     byte = 1
	VoiceRestrictionCongestion byte = 2
	VoiceRestrictionDisabled   byte = 3
	voiceSummaryIntervalSize        = 22
	voiceReportIntervalSize         = 37
	voiceModeSize                   = 8
	voiceRTTSize                    = 13
)

var ErrInvalidVoiceTelemetry = errors.New("invalid voice telemetry")

type VoiceSendInterval struct {
	ID                uint32
	FirstSequence     uint32
	LastSequence      uint32
	SuccessfulSends   uint32
	LocalSendFailures uint32
	ActiveMS          uint16
}

type VoiceMode struct {
	Revision      uint32
	DesiredFrames byte
	AllowedFrames byte
	Reason        byte
}

type VoiceSummary struct {
	Direction byte
	Intervals []VoiceSendInterval
	Mode      VoiceMode
}

type VoiceReceiveInterval struct {
	ID                uint32
	FirstSequence     uint32
	LastSequence      uint32
	ReceivedUnique    uint32
	NewMultipleBursts uint32
	LongestBurst      uint32
	RestoredUnique    uint32
	LateFrames        uint32
	PLCFrames         uint32
	Flags             byte
}

// VoiceRTTSample is repeated unchanged until a different heartbeat probe
// completes. Receivers must deduplicate ID before advancing RTT filters.
type VoiceRTTSample struct {
	ID     uint32
	Micros uint32
	AgeMS  uint32
	Valid  bool
}

type VoiceReport struct {
	Direction byte
	Intervals []VoiceReceiveInterval
	RTT       VoiceRTTSample
}

func validateVoiceMode(mode VoiceMode) bool {
	if mode.Reason == VoiceRestrictionDisabled {
		return mode.DesiredFrames == 1 && mode.AllowedFrames == 1
	}
	return (mode.DesiredFrames == 2 || mode.DesiredFrames == 3) &&
		mode.AllowedFrames >= 1 && mode.AllowedFrames <= mode.DesiredFrames &&
		mode.Reason <= VoiceRestrictionCongestion &&
		(mode.Reason != VoiceRestrictionNone || mode.AllowedFrames == mode.DesiredFrames)
}

func validateVoiceSummary(summary VoiceSummary) error {
	if summary.Direction > VoiceDownstream || len(summary.Intervals) == 0 || len(summary.Intervals) > MaxVoiceIntervals || !validateVoiceMode(summary.Mode) {
		return ErrInvalidVoiceTelemetry
	}
	for i, interval := range summary.Intervals {
		attempts := uint64(interval.SuccessfulSends) + uint64(interval.LocalSendFailures)
		if interval.ActiveMS > 1000 || attempts >= 1<<31 ||
			(attempts == 0 && (interval.FirstSequence != 0 || interval.LastSequence != 0)) ||
			(attempts > 0 && uint64(uint32(interval.LastSequence-interval.FirstSequence))+1 != attempts) ||
			(i > 0 && interval.ID-summary.Intervals[i-1].ID != 1) {
			return ErrInvalidVoiceTelemetry
		}
	}
	return nil
}

// Intervals are oldest first, including empty intervals. Sequence zero is a
// valid value after wrap; counts distinguish an empty range from [0, 0].
func EncodeVoiceSummary(summary VoiceSummary) ([]byte, error) {
	if err := validateVoiceSummary(summary); err != nil {
		return nil, err
	}
	payload := make([]byte, 0, 3+len(summary.Intervals)*voiceSummaryIntervalSize+voiceModeSize)
	payload = append(payload, VoiceControlVersion, summary.Direction, byte(len(summary.Intervals)))
	for _, interval := range summary.Intervals {
		payload = binary.BigEndian.AppendUint32(payload, interval.ID)
		payload = binary.BigEndian.AppendUint32(payload, interval.FirstSequence)
		payload = binary.BigEndian.AppendUint32(payload, interval.LastSequence)
		payload = binary.BigEndian.AppendUint32(payload, interval.SuccessfulSends)
		payload = binary.BigEndian.AppendUint32(payload, interval.LocalSendFailures)
		payload = binary.BigEndian.AppendUint16(payload, interval.ActiveMS)
	}
	payload = binary.BigEndian.AppendUint32(payload, summary.Mode.Revision)
	payload = append(payload, summary.Mode.DesiredFrames, summary.Mode.AllowedFrames, summary.Mode.Reason, 0)
	return payload, nil
}

func DecodeVoiceSummary(payload []byte) (VoiceSummary, error) {
	if len(payload) < 3 || payload[0] != VoiceControlVersion {
		return VoiceSummary{}, ErrInvalidVoiceTelemetry
	}
	count := int(payload[2])
	if count == 0 || count > MaxVoiceIntervals || len(payload) != 3+count*voiceSummaryIntervalSize+voiceModeSize {
		return VoiceSummary{}, ErrInvalidVoiceTelemetry
	}
	summary := VoiceSummary{Direction: payload[1], Intervals: make([]VoiceSendInterval, 0, count)}
	rest := payload[3:]
	for i := 0; i < count; i++ {
		summary.Intervals = append(summary.Intervals, VoiceSendInterval{
			ID:                binary.BigEndian.Uint32(rest[:4]),
			FirstSequence:     binary.BigEndian.Uint32(rest[4:8]),
			LastSequence:      binary.BigEndian.Uint32(rest[8:12]),
			SuccessfulSends:   binary.BigEndian.Uint32(rest[12:16]),
			LocalSendFailures: binary.BigEndian.Uint32(rest[16:20]),
			ActiveMS:          binary.BigEndian.Uint16(rest[20:22]),
		})
		rest = rest[voiceSummaryIntervalSize:]
	}
	if rest[7] != 0 {
		return VoiceSummary{}, ErrInvalidVoiceTelemetry
	}
	summary.Mode = VoiceMode{Revision: binary.BigEndian.Uint32(rest[:4]), DesiredFrames: rest[4], AllowedFrames: rest[5], Reason: rest[6]}
	if err := validateVoiceSummary(summary); err != nil {
		return VoiceSummary{}, err
	}
	return summary, nil
}

func validateVoiceReport(report VoiceReport) error {
	if report.Direction > VoiceDownstream || len(report.Intervals) == 0 || len(report.Intervals) > MaxVoiceIntervals {
		return ErrInvalidVoiceTelemetry
	}
	if report.RTT.Valid {
		if report.RTT.Micros == 0 || report.RTT.AgeMS > 10000 {
			return ErrInvalidVoiceTelemetry
		}
	} else if report.RTT.ID != 0 || report.RTT.Micros != 0 || report.RTT.AgeMS != 0 {
		return ErrInvalidVoiceTelemetry
	}
	for i, interval := range report.Intervals {
		if interval.Flags & ^byte(VoiceIntervalComplete|VoiceBurstValid) != 0 ||
			(interval.Flags&VoiceBurstValid != 0 && interval.Flags&VoiceIntervalComplete == 0) ||
			(report.Direction == VoiceUpstream && interval.PLCFrames != 0) ||
			(i > 0 && int32(interval.ID-report.Intervals[i-1].ID) <= 0) {
			return ErrInvalidVoiceTelemetry
		}
		// A receiver cannot know which individual attempts failed locally.
		// Matching successful_sends and flags is the sender's responsibility.
		span := uint64(uint32(interval.LastSequence-interval.FirstSequence)) + 1
		if span >= 1<<31 || uint64(interval.ReceivedUnique) > span ||
			(interval.Flags&VoiceBurstValid == 0 && (interval.NewMultipleBursts != 0 || interval.LongestBurst != 0)) ||
			(interval.NewMultipleBursts != 0 && interval.LongestBurst < 2) {
			return ErrInvalidVoiceTelemetry
		}
	}
	return nil
}

func EncodeVoiceReport(report VoiceReport) ([]byte, error) {
	if err := validateVoiceReport(report); err != nil {
		return nil, err
	}
	payload := make([]byte, 0, 3+len(report.Intervals)*voiceReportIntervalSize+voiceRTTSize)
	payload = append(payload, VoiceControlVersion, report.Direction, byte(len(report.Intervals)))
	for _, interval := range report.Intervals {
		for _, value := range [...]uint32{interval.ID, interval.FirstSequence, interval.LastSequence, interval.ReceivedUnique,
			interval.NewMultipleBursts, interval.LongestBurst, interval.RestoredUnique, interval.LateFrames, interval.PLCFrames} {
			payload = binary.BigEndian.AppendUint32(payload, value)
		}
		payload = append(payload, interval.Flags)
	}
	payload = binary.BigEndian.AppendUint32(payload, report.RTT.ID)
	payload = binary.BigEndian.AppendUint32(payload, report.RTT.Micros)
	payload = binary.BigEndian.AppendUint32(payload, report.RTT.AgeMS)
	if report.RTT.Valid {
		payload = append(payload, 1)
	} else {
		payload = append(payload, 0)
	}
	return payload, nil
}

func DecodeVoiceReport(payload []byte) (VoiceReport, error) {
	if len(payload) < 3 || payload[0] != VoiceControlVersion {
		return VoiceReport{}, ErrInvalidVoiceTelemetry
	}
	count := int(payload[2])
	if count == 0 || count > MaxVoiceIntervals || len(payload) != 3+count*voiceReportIntervalSize+voiceRTTSize {
		return VoiceReport{}, ErrInvalidVoiceTelemetry
	}
	report := VoiceReport{Direction: payload[1], Intervals: make([]VoiceReceiveInterval, 0, count)}
	rest := payload[3:]
	for i := 0; i < count; i++ {
		report.Intervals = append(report.Intervals, VoiceReceiveInterval{
			ID:                binary.BigEndian.Uint32(rest[:4]),
			FirstSequence:     binary.BigEndian.Uint32(rest[4:8]),
			LastSequence:      binary.BigEndian.Uint32(rest[8:12]),
			ReceivedUnique:    binary.BigEndian.Uint32(rest[12:16]),
			NewMultipleBursts: binary.BigEndian.Uint32(rest[16:20]),
			LongestBurst:      binary.BigEndian.Uint32(rest[20:24]),
			RestoredUnique:    binary.BigEndian.Uint32(rest[24:28]),
			LateFrames:        binary.BigEndian.Uint32(rest[28:32]),
			PLCFrames:         binary.BigEndian.Uint32(rest[32:36]),
			Flags:             rest[36],
		})
		rest = rest[voiceReportIntervalSize:]
	}
	if rest[12] > 1 {
		return VoiceReport{}, ErrInvalidVoiceTelemetry
	}
	report.RTT = VoiceRTTSample{ID: binary.BigEndian.Uint32(rest[:4]), Micros: binary.BigEndian.Uint32(rest[4:8]), AgeMS: binary.BigEndian.Uint32(rest[8:12]), Valid: rest[12] == 1}
	if err := validateVoiceReport(report); err != nil {
		return VoiceReport{}, err
	}
	return report, nil
}
