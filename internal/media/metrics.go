package media

import (
	"log"
	"time"
)

func (m *Manager) logMetrics() {
	m.mu.RLock()
	publishers := make([]*publisher, 0, len(m.publishers))
	for _, p := range m.publishers {
		publishers = append(publishers, p)
	}
	m.mu.RUnlock()
	now := time.Now()
	for _, p := range publishers {
		p.mu.Lock()
		hasBaseline := !p.metricsAt.IsZero()
		elapsed := now.Sub(p.metricsAt).Seconds()
		inBytes := p.inBytes.Load()
		inPackets := p.inPackets.Load()
		inBitrate := float64(0)
		inPacketsPerSecond := float64(0)
		if hasBaseline && elapsed > 0 {
			inBitrate = float64(inBytes-p.lastInBytes) * 8 / elapsed
			inPacketsPerSecond = float64(inPackets-p.lastInPackets) / elapsed
		}
		p.metricsAt, p.lastInBytes, p.lastInPackets = now, inBytes, inPackets
		for _, s := range p.subscribers {
			outBytes := s.outBytes.Load()
			outPackets := s.outPackets.Load()
			outBitrate := float64(0)
			outPacketsPerSecond := float64(0)
			if hasBaseline && elapsed > 0 {
				outBitrate = float64(outBytes-s.lastOutBytes) * 8 / elapsed
				outPacketsPerSecond = float64(outPackets-s.lastOutPackets) / elapsed
			}
			s.lastOutBytes, s.lastOutPackets = outBytes, outPackets
			log.Printf("screen metrics: stream=%d session=%d in_kbps=%.0f in_pps=%.1f out_kbps=%.0f out_pps=%.1f queue=%d drops=%d pli=%d nack=%d", p.id, s.sessionID, inBitrate/1000, inPacketsPerSecond, outBitrate/1000, outPacketsPerSecond, len(s.packets), s.drops.Load(), s.plis.Load(), s.nacks.Load())
		}
		p.mu.Unlock()
	}
}
