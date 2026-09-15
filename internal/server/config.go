package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"example.com/go-voice-mvp/internal/domain"
	"example.com/go-voice-mvp/internal/voice"
)

const (
	MaxServerConfigBytes = 64 * 1024
	MaxBootstrapChannels = 256
)

type ChannelDefinition struct {
	Name        string
	Topic       string
	Description string
	Position    uint32
	MaxUsers    uint32
	Children    []ChannelDefinition
}

type ServerDefinition struct {
	Info     domain.ServerInfo
	Channels []ChannelDefinition
}

type BootstrapSource interface {
	Load(context.Context) (ServerDefinition, error)
}

type BuiltinBootstrapSource struct{}

func (BuiltinBootstrapSource) Load(ctx context.Context) (ServerDefinition, error) {
	if err := ctx.Err(); err != nil {
		return ServerDefinition{}, err
	}
	return ServerDefinition{Info: domain.ServerInfo{Name: voice.DefaultServerName}, Channels: []ChannelDefinition{{
		Name: voice.DefaultChannelName,
	}}}, nil
}

type JSONBootstrapSource struct {
	Path string
}

type ServerConfig struct {
	Server   ServerSpec    `json:"server"`
	Channels []ChannelSpec `json:"channels"`
}

type ServerSpec struct {
	Name string `json:"name"`
}

type ChannelSpec struct {
	Name        string        `json:"name"`
	Topic       string        `json:"topic,omitempty"`
	Description string        `json:"description,omitempty"`
	Position    uint32        `json:"position,omitempty"`
	MaxUsers    uint32        `json:"max_users,omitempty"`
	Children    []ChannelSpec `json:"children,omitempty"`
}

func (source JSONBootstrapSource) Load(ctx context.Context) (ServerDefinition, error) {
	if source.Path == "" {
		return ServerDefinition{}, errors.New("server config path is required")
	}
	if err := ctx.Err(); err != nil {
		return ServerDefinition{}, err
	}

	file, err := os.Open(source.Path)
	if err != nil {
		return ServerDefinition{}, fmt.Errorf("open server config %q: %w", source.Path, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, MaxServerConfigBytes+1))
	if err != nil {
		return ServerDefinition{}, fmt.Errorf("read server config %q: %w", source.Path, err)
	}
	if len(data) > MaxServerConfigBytes {
		return ServerDefinition{}, fmt.Errorf(
			"server config %q exceeds %d bytes",
			source.Path,
			MaxServerConfigBytes,
		)
	}
	if err := ctx.Err(); err != nil {
		return ServerDefinition{}, err
	}
	if !utf8.Valid(data) {
		return ServerDefinition{}, fmt.Errorf("decode server config %q: input is not valid UTF-8", source.Path)
	}

	var config ServerConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return ServerDefinition{}, fmt.Errorf("decode server config %q: %w", source.Path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON documents are not allowed")
		}
		return ServerDefinition{}, fmt.Errorf("decode server config %q trailing data: %w", source.Path, err)
	}
	if !utf8.ValidString(config.Server.Name) || config.Server.Name == "" || len(config.Server.Name) > domain.MaxServerNameBytes || strings.TrimSpace(config.Server.Name) != config.Server.Name {
		return ServerDefinition{}, fmt.Errorf("decode server config %q: server name must be valid UTF-8, trimmed, and 1..%d bytes", source.Path, domain.MaxServerNameBytes)
	}
	if len(config.Channels) == 0 {
		return ServerDefinition{}, fmt.Errorf("decode server config %q: channels must contain at least one channel", source.Path)
	}

	return ServerDefinition{Info: domain.ServerInfo{Name: config.Server.Name}, Channels: definitionsFromSpecs(config.Channels)}, nil
}

func BootstrapHub(ctx context.Context, source BootstrapSource) (*voice.Hub, error) {
	if source == nil {
		return nil, errors.New("bootstrap source is required")
	}
	definition, err := source.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load server definition: %w", err)
	}
	if err := validateBootstrapDefinitions(ctx, definition.Channels); err != nil {
		return nil, err
	}
	if len(definition.Channels) == 0 {
		return nil, errors.New("server definition must contain at least one channel")
	}

	hub, err := voice.NewEmptyHubWithServerInfo(definition.Info)
	if err != nil {
		return nil, fmt.Errorf("validate server definition: %w", err)
	}
	for index, channel := range definition.Channels {
		path := fmt.Sprintf("channels[%d]", index)
		if err := createChannelTree(ctx, hub, 0, channel, path); err != nil {
			return nil, err
		}
	}
	return hub, nil
}

func validateBootstrapDefinitions(ctx context.Context, definitions []ChannelDefinition) error {
	count := 0
	var walk func([]ChannelDefinition, int, string) error
	walk = func(items []ChannelDefinition, depth int, path string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for index, item := range items {
			count++
			itemPath := fmt.Sprintf("%s[%d]", path, index)
			if depth > domain.MaxChannelDepth {
				return fmt.Errorf(
					"%s: channel depth exceeds %d",
					itemPath,
					domain.MaxChannelDepth,
				)
			}
			if count > MaxBootstrapChannels {
				return fmt.Errorf(
					"%s: bootstrap channel count exceeds %d",
					itemPath,
					MaxBootstrapChannels,
				)
			}
			if err := walk(item.Children, depth+1, itemPath+".children"); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(definitions, 1, "channels")
}

func createChannelTree(
	ctx context.Context,
	hub *voice.Hub,
	parentID domain.ChannelID,
	definition ChannelDefinition,
	path string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	channel, err := hub.CreateChannel(domain.Channel{
		ParentID:    parentID,
		Name:        definition.Name,
		Topic:       definition.Topic,
		Description: definition.Description,
		Position:    definition.Position,
		MaxUsers:    definition.MaxUsers,
	})
	if err != nil {
		return fmt.Errorf("%s (%q): %w", path, definition.Name, err)
	}
	for index, child := range definition.Children {
		childPath := fmt.Sprintf("%s.children[%d]", path, index)
		if err := createChannelTree(ctx, hub, channel.ID, child, childPath); err != nil {
			return err
		}
	}
	return nil
}

func definitionsFromSpecs(specs []ChannelSpec) []ChannelDefinition {
	definitions := make([]ChannelDefinition, len(specs))
	for index, spec := range specs {
		definitions[index] = ChannelDefinition{
			Name:        spec.Name,
			Topic:       spec.Topic,
			Description: spec.Description,
			Position:    spec.Position,
			MaxUsers:    spec.MaxUsers,
			Children:    definitionsFromSpecs(spec.Children),
		}
	}
	return definitions
}
