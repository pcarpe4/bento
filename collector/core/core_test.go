package core

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestParseConfigValidation(t *testing.T) {
	valid := []byte(`
sources:
  - name: a
    type: test_source
    destinations: [out]
destinations:
  - name: out
    type: test_dest
`)
	conf, err := ParseConfig(valid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conf.DefaultInterval != "5m" {
		t.Errorf("expected default interval 5m, got %v", conf.DefaultInterval)
	}

	for name, bad := range map[string]string{
		"no sources":          "destinations: [{name: out, type: t}]",
		"no destinations":     "sources: [{name: a, type: t}]",
		"missing source name": "sources: [{type: t}]\ndestinations: [{name: out, type: t}]",
		"unknown destination": "sources: [{name: a, type: t, destinations: [nope]}]\ndestinations: [{name: out, type: t}]",
		"duplicate source":    "sources: [{name: a, type: t}, {name: a, type: t}]\ndestinations: [{name: out, type: t}]",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig([]byte(bad)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("COLLECTOR_TEST_VAL", "secret")
	out := string(expandEnv([]byte("password: ${COLLECTOR_TEST_VAL} plain: $HOME missing: ${COLLECTOR_TEST_UNSET_VAL}")))
	expected := "password: secret plain: $HOME missing: "
	if out != expected {
		t.Errorf("expected %q, got %q", expected, out)
	}
}

func TestFields(t *testing.T) {
	f := Fields{
		"str":    "hello",
		"flag":   true,
		"list":   []any{"a", "b"},
		"single": "solo",
		"m":      map[string]any{"k": "v"},
		"dur":    "30s",
	}
	if v := f.String("str", ""); v != "hello" {
		t.Errorf("String: got %q", v)
	}
	if v := f.String("nope", "def"); v != "def" {
		t.Errorf("String default: got %q", v)
	}
	if !f.Bool("flag", false) {
		t.Error("Bool: expected true")
	}
	if v := f.StringList("list"); len(v) != 2 || v[0] != "a" {
		t.Errorf("StringList: got %v", v)
	}
	if v := f.StringList("single"); len(v) != 1 || v[0] != "solo" {
		t.Errorf("StringList single: got %v", v)
	}
	if v := f.StringMap("m"); v["k"] != "v" {
		t.Errorf("StringMap: got %v", v)
	}
	d, err := f.Duration("dur", 0)
	if err != nil || d != 30*time.Second {
		t.Errorf("Duration: got %v, %v", d, err)
	}
	if _, err := f.RequiredString("nope"); err == nil {
		t.Error("RequiredString: expected error")
	}
}

// memorySource emits one fixed message per collection.
type memorySource struct{}

func (memorySource) Collect(ctx context.Context) ([]Message, error) {
	return []Message{NewMessage(map[string]any{"value": 42})}, nil
}

// memoryDestination records everything written to it.
type memoryDestination struct {
	mu     sync.Mutex
	batch  []Message
	closed bool
}

func (m *memoryDestination) Write(ctx context.Context, batch []Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batch = append(m.batch, batch...)
	return nil
}

func (m *memoryDestination) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func TestRunnerEndToEnd(t *testing.T) {
	dest := &memoryDestination{}
	RegisterSource("test_memory_source", func(cfg Fields) (Source, error) {
		return memorySource{}, nil
	})
	RegisterDestination("test_memory_dest", func(cfg Fields) (Destination, error) {
		return dest, nil
	})

	conf, err := ParseConfig([]byte(`
sources:
  - name: mem
    type: test_memory_source
    interval: 0s
destinations:
  - name: out
    type: test_memory_dest
`))
	if err != nil {
		t.Fatal(err)
	}

	runner, err := NewRunner(conf, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runner.Run(ctx); err != nil {
		t.Fatal(err)
	}

	dest.mu.Lock()
	defer dest.mu.Unlock()
	if len(dest.batch) != 1 {
		t.Fatalf("expected 1 message, got %d", len(dest.batch))
	}
	msg := dest.batch[0]
	if msg.Data["value"] != 42 {
		t.Errorf("unexpected data: %v", msg.Data)
	}
	if msg.Meta["source"] != "mem" || msg.Meta["source_type"] != "test_memory_source" {
		t.Errorf("unexpected meta: %v", msg.Meta)
	}
	if !dest.closed {
		t.Error("destination was not closed")
	}
}
