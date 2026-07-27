package core

import (
	"context"
	"fmt"
	"sort"
)

// Source collects a batch of messages. Implementations are constructed once
// at startup and Collect is called on every poll.
type Source interface {
	Collect(ctx context.Context) ([]Message, error)
}

// Destination receives batches of collected messages.
type Destination interface {
	Write(ctx context.Context, batch []Message) error
	Close(ctx context.Context) error
}

// SourceCtor builds a Source from its YAML `config` block.
type SourceCtor func(cfg Fields) (Source, error)

// DestinationCtor builds a Destination from its YAML `config` block.
type DestinationCtor func(cfg Fields) (Destination, error)

var (
	sourceTypes      = map[string]SourceCtor{}
	destinationTypes = map[string]DestinationCtor{}
)

// RegisterSource makes a source type available under the given name. Call it
// from an init function in the file implementing the type.
func RegisterSource(typeName string, ctor SourceCtor) {
	if _, exists := sourceTypes[typeName]; exists {
		panic(fmt.Sprintf("source type %q registered twice", typeName))
	}
	sourceTypes[typeName] = ctor
}

// RegisterDestination makes a destination type available under the given
// name. Call it from an init function in the file implementing the type.
func RegisterDestination(typeName string, ctor DestinationCtor) {
	if _, exists := destinationTypes[typeName]; exists {
		panic(fmt.Sprintf("destination type %q registered twice", typeName))
	}
	destinationTypes[typeName] = ctor
}

// NewSource instantiates a registered source type.
func NewSource(typeName string, cfg Fields) (Source, error) {
	ctor, ok := sourceTypes[typeName]
	if !ok {
		return nil, fmt.Errorf("unknown source type %q, available: %v", typeName, SourceTypes())
	}
	return ctor(cfg)
}

// NewDestination instantiates a registered destination type.
func NewDestination(typeName string, cfg Fields) (Destination, error) {
	ctor, ok := destinationTypes[typeName]
	if !ok {
		return nil, fmt.Errorf("unknown destination type %q, available: %v", typeName, DestinationTypes())
	}
	return ctor(cfg)
}

// SourceTypes lists all registered source type names.
func SourceTypes() []string {
	names := make([]string, 0, len(sourceTypes))
	for name := range sourceTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DestinationTypes lists all registered destination type names.
func DestinationTypes() []string {
	names := make([]string, 0, len(destinationTypes))
	for name := range destinationTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
