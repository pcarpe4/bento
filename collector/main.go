// Command collector is a barebones infrastructure data collector: it polls
// configured sources on an interval and writes the results to configured
// destinations, all defined in a single YAML file.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/pcarpe4/bento/collector/core"

	// Register the built-in source and destination types.
	_ "github.com/pcarpe4/bento/collector/destinations"
	_ "github.com/pcarpe4/bento/collector/sources"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the collector config file")
	listTypes := flag.Bool("list", false, "list registered source and destination types and exit")
	flag.Parse()

	if *listTypes {
		fmt.Println("sources:", core.SourceTypes())
		fmt.Println("destinations:", core.DestinationTypes())
		return
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	conf, err := core.LoadConfig(*configPath)
	if err != nil {
		log.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	runner, err := core.NewRunner(conf, log)
	if err != nil {
		log.Error("failed to initialise", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("collector started",
		"sources", len(conf.Sources), "destinations", len(conf.Destinations))
	if err := runner.Run(ctx); err != nil {
		log.Error("runner failed", "error", err)
		os.Exit(1)
	}
	log.Info("collector stopped")
}
