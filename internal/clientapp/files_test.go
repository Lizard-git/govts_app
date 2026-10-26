package clientapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"uniclog.io/sonoryx/internal/persist"
)

func TestVerifiedUploadRejectsInvalidMetadata(t *testing.T) {
	for _, state := range []string{"ready", "attached"} {
		for _, field := range []string{"id", "size", "hash", "attachment"} {
			t.Run(state+"/"+field, func(t *testing.T) {
				task := FileTask{UploadID: "upload", Size: 10, SHA256: "hash", State: "running"}
				upload := persist.FileUpload{ID: "upload", Size: 10, SHA256: "hash", State: state, AttachmentID: 1}
				switch field {
				case "id":
					upload.ID = "other"
				case "size":
					upload.Size++
				case "hash":
					upload.SHA256 = "other"
				case "attachment":
					upload.AttachmentID = 0
				}
				if err := (&FileQueue{}).waitVerified(context.Background(), &task, upload); err == nil {
					t.Fatal("accepted invalid completed upload")
				}
				if task.State != "running" || task.AttachmentID != 0 {
					t.Fatal("invalid response changed the task")
				}
			})
		}
	}
}

func TestAttachmentCompletionRetriesFailedLocalCheckpoint(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	q := &FileQueue{path: filepath.Join(blocked, "queue.json"), tasks: []FileTask{{ID: "one", Context: "ctx", State: "ready"}}}
	if err := q.Control("ctx", "one", "attached"); err == nil {
		t.Fatal("accepted failed checkpoint")
	}
	if q.tasks[0].State != "ready" {
		t.Fatal("failed save prevented retry")
	}
	q.path = filepath.Join(t.TempDir(), "queue.json")
	if err := q.Control("ctx", "one", "attached"); err != nil {
		t.Fatal(err)
	}
	if err := q.Control("ctx", "one", "attached"); err != nil {
		t.Fatal("completion not idempotent", err)
	}
	data, err := os.ReadFile(q.path)
	if err != nil {
		t.Fatal(err)
	}
	var tasks []FileTask
	if err = json.Unmarshal(data, &tasks); err != nil || len(tasks) != 1 || tasks[0].State != "done" {
		t.Fatal("completion was not persisted", err)
	}
}

func TestFilePinCheckedBeforeSendingCredentialOrBody(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing credential")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "file bytes" {
			t.Error("unexpected body")
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	wrong := pinnedFileTransport(strings.Repeat("0", 64))
	defer wrong.CloseIdleConnections()
	client := &http.Client{Transport: wrong}
	request, _ := http.NewRequest("POST", server.URL, strings.NewReader("file bytes"))
	request.Header.Set("Authorization", "Bearer secret")
	if response, err := client.Do(request); err == nil {
		response.Body.Close()
		t.Fatal("accepted wrong pin")
	}
	if calls.Load() != 0 {
		t.Fatal("sent HTTP credentials before pin validation")
	}
	correct := pinnedFileTransport(certificateFingerprint(server.Certificate()))
	defer correct.CloseIdleConnections()
	client.Transport = correct
	request, _ = http.NewRequest("POST", server.URL, strings.NewReader("file bytes"))
	request.Header.Set("Authorization", "Bearer secret")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if calls.Load() != 1 {
		t.Fatal("correct pin request missing")
	}
}
