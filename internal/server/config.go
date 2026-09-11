package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

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

type ChannelSource interface {
	Load(context.Context) ([]ChannelDefinition, error)
}

type BuiltinChannelSource struct{}

func (BuiltinChannelSource) Load(ctx context.Context) ([]ChannelDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []ChannelDefinition{{
		Name:     "main",
		Topic:    "Main voice channel",
		Position: 10,
	}}, nil
}

type JSONChannelSource struct {
	Path string
}

type ServerConfig struct {
	Channels []ChannelSpec `json:"channels"`
}

type ChannelSpec struct {
	Name        string        `json:"name"`
	Topic       string        `json:"topic,omitempty"`
	Description string        `json:"description,omitempty"`
	Position    uint32        `json:"position,omitempty"`
	MaxUsers    uint32        `json:"max_users,omitempty"`
	Children    []ChannelSpec `json:"children,omitempty"`
}

func (source JSONChannelSource) Load(ctx context.Context) ([]ChannelDefinition, error) {
	if source.Path == "" {
		return nil, errors.New("server config path is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	file, err := os.Open(source.Path)
	if err != nil {
		return nil, fmt.Errorf("open server config %q: %w", source.Path, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, MaxServerConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read server config %q: %w", source.Path, err)
	}
	if len(data) > MaxServerConfigBytes {
		return nil, fmt.Errorf(
			"server config %q exceeds %d bytes",
			source.Path,
			MaxServerConfigBytes,
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var config ServerConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode server config %q: %w", source.Path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON documents are not allowed")
		}
		return nil, fmt.Errorf("decode server config %q trailing data: %w", source.Path, err)
	}

	return definitionsFromSpecs(config.Channels), nil
}

func BootstrapHub(ctx context.Context, source ChannelSource) (*voice.Hub, error) {
	if source == nil {
		return nil, errors.New("channel source is required")
	}
	definitions, err := source.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load channel definitions: %w", err)
	}
	if err := validateBootstrapDefinitions(ctx, definitions); err != nil {
		return nil, err
	}

	hub := voice.NewHub()
	for index, definition := range definitions {
		path := fmt.Sprintf("channels[%d]", index)
		if err := createChannelTree(ctx, hub, 0, definition, path); err != nil {
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
