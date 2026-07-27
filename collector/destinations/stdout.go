// Package destinations contains the built-in destination types. Adding a
// new destination type means adding one file to this package: implement
// core.Destination and register a constructor in an init function.
package destinations

import (
	"context"
	"encoding/json"
	"os"

	"github.com/pcarpe4/bento/collector/core"
)

// stdoutDestination writes each message as a JSON line to standard output.
type stdoutDestination struct {
	encoder *json.Encoder
}

func init() {
	core.RegisterDestination("stdout", func(cfg core.Fields) (core.Destination, error) {
		return &stdoutDestination{encoder: json.NewEncoder(os.Stdout)}, nil
	})
}

func (s *stdoutDestination) Write(ctx context.Context, batch []core.Message) error {
	for _, msg := range batch {
		if err := s.encoder.Encode(msg); err != nil {
			return err
		}
	}
	return nil
}

func (s *stdoutDestination) Close(ctx context.Context) error {
	return nil
}
