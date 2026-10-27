package clientapp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	voiceclient "uniclog.io/sonoryx/internal/client"
	"uniclog.io/sonoryx/internal/domain"
	"uniclog.io/sonoryx/internal/filetransfer"
	"uniclog.io/sonoryx/internal/persist"
	"uniclog.io/sonoryx/internal/protocol"
)

type FileTask struct {
	ID           string          `json:"id"`
	Context      string          `json:"context"`
	Direction    string          `json:"direction"`
	Kind         domain.ChatKind `json:"kind"`
	TargetID     int64           `json:"targetId,string"`
	Name         string          `json:"name"`
	Path         string          `json:"path"`
	Size         int64           `json:"size"`
	SHA256       string          `json:"sha256"`
	Offset       int64           `json:"offset"`
	UploadID     string          `json:"uploadId"`
	AttachmentID int64           `json:"attachmentId,string"`
	State        string          `json:"state"`
	Error        string          `json:"error"`
	Attempts     int             `json:"attempts"`
	RetryAt      int64           `json:"retryAt"`
	Staged       bool            `json:"staged"`
}
type FileQueue struct {
	app     *App
	mu      sync.Mutex
	tasks   []FileTask
	path    string
	active  map[string]context.CancelFunc
	wake    chan struct{}
	done    chan struct{}
	workers sync.WaitGroup
}

func (a *App) Files() *FileQueue {
	a.fileOnce.Do(func() {
		dir, err := os.UserConfigDir()
		q := &FileQueue{app: a, active: map[string]context.CancelFunc{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
		if err == nil {
			q.path = filepath.Join(dir, "Sonoryx", "file-queue.json")
			if b, e := os.ReadFile(q.path); e == nil && len(b) <= 1<<20 {
				_ = json.Unmarshal(b, &q.tasks)
			}
		}
		if len(q.tasks) > 256 {
			q.tasks = q.tasks[:256]
		}
		for i := range q.tasks {
			if q.tasks[i].State == "running" || q.tasks[i].State == "ready" || q.tasks[i].State == "verifying" {
				q.tasks[i].State = "queued"
			}
			if q.tasks[i].State == "done" || q.tasks[i].State == "cancelled" {
				q.cleanupStaged(q.tasks[i])
			}
		}
		q.cleanupOrphanStages()
		a.fileQueue = q
		go q.run(a.lifetimeCtx)
	})
	return a.fileQueue
}
func (q *FileQueue) cleanupOrphanStages() {
	dir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	dir = filepath.Join(dir, "Sonoryx", "file-staging")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	used := map[string]bool{}
	for _, t := range q.tasks {
		if t.Staged && t.State != "done" && t.State != "cancelled" {
			used[t.Path] = true
		}
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if !used[path] && entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), "staged-") {
			if stat, err := entry.Info(); err == nil && time.Since(stat.ModTime()) > 24*time.Hour {
				_ = os.Remove(path)
			}
		}
	}
}
func (q *FileQueue) saveLocked() error {
	if q.path == "" {
		return errors.New("file queue storage unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(q.path), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(q.tasks)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(q.path), ".file-queue-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_ = f.Chmod(0600)
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = filetransfer.CommitFile(f.Name(), q.path)
	}
	return err
}
func (q *FileQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
func (q *FileQueue) List(expected string) []FileTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	list := []FileTask{}
	for _, t := range q.tasks {
		if t.Context == expected {
			list = append(list, t)
		}
	}
	return list
}
func (q *FileQueue) Add(t FileTask) (FileTask, error) {
	if t.Direction != "upload" && t.Direction != "download" {
		return t, errors.New("invalid transfer direction")
	}
	if _, err := q.app.fileConnection(t.Context); err != nil {
		return t, err
	}
	if t.Direction == "upload" {
		f, err := os.Open(t.Path)
		if err != nil {
			return t, err
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil || !stat.Mode().IsRegular() {
			return t, errors.New("source must be a regular file")
		}
		t.Size = stat.Size()
		if t.Name == "" {
			t.Name = filetransfer.SafeName(stat.Name())
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		caps, err := q.app.FileCapabilities(ctx, t.Context)
		cancel()
		if err != nil {
			return t, err
		}
		maxSize, ok := caps["maxFileBytes"].(float64)
		if !ok || t.Size > int64(maxSize) {
			return t, errors.New("file exceeds server size limit")
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	// Terminal entries are not shown in chat and must not fill the queue.
	kept := q.tasks[:0]
	for _, old := range q.tasks {
		_, active := q.active[old.ID]
		if !active && (old.State == "done" || old.State == "cancelled") {
			q.cleanupStaged(old)
			continue
		}
		kept = append(kept, old)
	}
	clear(q.tasks[len(kept):])
	q.tasks = kept
	if len(q.tasks) >= 256 {
		return t, errors.New("file queue full")
	}
	if t.Direction == "upload" {
		count := 0
		for _, old := range q.tasks {
			if old.Context == t.Context && old.Direction == "upload" && old.Kind == t.Kind && old.TargetID == t.TargetID && old.State != "done" && old.State != "cancelled" {
				count++
			}
		}
		if count >= domain.MaxChatAttachments {
			return t, errors.New("maximum four attachments per draft")
		}
	}
	t.ID = fmt.Sprintf("%x", time.Now().UnixNano())
	t.State = "queued"
	q.tasks = append(q.tasks, t)
	if err := q.saveLocked(); err != nil {
		q.tasks = q.tasks[:len(q.tasks)-1]
		return t, err
	}
	q.signal()
	return t, nil
}
func (q *FileQueue) Control(expected, id, action string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.tasks {
		t := &q.tasks[i]
		if t.ID != id || t.Context != expected {
			continue
		}
		switch action {
		case "attached":
			if t.State == "done" {
				return nil
			}
			if t.State != "ready" {
				return errors.New("transfer not ready")
			}
			t.State = "done"
			if err := q.saveLocked(); err != nil {
				t.State = "ready"
				return err
			}
			q.cleanupStaged(*t)
			return nil
		case "pause":
			t.State = "paused"
		case "resume":
			if t.State == "done" || t.State == "ready" {
				return nil
			}
			t.State = "queued"
			t.Error = ""
			t.Attempts = 0
			t.RetryAt = 0
		case "cancel":
			t.State = "cancelled"
		case "remove":
			if _, ok := q.active[id]; ok {
				return errors.New("pause transfer before removing")
			}
			if t.State != "done" && t.State != "cancelled" {
				return errors.New("cancel transfer before removing")
			}
			q.cleanupStaged(*t)
			q.tasks = append(q.tasks[:i], q.tasks[i+1:]...)
			return q.saveLocked()
		default:
			return errors.New("invalid transfer action")
		}
		if cancel := q.active[id]; cancel != nil {
			cancel()
		} else if action == "cancel" {
			snapshot := *t
			go q.cancelTask(snapshot)
		}
		if err := q.saveLocked(); err != nil {
			return err
		}
		q.signal()
		return nil
	}
	return errors.New("transfer not found")
}
func (q *FileQueue) update(t FileTask) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.tasks {
		if q.tasks[i].ID == t.ID {
			if q.tasks[i].State == "paused" || q.tasks[i].State == "cancelled" {
				return context.Canceled
			}
			q.tasks[i] = t
			return q.saveLocked()
		}
	}
	return context.Canceled
}
func (q *FileQueue) run(ctx context.Context) {
	defer close(q.done)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	defer func() {
		q.mu.Lock()
		for _, cancel := range q.active {
			cancel()
		}
		q.mu.Unlock()
		q.workers.Wait()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-q.wake:
		}
		q.mu.Lock()
		for i := range q.tasks {
			t := q.tasks[i]
			if len(q.active) >= 2 {
				break
			}
			if t.State != "queued" {
				continue
			}
			if time.Now().UnixMilli() < t.RetryAt {
				continue
			}
			if _, ok := q.active[t.ID]; ok {
				continue
			}
			if _, err := q.app.fileConnection(t.Context); err != nil {
				continue
			}
			runCtx, cancel := context.WithCancel(ctx)
			q.active[t.ID] = cancel
			q.tasks[i].State = "running"
			t.State = "running"
			q.workers.Add(1)
			go q.execute(runCtx, t)
		}
		q.mu.Unlock()
	}
}
func (q *FileQueue) execute(ctx context.Context, t FileTask) {
	defer q.workers.Done()
	var err error
	if t.Direction == "upload" {
		err = q.upload(ctx, &t)
	} else {
		err = q.download(ctx, &t)
	}
	q.mu.Lock()
	for i := range q.tasks {
		if q.tasks[i].ID != t.ID {
			continue
		}
		old := q.tasks[i]
		if old.State == "cancelled" || old.State == "paused" {
			t.State = old.State
		} else if old.State == "queued" && errors.Is(err, context.Canceled) {
			t.State = "queued"
		} else if err != nil {
			t.State = "error"
			t.Error = err.Error()
			if _, e := q.app.fileConnection(t.Context); e != nil || q.app.lifetimeCtx.Err() != nil {
				t.State = "queued"
			} else if (retryableFileError(err) || errors.Is(err, context.Canceled)) && t.Attempts < 5 {
				t.Attempts++
				t.RetryAt = time.Now().Add(time.Duration(1<<t.Attempts) * time.Second).UnixMilli()
				t.State = "queued"
			}
		}
		q.tasks[i] = t
		break
	}
	delete(q.active, t.ID)
	saveErr := q.saveLocked()
	q.mu.Unlock()
	if saveErr != nil {
		q.app.logger.Printf("file queue checkpoint failed: %v", saveErr)
	}
	if t.State == "cancelled" {
		q.cancelTask(t)
	}
	q.signal()
}
func (q *FileQueue) Wait(ctx context.Context) error {
	select {
	case <-q.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (q *FileQueue) cancelTask(t FileTask) {
	defer q.cleanupStaged(t)
	if t.Direction == "download" {
		_ = os.Remove(t.Path + ".sonoryx-" + t.ID + ".part")
		return
	}
	if t.UploadID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = q.app.fileJSON(ctx, t.Context, "DELETE", "/files/v1/uploads/"+t.UploadID, nil, nil)
	}
}
func (q *FileQueue) cleanupStaged(t FileTask) {
	if !t.Staged {
		return
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	if filepath.Dir(t.Path) == filepath.Join(dir, "Sonoryx", "file-staging") {
		_ = os.Remove(t.Path)
	}
}
func (a *App) FileAccountRights(ctx context.Context, expected, accountID string) (map[string]bool, error) {
	id, err := strconv.ParseInt(accountID, 10, 64)
	if err != nil || id <= 0 {
		return nil, errors.New("invalid account ID")
	}
	var out map[string]bool
	_, err = a.fileJSON(ctx, expected, "GET", "/files/v1/admin/rights?id="+strconv.FormatInt(id, 10), nil, &out)
	return out, err
}

func (a *App) FileAdmin(ctx context.Context, expected, method, cursor string, input any) (map[string]any, error) {
	var out map[string]any
	path := "/files/v1/admin"
	if cursor != "" {
		if _, err := strconv.ParseInt(cursor, 10, 64); err != nil {
			return nil, err
		}
		path += "?cursor=" + cursor
	}
	var output any
	if method == "GET" {
		output = &out
	}
	_, err := a.fileJSON(ctx, expected, method, path, input, output)
	return out, err
}

type fileConnection struct {
	connContext context.Context
	endpoint    netip.AddrPort
	session     uint64
}

func (a *App) fileConnection(expected string) (fileConnection, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentConn == nil || a.chatSession == nil || a.chatSession.ctx.Err() != nil || a.state.ConnectionStatus() != voiceclient.ConnectionConnected {
		return fileConnection{}, ErrNotConnected
	}
	if expected != a.chatContextLocked()+"|"+strconv.FormatInt(a.state.SnapshotView().UserID, 10) {
		return fileConnection{}, errors.New("file server or account changed")
	}
	return fileConnection{a.chatSession.ctx, a.serverEndpoint, a.state.SessionID()}, nil
}
func (a *App) fileClient(ctx context.Context, expected string) (*http.Client, string, string, func(), error) {
	c, err := a.fileConnection(expected)
	if err != nil {
		return nil, "", "", func() {}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.connContext, cancel)
	cleanup := func() { stop(); cancel() }
	a.mu.Lock()
	conn := a.currentConn
	a.mu.Unlock()
	response, err := voiceclient.DoRequest(ctx, conn, a.state, protocol.VoicePacket{Type: protocol.PacketFileAccessRequest}, time.Second)
	if err != nil {
		cleanup()
		return nil, "", "", func() {}, err
	}
	var access protocol.FileAccess
	if response.Type != protocol.PacketFileAccessAck || json.Unmarshal(response.Payload, &access) != nil || access.Port == 0 || access.Fingerprint == "" {
		cleanup()
		return nil, "", "", func() {}, errors.New("file transfer is disabled on this server")
	}
	key := netip.AddrPortFrom(c.endpoint.Addr(), access.Port).String() + "|" + normalizeFingerprint(access.Fingerprint)
	a.fileTransportMu.Lock()
	if a.fileTransport == nil || a.fileTransportKey != key {
		if a.fileTransport != nil {
			a.fileTransport.CloseIdleConnections()
		}
		a.fileTransport = pinnedFileTransport(access.Fingerprint)
		a.fileTransportKey = key
	}
	transport := a.fileTransport
	a.fileTransportMu.Unlock()
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, "https://" + netip.AddrPortFrom(c.endpoint.Addr(), access.Port).String(), access.Credential, cleanup, nil
}
func pinnedFileTransport(pin string) *http.Transport {
	return &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, VerifyConnection: func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 || certificateFingerprint(cs.PeerCertificates[0]) != normalizeFingerprint(pin) {
			return errors.New("file server TLS identity changed")
		}
		return nil
	}}, ResponseHeaderTimeout: 15 * time.Second, MaxResponseHeaderBytes: 16 << 10, IdleConnTimeout: 30 * time.Second, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 8}
}

var errFileOffsetChanged = errors.New("file upload offset changed; reconcile before retry")

func (a *App) fileRequest(ctx context.Context, expected, method, path string, body io.Reader, headers map[string]string) (*http.Response, func(), error) {
	var start int64
	seeker, replayable := body.(io.Seeker)
	if replayable {
		var err error
		start, err = seeker.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, func() {}, err
		}
	}
	for attempt := 0; ; attempt++ {
		response, close, err := a.fileRequestAuthenticated(ctx, expected, method, path, body, headers)
		var status *fileHTTPError
		if !errors.As(err, &status) || status.status != http.StatusTooManyRequests || attempt >= 4 || (body != nil && !replayable) {
			return response, close, err
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 300 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, func() {}, ctx.Err()
		case <-timer.C:
		}
		if replayable {
			if _, err = seeker.Seek(start, io.SeekStart); err != nil {
				return nil, func() {}, err
			}
		}
	}
}

func (a *App) fileRequestAuthenticated(ctx context.Context, expected, method, path string, body io.Reader, headers map[string]string) (*http.Response, func(), error) {
	var start int64
	seeker, replayable := body.(io.Seeker)
	if replayable {
		var err error
		start, err = seeker.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, func() {}, err
		}
	}
	response, close, err := a.fileRequestOnce(ctx, expected, method, path, body, headers)
	var httpErr *fileHTTPError
	if !errors.As(err, &httpErr) || httpErr.status != 401 || ctx.Err() != nil {
		return response, close, err
	}
	if body != nil && !replayable {
		return response, close, err
	}
	if method == "PATCH" {
		var upload persist.FileUpload
		if _, e := a.fileJSON(ctx, expected, "GET", strings.TrimSuffix(path, "/content"), nil, &upload); e != nil {
			return nil, func() {}, e
		}
		offset, e := strconv.ParseInt(headers["Upload-Offset"], 10, 64)
		if e != nil || upload.Offset != offset {
			return nil, func() {}, errFileOffsetChanged
		}
	}
	if replayable {
		if _, e := seeker.Seek(start, io.SeekStart); e != nil {
			return nil, func() {}, e
		}
	}
	return a.fileRequestOnce(ctx, expected, method, path, body, headers)
}

func (a *App) fileRequestOnce(ctx context.Context, expected, method, path string, body io.Reader, headers map[string]string) (*http.Response, func(), error) {
	connection, err := a.fileConnection(expected)
	if err != nil {
		return nil, func() {}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	stop := context.AfterFunc(connection.connContext, cancel)
	// Metadata, previews and transfers share a budget below the server's per-account limit.
	a.fileTransportMu.Lock()
	if a.fileRequestSlots == nil {
		a.fileRequestSlots = make(chan struct{}, 2)
	}
	slots := a.fileRequestSlots
	a.fileTransportMu.Unlock()
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		stop()
		cancel()
		return nil, func() {}, ctx.Err()
	}
	client, base, credential, closeClient, err := a.fileClient(ctx, expected)
	if err != nil {
		<-slots
		stop()
		cancel()
		return nil, func() {}, err
	}
	var release sync.Once
	cleanup := func() { release.Do(func() { closeClient(); stop(); cancel(); <-slots }) }
	request, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	request.Header.Set("X-Govts-Session", strconv.FormatUint(connection.session, 10))
	request.Header.Set("Authorization", "Bearer "+credential)
	for k, v := range headers {
		if k == "Content-Length" {
			request.ContentLength, _ = strconv.ParseInt(v, 10, 64)
		} else {
			request.Header.Set(k, v)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		response.Body.Close()
		cleanup()
		return nil, func() {}, &fileHTTPError{response.StatusCode, strings.TrimSpace(string(b))}
	}
	return response, func() { response.Body.Close(); cleanup() }, nil
}

type fileHTTPError struct {
	status  int
	message string
}

func (e *fileHTTPError) Error() string { return fmt.Sprintf("files HTTP %d: %s", e.status, e.message) }
func retryableFileError(err error) bool {
	var n net.Error
	var h *fileHTTPError
	return errors.Is(err, errFileOffsetChanged) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &n) || errors.As(err, &h) && (h.status == 429 || h.status >= 500)
}
func (a *App) fileJSON(ctx context.Context, expected, method, path string, input, output any) (int, error) {
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	response, close, err := a.fileRequest(ctx, expected, method, path, body, map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return 0, err
	}
	defer close()
	if output != nil {
		err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(output)
	}
	return response.StatusCode, err
}
func (a *App) FileMetadata(ctx context.Context, expected string, ids []string) ([]persist.FileAttachment, error) {
	var list []persist.FileAttachment
	_, err := a.fileJSON(ctx, expected, "POST", "/files/v1/metadata", ids, &list)
	return list, err
}
func (a *App) FileCapabilities(ctx context.Context, expected string) (map[string]any, error) {
	var caps map[string]any
	_, err := a.fileJSON(ctx, expected, "GET", "/files/v1/capabilities", nil, &caps)
	return caps, err
}
func (a *App) FilePreview(ctx context.Context, expected, id string) ([]byte, error) {
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return nil, err
	}
	response, close, err := a.fileRequest(ctx, expected, "GET", "/files/v1/attachments/"+id+"/preview", nil, nil)
	if err != nil {
		return nil, err
	}
	defer close()
	b, err := io.ReadAll(io.LimitReader(response.Body, (512<<10)+1))
	if len(b) > 512<<10 {
		return nil, errors.New("preview too large")
	}
	return b, err
}

// FileOriginalImage returns the original bytes, never a rescaled preview.
func (a *App) FileOriginalImage(ctx context.Context, expected, id string) ([]byte, string, error) {
	parsed, err := strconv.ParseInt(id, 10, 64)
	if err != nil || parsed <= 0 {
		return nil, "", errors.New("invalid attachment ID")
	}
	list, err := a.FileMetadata(ctx, expected, []string{id})
	if err != nil {
		return nil, "", err
	}
	const maxImageBytes = 32 << 20
	if len(list) != 1 || list[0].Deleted || list[0].Size <= 0 || list[0].Size > maxImageBytes {
		return nil, "", errors.New("image unavailable or exceeds 32 MiB")
	}
	response, close, err := a.fileRequest(ctx, expected, "GET", "/files/v1/attachments/"+id+"/content", nil, nil)
	if err != nil {
		return nil, "", err
	}
	defer close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) != list[0].Size || len(data) > maxImageBytes {
		return nil, "", errors.New("invalid original image size")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != list[0].SHA256 {
		return nil, "", errors.New("original image checksum mismatch")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return nil, "", errors.New("invalid image or exceeds 40 megapixels")
	}
	mime := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "webp": "image/webp"}[format]
	if mime == "" {
		return nil, "", errors.New("unsupported image format")
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	return data, mime, nil
}

func (q *FileQueue) upload(ctx context.Context, t *FileTask) error {
	// A completed server upload no longer needs its local source file.
	if t.UploadID != "" {
		var status persist.FileUpload
		if _, err := q.app.fileJSON(ctx, t.Context, "GET", "/files/v1/uploads/"+t.UploadID, nil, &status); err != nil {
			return err
		}
		if status.State == "ready" || status.State == "attached" || status.State == "verifying" {
			return q.waitVerified(ctx, t, status)
		}
		if status.State != "uploading" {
			return errors.New("upload expired or cancelled; select file again")
		}
	}
	f, err := os.Open(t.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	if stat.Size() != t.Size {
		return errors.New("source file size changed")
	}
	h := sha256.New()
	if _, err = io.Copy(h, &cancelReader{ctx, f}); err != nil {
		return err
	}
	hash := hex.EncodeToString(h.Sum(nil))
	if t.SHA256 != "" && t.SHA256 != hash {
		return errors.New("source file changed")
	}
	t.SHA256 = hash
	var upload persist.FileUpload
	if t.UploadID == "" {
		_, err = q.app.fileJSON(ctx, t.Context, "POST", "/files/v1/uploads", persist.FileUpload{RequestID: t.ID, Kind: t.Kind, TargetID: t.TargetID, Name: t.Name, Size: t.Size, SHA256: t.SHA256}, &upload)
	} else {
		_, err = q.app.fileJSON(ctx, t.Context, "GET", "/files/v1/uploads/"+t.UploadID, nil, &upload)
	}
	if err != nil {
		return err
	}
	t.UploadID = upload.ID
	t.Offset = upload.Offset
	if err = q.update(*t); err != nil {
		return err
	}
	if upload.State == "ready" || upload.State == "attached" || upload.State == "verifying" {
		return q.waitVerified(ctx, t, upload)
	}
	if upload.State != "uploading" {
		return errors.New("upload expired or cancelled; select file again")
	}
	if _, err = f.Seek(t.Offset, 0); err != nil {
		return err
	}
	buffer := make([]byte, filetransfer.ChunkSize)
	for t.Offset < t.Size {
		n, e := io.ReadFull(f, buffer[:min(int64(len(buffer)), t.Size-t.Offset)])
		if e != nil {
			return e
		}
		chunk := buffer[:n]
		sum := sha256.Sum256(chunk)
		response, close, e := q.app.fileRequest(ctx, t.Context, "PATCH", "/files/v1/uploads/"+t.UploadID+"/content", bytes.NewReader(chunk), map[string]string{"Upload-Offset": strconv.FormatInt(t.Offset, 10), "Upload-Checksum": hex.EncodeToString(sum[:])})
		if e != nil {
			return e
		}
		e = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&upload)
		close()
		if e != nil {
			return e
		}
		if upload.Offset != t.Offset+int64(n) {
			return errors.New("invalid upload checkpoint")
		}
		t.Offset = upload.Offset
		if e = q.update(*t); e != nil {
			return e
		}
	}
	_, err = q.app.fileJSON(ctx, t.Context, "POST", "/files/v1/uploads/"+t.UploadID+"/complete", nil, &upload)
	if err != nil {
		return err
	}
	return q.waitVerified(ctx, t, upload)
}

func (q *FileQueue) waitVerified(ctx context.Context, t *FileTask, upload persist.FileUpload) error {
	deadline := time.NewTimer(31 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if upload.ID != t.UploadID || upload.Size != t.Size || upload.SHA256 != t.SHA256 {
			return errors.New("upload metadata changed")
		}
		switch upload.State {
		case "ready", "attached":
			if upload.AttachmentID <= 0 {
				return errors.New("invalid verified attachment")
			}
			t.Offset = upload.Size
			t.AttachmentID = upload.AttachmentID
			t.State = "ready"
			if upload.State == "attached" {
				t.State = "done"
				q.cleanupStaged(*t)
			}
			return nil
		case "verifying":
			if t.State != "verifying" {
				t.State = "verifying"
				if err := q.update(*t); err != nil {
					return err
				}
			}
		default:
			if upload.Error != "" {
				return errors.New(upload.Error)
			}
			return errors.New("upload verification failed or cancelled")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("upload verification timed out")
		case <-ticker.C:
		}
		if _, err := q.app.fileJSON(ctx, t.Context, "GET", "/files/v1/uploads/"+t.UploadID, nil, &upload); err != nil {
			return err
		}
	}
}
func (q *FileQueue) download(ctx context.Context, t *FileTask) error {
	metadata, err := q.app.FileMetadata(ctx, t.Context, []string{strconv.FormatInt(t.AttachmentID, 10)})
	if err != nil {
		return err
	}
	if len(metadata) != 1 || metadata[0].Deleted {
		return errors.New("attachment deleted")
	}
	att := metadata[0]
	if t.SHA256 != "" && t.SHA256 != att.SHA256 {
		return errors.New("attachment content changed")
	}
	t.SHA256 = att.SHA256
	t.Size = att.Size
	path := t.Path + ".sonoryx-" + t.ID + ".part"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	offset := min(stat.Size(), t.Offset)
	if offset > t.Size {
		return errors.New("invalid partial file")
	}
	if err = f.Truncate(offset); err != nil {
		return err
	}
	if _, err = f.Seek(offset, 0); err != nil {
		return err
	}
	for offset < t.Size {
		end := min(offset+filetransfer.ChunkSize, t.Size) - 1
		response, close, e := q.app.fileRequest(ctx, t.Context, "GET", "/files/v1/attachments/"+strconv.FormatInt(t.AttachmentID, 10)+"/content", nil, map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", offset, end), "If-Range": "\"" + t.SHA256 + "\""})
		if e != nil {
			return e
		}
		if response.StatusCode != http.StatusPartialContent || response.Header.Get("ETag") != "\""+t.SHA256+"\"" || response.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", offset, end, t.Size) {
			close()
			return errors.New("invalid download range")
		}
		n, e := io.CopyN(f, &cancelReader{ctx, response.Body}, end-offset+1)
		close()
		if e != nil {
			_ = f.Truncate(offset)
			return e
		}
		if e = f.Sync(); e != nil {
			return e
		}
		offset += n
		t.Offset = offset
		if e = q.update(*t); e != nil {
			return e
		}
	}
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	h := sha256.New()
	if _, err = io.Copy(h, &cancelReader{ctx, f}); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != t.SHA256 {
		_ = f.Truncate(0)
		t.Offset = 0
		return errors.New("download checksum mismatch")
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = filetransfer.CommitFile(path, t.Path); err != nil {
		return err
	}
	t.State = "done"
	return nil
}

type cancelReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *cancelReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(b)
}
