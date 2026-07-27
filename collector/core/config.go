package core

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// SourceConfig is one entry under `sources` in the config file.
type SourceConfig struct {
	Name         string   `yaml:"name"`
	Type         string   `yaml:"type"`
	Interval     string   `yaml:"interval"`
	Destinations []string `yaml:"destinations"`
	Config       Fields   `yaml:"config"`
}

// DestinationConfig is one entry under `destinations` in the config file.
type DestinationConfig struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Config Fields `yaml:"config"`
}

// Config is the root of the collector config file.
type Config struct {
	// DefaultInterval applies to sources that don't set their own. Defaults
	// to 5m. "0s" means collect once and exit.
	DefaultInterval string              `yaml:"default_interval"`
	Sources         []SourceConfig      `yaml:"sources"`
	Destinations    []DestinationConfig `yaml:"destinations"`
}

var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv substitutes ${VAR} references with environment variable values.
// Unset variables expand to an empty string.
func expandEnv(raw []byte) []byte {
	return envVarPattern.ReplaceAllFunc(raw, func(match []byte) []byte {
		name := envVarPattern.FindSubmatch(match)[1]
		return []byte(os.Getenv(string(name)))
	})
}

// LoadConfig reads, env-expands and parses a YAML config file.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	return ParseConfig(expandEnv(raw))
}

// ParseConfig parses config YAML and validates its structure.
func ParseConfig(raw []byte) (*Config, error) {
	conf := &Config{}
	if err := yaml.Unmarshal(raw, conf); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	if conf.DefaultInterval == "" {
		conf.DefaultInterval = "5m"
	}

	if len(conf.Sources) == 0 {
		return nil, fmt.Errorf("at least one source must be configured")
	}
	if len(conf.Destinations) == 0 {
		return nil, fmt.Errorf("at least one destination must be configured")
	}

	destNames := map[string]bool{}
	for i, d := range conf.Destinations {
		if d.Name == "" {
			return nil, fmt.Errorf("destinations[%d]: name is required", i)
		}
		if d.Type == "" {
			return nil, fmt.Errorf("destination %q: type is required", d.Name)
		}
		if destNames[d.Name] {
			return nil, fmt.Errorf("destination name %q used twice", d.Name)
		}
		destNames[d.Name] = true
	}

	srcNames := map[string]bool{}
	for i, s := range conf.Sources {
		if s.Name == "" {
			return nil, fmt.Errorf("sources[%d]: name is required", i)
		}
		if s.Type == "" {
			return nil, fmt.Errorf("source %q: type is required", s.Name)
		}
		if srcNames[s.Name] {
			return nil, fmt.Errorf("source name %q used twice", s.Name)
		}
		srcNames[s.Name] = true
		for _, d := range s.Destinations {
			if !destNames[d] {
				return nil, fmt.Errorf("source %q references unknown destination %q", s.Name, d)
			}
		}
	}
	return conf, nil
}
