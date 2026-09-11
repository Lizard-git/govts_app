package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/go-voice-mvp/internal/domain"
)

type channelSourceFunc func(context.Context) ([]ChannelDefinition, error)

func (function channelSourceFunc) Load(ctx context.Context) ([]ChannelDefinition, error) {
	return function(ctx)
}

func TestBootstrapHubUsesBuiltinMainChannel(t *testing.T) {
	hub, err := BootstrapHub(context.Background(), BuiltinChannelSource{})
	if err != nil {
		t.Fatal(err)
	}
	channels := hub.ListChannels()
	if len(channels) != 2 {
		t.Fatalf("channel count = %d, want 2", len(channels))
	}
	main, err := hub.FindChannelByName("main")
	if err != nil {
		t.Fatal(err)
	}
	if main.ID != 2 || main.ParentID != 0 || main.Position != 10 {
		t.Fatalf("main channel = %+v", main)
	}

	alice, err := hub.CreateSession("alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := hub.CreateSession("bob", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.JoinChannelByName(alice.ID, "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.JoinChannelByName(bob.ID, "main"); err != nil {
		t.Fatal(err)
	}
	recipients, err := hub.RecipientsFor(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 1 || recipients[0].ID != bob.ID {
		t.Fatalf("voice recipients = %+v, want Bob", recipients)
	}
}

func TestJSONChannelSourceBuildsParentFirstTree(t *testing.T) {
	path := writeTestConfig(t, `{
  "channels": [
    {
      "name": "main",
      "topic": "Main topic",
      "description": "Main description",
      "position": 20,
      "max_users": 50,
      "children": [
        {"name": "gaming", "position": 10, "max_users": 10},
        {"name": "music", "position": 20}
      ]
    },
    {"name": "afk", "position": 10}
  ]
}`)

	hub, err := BootstrapHub(context.Background(), JSONChannelSource{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	main, err := hub.FindChannelByName("main")
	if err != nil {
		t.Fatal(err)
	}
	gaming, err := hub.FindChannelByName("gaming")
	if err != nil {
		t.Fatal(err)
	}
	music, err := hub.FindChannelByName("music")
	if err != nil {
		t.Fatal(err)
	}
	afk, err := hub.FindChannelByName("afk")
	if err != nil {
		t.Fatal(err)
	}

	if main.ID != 2 || gaming.ID != 3 || music.ID != 4 || afk.ID != 5 {
		t.Fatalf("parent-first IDs = main:%d gaming:%d music:%d afk:%d", main.ID, gaming.ID, music.ID, afk.ID)
	}
	if gaming.ParentID != main.ID || music.ParentID != main.ID || afk.ParentID != 0 {
		t.Fatalf("unexpected parents: gaming=%d music=%d afk=%d", gaming.ParentID, music.ParentID, afk.ParentID)
	}
	if main.Topic != "Main topic" || main.Description != "Main description" || main.MaxUsers != 50 {
		t.Fatalf("main metadata = %+v", main)
	}

	channels := hub.ListChannels()
	wantIDs := []domain.ChannelID{1, afk.ID, main.ID, gaming.ID, music.ID}
	for index, wantID := range wantIDs {
		if channels[index].ID != wantID {
			t.Fatalf("sorted channel[%d] ID = %d, want %d", index, channels[index].ID, wantID)
		}
	}
}

func TestJSONChannelSourceAcceptsEmptyChannelList(t *testing.T) {
	path := writeTestConfig(t, `{"channels": []}`)
	hub, err := BootstrapHub(context.Background(), JSONChannelSource{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if channels := hub.ListChannels(); len(channels) != 1 || channels[0].Name != "default" {
		t.Fatalf("channels = %+v, want only default", channels)
	}
}

func TestJSONChannelSourceRejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "empty", content: "", want: "decode server config"},
		{name: "malformed", content: `{`, want: "decode server config"},
		{name: "unknown root field", content: `{"unknown": true}`, want: "unknown field"},
		{name: "unknown channel field", content: `{"channels":[{"name":"main","unknown":true}]}`, want: "unknown field"},
		{name: "second document", content: `{"channels":[]} {"channels":[]}`, want: "multiple JSON documents"},
		{name: "trailing garbage", content: `{"channels":[]} trailing`, want: "trailing data"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeTestConfig(t, test.content)
			_, err := (JSONChannelSource{Path: path}).Load(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestJSONChannelSourceRejectsOversizedFile(t *testing.T) {
	path := writeTestConfig(t, strings.Repeat(" ", MaxServerConfigBytes+1))
	_, err := (JSONChannelSource{Path: path}).Load(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Load() error = %v, want size error", err)
	}
}

func TestBootstrapHubRejectsInvalidDefinitionsAtomically(t *testing.T) {
	tooMany := make([]ChannelDefinition, MaxBootstrapChannels+1)
	for index := range tooMany {
		tooMany[index].Name = "channel-" + strings.Repeat("x", index%8) + string(rune('a'+index%26))
		tooMany[index].Position = uint32(index)
	}
	tooDeep := ChannelDefinition{Name: "depth-1"}
	cursor := &tooDeep
	for depth := 2; depth <= domain.MaxChannelDepth+1; depth++ {
		cursor.Children = []ChannelDefinition{{Name: "depth"}}
		cursor = &cursor.Children[0]
	}

	tests := []struct {
		name        string
		definitions []ChannelDefinition
		want        string
	}{
		{
			name: "duplicate sibling",
			definitions: []ChannelDefinition{
				{Name: "main"},
				{Name: "MAIN"},
			},
			want: "channels[1]",
		},
		{name: "invalid metadata", definitions: []ChannelDefinition{{Name: " main"}}, want: "channels[0]"},
		{name: "too deep", definitions: []ChannelDefinition{tooDeep}, want: "depth exceeds"},
		{name: "too many", definitions: tooMany, want: "count exceeds"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := channelSourceFunc(func(context.Context) ([]ChannelDefinition, error) {
				return test.definitions, nil
			})
			hub, err := BootstrapHub(context.Background(), source)
			if hub != nil {
				t.Fatalf("BootstrapHub() returned partial Hub: %+v", hub.ListChannels())
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("BootstrapHub() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestBootstrapHubPropagatesContextAndSourceErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if hub, err := BootstrapHub(ctx, BuiltinChannelSource{}); hub != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled BootstrapHub() = (%v, %v)", hub, err)
	}

	wantErr := errors.New("source failed")
	source := channelSourceFunc(func(context.Context) ([]ChannelDefinition, error) {
		return nil, wantErr
	})
	if hub, err := BootstrapHub(context.Background(), source); hub != nil || !errors.Is(err, wantErr) {
		t.Fatalf("failed BootstrapHub() = (%v, %v)", hub, err)
	}
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
