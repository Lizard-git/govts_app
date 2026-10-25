package protocol

import (
	"slices"
	"time"
)

const VoiceStateAssemblyTimeout = 3 * time.Second

// VoiceStateSnapshot contains a fully assembled revision. Header has no page
// entries; Sources contains all entries, in wire page order. The receiver must
// install the referenced audio profile and this snapshot before acknowledging.
type VoiceStateSnapshot struct {
	Header  VoiceStatePage
	Sources []VoiceSourceState
}

// VoiceStateAssembler owns at most one incomplete revision. It is not safe for
// concurrent use. Add never installs state or acknowledges a revision itself.
// Completed revisions must be passed as installedRevision only after the caller
// has atomically installed them; duplicates can then be acknowledged again.
// Call Reset after installation to release the completed assembly.
type VoiceStateAssembler struct {
	header   VoiceStatePage
	started  time.Time
	pages    [MaxVoiceStatePages][]VoiceSourceState
	seen     [MaxVoiceStatePages]bool
	count    uint16
	complete bool
}

func (a *VoiceStateAssembler) Reset() { *a = VoiceStateAssembler{} }

// Expire must also be called during network silence, not just from Add. True
// asks the caller to request a new full snapshot under its request rate limit.
func (a *VoiceStateAssembler) Expire(now time.Time) bool {
	if a.header.Revision != 0 && now.Sub(a.started) >= VoiceStateAssemblyTimeout {
		a.Reset()
		return true
	}
	return false
}

// Add returns a completed snapshot, or nil while incomplete/stale. reack is
// true only for an already installed revision and must not trigger a reset of
// audio queues. An error requires resynchronization. Duplicate pages do not
// restart the deadline. A newer revision replaces an incomplete older one.
func (a *VoiceStateAssembler) Add(page VoiceStatePage, installedRevision uint64, now time.Time) (snapshot *VoiceStateSnapshot, reack bool, err error) {
	if err := validateVoiceStatePage(page); err != nil {
		return nil, false, err
	}
	if page.Revision == installedRevision {
		return nil, true, nil
	}
	if page.Revision < installedRevision {
		return nil, false, nil
	}
	if a.Expire(now) {
		return nil, false, ErrInvalidVoiceState
	}
	if a.header.Revision > page.Revision {
		return nil, false, nil
	}
	if page.Flags&VoiceStateFull == 0 && page.BaseRevision != installedRevision {
		a.Reset()
		return nil, false, ErrInvalidVoiceState
	}
	header := page
	header.PageIndex = 0
	header.Sources = nil
	if a.header.Revision == 0 || a.header.Revision < page.Revision {
		a.Reset()
		a.header = header
		a.started = now
	} else if !sameVoiceStateHeader(a.header, header) {
		a.Reset()
		return nil, false, ErrInvalidVoiceState
	}
	index := int(page.PageIndex)
	if a.seen[index] {
		if !slices.Equal(a.pages[index], page.Sources) {
			a.Reset()
			return nil, false, ErrInvalidVoiceState
		}
	} else {
		a.pages[index] = slices.Clone(page.Sources)
		a.seen[index] = true
		a.count++
	}
	if a.count != a.header.PageCount {
		return nil, false, nil
	}
	if a.complete {
		// The caller already owns this completed candidate while waiting for
		// its profile. Still validate duplicate bytes above, without rebuilding
		// all sources for every repeated page of a large snapshot.
		return nil, false, nil
	}
	sources := make([]VoiceSourceState, 0, int(a.header.PageCount)*MaxVoiceStateEntries)
	ids := make(map[uint64]struct{})
	for _, entries := range a.pages[:a.header.PageCount] {
		for _, source := range entries {
			if _, duplicate := ids[source.SenderID]; duplicate {
				a.Reset()
				return nil, false, ErrInvalidVoiceState
			}
			ids[source.SenderID] = struct{}{}
			sources = append(sources, source)
		}
	}
	// Keep pages until installation or expiry, so contradictory retransmissions
	// of the completed revision cannot replace its immutable contents.
	a.complete = true
	return &VoiceStateSnapshot{Header: a.header, Sources: sources}, false, nil
}

func sameVoiceStateHeader(left, right VoiceStatePage) bool {
	// Sources and PageIndex have already been removed from both headers.
	return left.Flags == right.Flags && left.Revision == right.Revision && left.BaseRevision == right.BaseRevision &&
		left.OwnControlRevision == right.OwnControlRevision && left.OwnSourceEpoch == right.OwnSourceEpoch &&
		left.OwnReceiverEpoch == right.OwnReceiverEpoch && left.OwnChannelID == right.OwnChannelID &&
		left.AudioProfileRevision == right.AudioProfileRevision && left.DownstreamBarrier == right.DownstreamBarrier &&
		left.EnabledDirections == right.EnabledDirections && left.PageCount == right.PageCount
}

// PaginateVoiceState produces a stable, bounded full snapshot or delta. Callers
// retain the encoded pages until Ack; rebuilding the same revision from mutable
// hub state would violate the assembly contract.
func PaginateVoiceState(header VoiceStatePage, sources []VoiceSourceState) ([]VoiceStatePage, error) {
	if len(sources) > MaxVoiceSources {
		return nil, ErrInvalidVoiceState
	}
	count := max(1, (len(sources)+MaxVoiceStateEntries-1)/MaxVoiceStateEntries)
	header.PageCount = uint16(count)
	header.Sources = nil
	pages := make([]VoiceStatePage, 0, count)
	ids := make(map[uint64]struct{}, len(sources))
	for _, source := range sources {
		if _, duplicate := ids[source.SenderID]; duplicate {
			return nil, ErrInvalidVoiceState
		}
		ids[source.SenderID] = struct{}{}
	}
	for i := 0; i < count; i++ {
		page := header
		page.PageIndex = uint16(i)
		start := i * MaxVoiceStateEntries
		page.Sources = slices.Clone(sources[start:min(start+MaxVoiceStateEntries, len(sources))])
		if err := validateVoiceStatePage(page); err != nil {
			return nil, err
		}
		pages = append(pages, page)
	}
	return pages, nil
}
