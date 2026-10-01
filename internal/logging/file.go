package logging

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	queueCapacity  = 2048
	maxRecordBytes = 8192
	shutdownWait   = time.Second
	maxFileBytes   = 10 * 1024 * 1024
	maxStoredBytes = 40 * 1024 * 1024
)

// asyncWriter never waits for its destination. Memory is bounded; excess
// records are dropped and summarized when the destination becomes writable.
type asyncWriter struct {
	queue   chan []byte
	stop    chan struct{}
	done    chan struct{}
	stopped atomic.Bool
	dropped atomic.Uint64
}

func newAsyncWriter(destination io.Writer, closeDestination func(), onError func(error), location *time.Location) *asyncWriter {
	w := &asyncWriter{queue: make(chan []byte, queueCapacity), stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		if closeDestination != nil {
			defer closeDestination()
		}
		var nextError time.Time
		reportError := func(err error) {
			if onError != nil && time.Now().After(nextError) {
				onError(err)
				nextError = time.Now().Add(time.Minute)
			}
		}
		write := func(record []byte) {
			n, err := destination.Write(record)
			if err == nil && n != len(record) {
				err = io.ErrShortWrite
			}
			if err != nil {
				w.dropped.Add(1)
				reportError(err)
			}
		}
		flushDrops := func() {
			if count := w.dropped.Swap(0); count > 0 {
				record := []byte(fmt.Sprintf("%s logging: dropped_records=%d (queue full or write failure)\n", time.Now().In(location).Format("2006/01/02 15:04:05.000000"), count))
				if n, err := destination.Write(record); err != nil || n != len(record) {
					w.dropped.Add(count)
					if err == nil {
						err = io.ErrShortWrite
					}
					reportError(err)
				}
			}
		}
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case record := <-w.queue:
				write(record)
			case <-ticker.C:
				flushDrops()
			case <-w.stop:
				for {
					select {
					case record := <-w.queue:
						write(record)
					default:
						flushDrops()
						return
					}
				}
			}
		}
	}()
	return w
}

func (w *asyncWriter) Write(record []byte) (int, error) {
	originalSize := len(record)
	if w.stopped.Load() {
		return originalSize, nil
	}
	if len(w.queue) == cap(w.queue) {
		w.dropped.Add(1)
		return originalSize, nil
	}
	if len(record) > maxRecordBytes {
		record = record[:maxRecordBytes]
	}
	copyRecord := append([]byte(nil), record...)
	if originalSize > maxRecordBytes {
		copy(copyRecord[len(copyRecord)-len(" [truncated]\n"):], " [truncated]\n")
	}
	select {
	case w.queue <- copyRecord:
	default:
		w.dropped.Add(1)
	}
	return originalSize, nil
}

// Start installs independent bounded queues for file and console output.
// A stalled destination cannot block application workers or the other output.
// Directory creation, file opening and retries also run in the file worker.
// Shutdown drains queued records for at most one second.
// path selects the directory and filename prefix; each run and segment gets
// its own .log file. Retention never deletes a segment held by another writer.
func Start(path string) (func(), error) {
	return start(path, time.UTC)
}

// StartLocal uses the host's local timezone for records and segment filenames.
func StartLocal(path string) (func(), error) {
	return start(path, time.Local)
}

func start(path string, location *time.Location) (func(), error) {
	previousWriter := log.Writer()
	console := newAsyncWriter(previousWriter, nil, nil, location)
	flags := log.Ldate | log.Ltime | log.Lmicroseconds
	if location == time.UTC {
		flags |= log.LUTC
	}
	log.SetFlags(flags)
	log.SetOutput(console)
	writers := []*asyncWriter{console}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			log.Printf("logging stopped: pid=%d", os.Getpid())
			// Late background logs must not fall back to synchronous I/O.
			log.SetOutput(io.Discard)
			for _, w := range writers {
				w.stopped.Store(true)
				close(w.stop)
			}
			timer := time.NewTimer(shutdownWait)
			defer timer.Stop()
			for _, w := range writers {
				select {
				case <-w.done:
				case <-timer.C:
					return
				}
			}
		})
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return stop, fmt.Errorf("resolve log file: %w", err)
	}
	ext := filepath.Ext(absolutePath)
	stem := strings.TrimSuffix(filepath.Base(absolutePath), ext)
	started := time.Now().In(location)
	runID := fmt.Sprintf("%s-%d", started.Format("20060102T150405.000000000"), os.Getpid())
	file := &fileSink{directory: filepath.Dir(absolutePath), stem: stem, runID: runID}
	fileWriter := newAsyncWriter(file, file.close, func(err error) {
		_, _ = fmt.Fprintf(console, "%s logging file write failed: directory=%q prefix=%q run_id=%s error=%v\n", time.Now().In(location).Format("2006/01/02 15:04:05.000000"), file.directory, stem, runID, err)
	}, location)
	writers = append(writers, fileWriter)
	log.SetOutput(io.MultiWriter(fileWriter, console))
	zone, offset := started.Zone()
	log.Printf("logging started: directory=%q prefix=%q run_id=%s pid=%d timezone=%q zone=%q utc_offset_seconds=%d", file.directory, stem, runID, os.Getpid(), location.String(), zone, offset)
	return stop, nil
}

// fileSink is owned exclusively by the file worker.
type fileSink struct {
	directory string
	stem      string
	runID     string
	part      uint64
	file      *os.File
	nextOpen  time.Time
	lastError error
	size      int64
}

func (f *fileSink) Write(record []byte) (int, error) {
	if f.file == nil {
		if time.Now().Before(f.nextOpen) {
			return 0, f.lastError
		}
		f.lastError = f.openNext()
		if f.lastError != nil {
			f.nextOpen = time.Now().Add(time.Minute)
			return 0, f.lastError
		}
	}
	if f.size+int64(len(record)) > maxFileBytes {
		if time.Now().Before(f.nextOpen) {
			return 0, f.lastError
		}
		if err := f.openNext(); err != nil {
			f.lastError = err
			f.nextOpen = time.Now().Add(time.Minute)
			return 0, err
		}
	}
	n, err := f.file.Write(record)
	f.size += int64(n)
	if err != nil {
		f.close()
		f.lastError = err
		f.nextOpen = time.Now().Add(time.Minute)
	}
	return n, err
}

func (f *fileSink) openNext() error {
	if err := os.MkdirAll(f.directory, 0700); err != nil {
		return err
	}
	var next *os.File
	for {
		f.part++
		path := filepath.Join(f.directory, fmt.Sprintf("%s-%s-%06d.log", f.stem, f.runID, f.part))
		var err error
		next, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := lockLogFile(next); err != nil {
			_ = next.Close()
			_ = os.Remove(path) // This is the empty file just created by us.
			return err
		}
		break
	}
	// Keep the old file and all history intact until creation succeeds.
	f.close()
	f.file = next
	f.size = 0
	f.lastError = nil
	f.nextOpen = time.Time{}
	f.prune()
	return nil
}

// prune considers only this application's generated filenames. Active files
// are protected by a nonblocking OS lock, including files of other processes.
// Cleanup runs only after successfully opening a replacement, in this worker.
func (f *fileSink) prune() {
	entries, err := os.ReadDir(f.directory)
	if err != nil {
		return
	}
	pattern := regexp.MustCompile("^" + regexp.QuoteMeta(f.stem) + `-\d{8}T\d{6}\.\d{9}-\d+-\d{6,}\.log$`)
	type candidate struct {
		path string
		info os.FileInfo
	}
	var files []candidate
	var total int64
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !pattern.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.Size() == 0 {
			continue
		}
		total += info.Size()
		files = append(files, candidate{filepath.Join(f.directory, entry.Name()), info})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].info.ModTime().Equal(files[j].info.ModTime()) {
			return files[i].path < files[j].path
		}
		return files[i].info.ModTime().Before(files[j].info.ModTime())
	})
	// Reserve one segment for the newly opened file. Active logs are never
	// removed, so many simultaneous processes can exceed the retention budget.
	for _, old := range files {
		if total <= maxStoredBytes-maxFileBytes {
			break
		}
		if old.path == f.file.Name() {
			continue
		}
		handle, err := os.OpenFile(old.path, os.O_RDWR, 0)
		if err != nil {
			continue
		}
		lockErr := lockLogFile(handle)
		_ = handle.Close()
		if lockErr != nil {
			continue
		}
		// Generated segments are never reopened for writing, so once their
		// lock can be acquired they cannot become active again.
		if err := os.Remove(old.path); err == nil || errors.Is(err, os.ErrNotExist) {
			total -= old.info.Size()
		}
	}
}

func (f *fileSink) close() {
	if f.file != nil {
		_ = f.file.Close()
		f.file = nil
	}
}
