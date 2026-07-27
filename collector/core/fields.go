package core

import (
	"fmt"
	"time"
)

// Fields is the free-form `config` block of a source or destination entry.
type Fields map[string]any

// String returns the string at key, or def when absent.
func (f Fields) String(key, def string) string {
	if v, ok := f[key].(string); ok {
		return v
	}
	return def
}

// RequiredString returns the string at key or an error when absent or empty.
func (f Fields) RequiredString(key string) (string, error) {
	v, ok := f[key].(string)
	if !ok || v == "" {
		return "", fmt.Errorf("config field %q is required", key)
	}
	return v, nil
}

// Bool returns the bool at key, or def when absent.
func (f Fields) Bool(key string, def bool) bool {
	if v, ok := f[key].(bool); ok {
		return v
	}
	return def
}

// StringList returns the list of strings at key, tolerating a single string
// value or an absent key.
func (f Fields) StringList(key string) []string {
	switch v := f[key].(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	}
	return nil
}

// StringMap returns the map of strings at key, or an empty map when absent.
func (f Fields) StringMap(key string) map[string]string {
	out := map[string]string{}
	if m, ok := f[key].(map[string]any); ok {
		for k, v := range m {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
	}
	return out
}

// Duration parses the duration string at key, or returns def when absent.
func (f Fields) Duration(key string, def time.Duration) (time.Duration, error) {
	v, ok := f[key].(string)
	if !ok || v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config field %q: %w", key, err)
	}
	return d, nil
}
