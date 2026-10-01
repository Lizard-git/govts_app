package logging

import (
	"log"
	"sync"
	"time"
)

const failureInterval = 10 * time.Second

var failureReasons = [...]string{"other", "unknown_session", "replayed_record", "authentication_failed", "malformed_packet"}

type failureBucket struct {
	count uint64
	next  time.Time
	last  error
	peer  any
}

// Failures has a fixed number of buckets and flushes even while its processing
// loop is blocked waiting for packets. Close stops the timer and flushes once.
type Failures struct {
	operation string
	mu        sync.Mutex
	buckets   [len(failureReasons)]failureBucket
	stop      chan struct{}
	done      chan struct{}
	once      sync.Once
}

func NewFailures(operation string) *Failures {
	f := &Failures{operation: operation, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(f.done)
		ticker := time.NewTicker(failureInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				f.Flush()
			case <-f.stop:
				f.Flush()
				return
			}
		}
	}()
	return f
}

func (f *Failures) Record(err error, peer any) {
	f.RecordKind("other", err, peer)
}

func (f *Failures) RecordKind(reason string, err error, peer any) {
	index := 0
	for i, known := range failureReasons {
		if reason == known {
			index = i
			break
		}
	}
	now := time.Now()
	f.mu.Lock()
	bucket := &f.buckets[index]
	bucket.count++
	bucket.last, bucket.peer = err, peer
	var report failureBucket
	if !now.Before(bucket.next) {
		report = *bucket
		*bucket = failureBucket{next: now.Add(failureInterval)}
	}
	f.mu.Unlock()
	f.report(index, report)
}

func (f *Failures) Flush() {
	f.mu.Lock()
	reports := f.buckets
	for i := range f.buckets {
		f.buckets[i] = failureBucket{next: time.Now().Add(failureInterval)}
	}
	f.mu.Unlock()
	for i, report := range reports {
		f.report(i, report)
	}
}

func (f *Failures) report(index int, bucket failureBucket) {
	if bucket.count == 0 {
		return
	}
	log.Printf("diagnostic failures: operation=%s reason=%s count=%d last_peer=%v last_error=%v", f.operation, failureReasons[index], bucket.count, bucket.peer, bucket.last)
}

func (f *Failures) Close() {
	f.once.Do(func() { close(f.stop); <-f.done })
}
