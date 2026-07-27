package core

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// boundSource is a configured source with its poll interval and resolved
// destinations.
type boundSource struct {
	name         string
	typeName     string
	interval     time.Duration
	source       Source
	destinations []boundDestination
}

type boundDestination struct {
	name        string
	destination Destination
}

// Runner owns all configured sources and destinations and schedules
// collection.
type Runner struct {
	log          *slog.Logger
	sources      []boundSource
	destinations []boundDestination
}

// NewRunner instantiates every configured destination and source. A source
// with no explicit `destinations` list sends to all destinations.
func NewRunner(conf *Config, log *slog.Logger) (*Runner, error) {
	r := &Runner{log: log}

	destsByName := map[string]boundDestination{}
	for _, dc := range conf.Destinations {
		dest, err := NewDestination(dc.Type, dc.Config)
		if err != nil {
			return nil, fmt.Errorf("destination %q: %w", dc.Name, err)
		}
		bound := boundDestination{name: dc.Name, destination: dest}
		destsByName[dc.Name] = bound
		r.destinations = append(r.destinations, bound)
	}

	for _, sc := range conf.Sources {
		src, err := NewSource(sc.Type, sc.Config)
		if err != nil {
			return nil, fmt.Errorf("source %q: %w", sc.Name, err)
		}

		intervalStr := sc.Interval
		if intervalStr == "" {
			intervalStr = conf.DefaultInterval
		}
		interval, err := time.ParseDuration(intervalStr)
		if err != nil {
			return nil, fmt.Errorf("source %q: invalid interval %q: %w", sc.Name, intervalStr, err)
		}

		bound := boundSource{
			name:     sc.Name,
			typeName: sc.Type,
			interval: interval,
			source:   src,
		}
		if len(sc.Destinations) == 0 {
			bound.destinations = r.destinations
		} else {
			for _, dn := range sc.Destinations {
				bound.destinations = append(bound.destinations, destsByName[dn])
			}
		}
		r.sources = append(r.sources, bound)
	}
	return r, nil
}

// Run collects from every source on its interval until ctx is cancelled.
// Sources with a 0 interval collect exactly once. Run returns once all
// source loops have stopped and destinations are closed.
func (r *Runner) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for i := range r.sources {
		src := &r.sources[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.runSource(ctx, src)
		}()
	}
	wg.Wait()

	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, d := range r.destinations {
		if err := d.destination.Close(closeCtx); err != nil {
			r.log.Error("failed to close destination", "destination", d.name, "error", err)
		}
	}
	return nil
}

func (r *Runner) runSource(ctx context.Context, src *boundSource) {
	log := r.log.With("source", src.name, "type", src.typeName)
	for {
		start := time.Now()
		batch, err := src.source.Collect(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error("collection failed", "error", err)
		} else {
			log.Info("collected", "messages", len(batch), "duration", time.Since(start).Round(time.Millisecond))
			for i := range batch {
				if batch[i].Meta == nil {
					batch[i].Meta = map[string]string{}
				}
				batch[i].Meta["source"] = src.name
				batch[i].Meta["source_type"] = src.typeName
			}
			for _, d := range src.destinations {
				if len(batch) == 0 {
					continue
				}
				if err := d.destination.Write(ctx, batch); err != nil {
					if ctx.Err() != nil {
						return
					}
					log.Error("write failed", "destination", d.name, "error", err)
				}
			}
		}

		if src.interval <= 0 {
			log.Info("interval is 0, source finished after one collection")
			return
		}
		select {
		case <-time.After(src.interval):
		case <-ctx.Done():
			return
		}
	}
}
