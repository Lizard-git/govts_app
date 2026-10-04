package serverstatus

import (
	"context"
	"math/rand/v2"
	"net/netip"
	"sort"
	"sync"
	"time"
)

type Status struct {
	OnlineCount   *uint32 `json:"onlineCount"`
	Status        string  `json:"status"`
	UpdatedAt     int64   `json:"updatedAt"`
	LastAttemptAt int64   `json:"lastAttemptAt"`
}

type entry struct {
	Status
	next     time.Time
	failures int
	cancel   context.CancelFunc
}

type lease struct {
	until   time.Time
	revoked bool
}

// Targets returns all history endpoints and the endpoint served by a fresh snapshot.
type Targets func() ([]netip.AddrPort, netip.AddrPort)

type Monitor struct {
	mu        sync.Mutex
	entries   map[netip.AddrPort]*entry
	leases    map[string]lease
	active    int
	closed    bool
	suspended bool
}

func NewMonitor() *Monitor {
	return &Monitor{entries: make(map[netip.AddrPort]*entry), leases: make(map[string]lease)}
}

// Each component owns a unique token. Revocation rejects late in-flight renewals.
func (m *Monitor) SetVisible(token string, visible bool) {
	if len(token) < 16 || len(token) > 80 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	now := time.Now()
	for key, value := range m.leases {
		if now.After(value.until) {
			delete(m.leases, key)
		}
	}
	previous, exists := m.leases[token]
	if visible && previous.revoked {
		return
	}
	if !exists && len(m.leases) >= 128 {
		return
	}
	m.leases[token] = lease{until: now.Add(20 * time.Second), revoked: !visible}
	if !m.visibleLocked(now) {
		m.cancelLocked()
	}
}

func (m *Monitor) visibleLocked(now time.Time) bool {
	if m.suspended {
		return false
	}
	for _, value := range m.leases {
		if !value.revoked && now.Before(value.until) {
			return true
		}
	}
	return false
}

// Native window visibility is independent of the WebView's document visibility.
func (m *Monitor) SetSuspended(value bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.suspended = value
	if value {
		m.cancelLocked()
	}
}

func (m *Monitor) cancelLocked() {
	for _, value := range m.entries {
		if value.cancel != nil {
			value.cancel()
		}
	}
}

func (m *Monitor) Forget(address netip.AddrPort) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if value := m.entries[address]; value != nil && value.cancel != nil {
		value.cancel()
	}
	delete(m.entries, address)
}

func (m *Monitor) Snapshot(address netip.AddrPort) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	value := m.entries[address]
	if value == nil {
		return Status{Status: "loading"}
	}
	result := value.Status
	if result.OnlineCount != nil {
		count := *result.OnlineCount
		result.OnlineCount = &count
	}
	if result.Status == "fresh" && time.Now().UnixMilli()-result.UpdatedAt >= 30_000 {
		result.Status = "stale"
	}
	return result
}

func (m *Monitor) Run(ctx context.Context, targets Targets, changed func()) {
	var workers sync.WaitGroup
	defer func() {
		m.mu.Lock()
		m.closed = true
		m.cancelLocked()
		m.mu.Unlock()
		workers.Wait()
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			addresses, current := targets()
			wanted := make(map[netip.AddrPort]bool, len(addresses))
			for _, address := range addresses {
				wanted[address] = true
			}
			m.mu.Lock()
			for address, value := range m.entries {
				if !wanted[address] {
					if value.cancel != nil {
						value.cancel()
					}
					delete(m.entries, address)
				}
			}
			if !m.visibleLocked(now) {
				m.cancelLocked()
				m.mu.Unlock()
				continue
			}
			notify := false
			// Oldest attempts first: large favorite lists must not starve later rows.
			sort.SliceStable(addresses, func(i, j int) bool {
				var left, right int64
				if value := m.entries[addresses[i]]; value != nil {
					left = value.LastAttemptAt
				}
				if value := m.entries[addresses[j]]; value != nil {
					right = value.LastAttemptAt
				}
				return left < right
			})
			for _, address := range addresses {
				value := m.entries[address]
				if value == nil {
					value = &entry{Status: Status{Status: "loading"}}
					m.entries[address] = value
				}
				if value.Status.Status == "fresh" && now.UnixMilli()-value.UpdatedAt >= 30_000 {
					value.Status.Status = "stale"
					notify = true
				}
				if address == current {
					if value.cancel != nil {
						value.cancel()
					}
					continue
				}
				if value.cancel != nil || now.Before(value.next) || m.active >= 4 {
					continue
				}
				queryCtx, cancel := context.WithCancel(ctx)
				value.cancel = cancel
				value.LastAttemptAt = now.UnixMilli()
				m.active++
				workers.Add(1)
				go func(address netip.AddrPort, value *entry) {
					defer workers.Done()
					defer cancel()
					count, err := Query(queryCtx, address)
					completed := time.Now()
					m.mu.Lock()
					m.active--
					if m.closed || queryCtx.Err() != nil || m.entries[address] != value {
						// Query's own deadline is internal; cancellation here is a stale generation.
						if m.entries[address] == value {
							value.cancel = nil
						}
						m.mu.Unlock()
						return
					}
					value.cancel = nil
					interval := 10 * time.Second
					if err == nil {
						value.OnlineCount = &count
						value.UpdatedAt = completed.UnixMilli()
						value.Status.Status = "fresh"
						value.failures = 0
					} else {
						value.failures++
						if value.OnlineCount == nil {
							value.Status.Status = "unavailable"
						} else {
							value.Status.Status = "stale"
						}
						interval *= time.Duration(1 << min(value.failures-1, 3))
						interval = min(interval, 60*time.Second)
					}
					value.next = completed.Add(interval + time.Duration(rand.IntN(1000))*time.Millisecond)
					m.mu.Unlock()
					changed()
				}(address, value)
			}
			m.mu.Unlock()
			if notify {
				changed()
			}
		}
	}
}
