package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tdeslauriers/carapace/pkg/config"
	"github.com/tdeslauriers/pixie/internal/gallery"
	"github.com/tdeslauriers/pixie/internal/util"
)

func main() {

	// set logging to json format for application
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(jsonHandler).
		With(slog.String(util.ServiceKey, util.ServiceGallery)))

	// create a logger for the main package
	logger := slog.Default().
		With(slog.String(util.PackageKey, util.PackageMain)).
		With(slog.String(util.ComponentKey, util.ComponentMain))

	// service definition & requirements
	def := config.SvcDefinition{
		ServiceName: util.ServiceGallery,
		Tls:         config.MutualTls,
		Requires: config.Requires{
			S2sClient:        true,
			Db:               true,
			IndexSecret:      true,
			AesSecret:        true,
			S2sVerifyingKey:  true,
			UserVerifyingKey: true,
			ObjectStorage:    true,
		},
	}

	config, err := config.Load(def)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to load %s gallery service config", util.ServiceGallery), "err", err.Error())
		os.Exit(1)
	}

	gallery, err := gallery.New(config)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to create %s gallery service", util.ServiceGallery), "	err", err.Error())
		os.Exit(1)
	}

	defer gallery.CloseDb()

	// create a context that is cancelled when a shutdown signal is received
	// signal.NotifyContext cancels ctx on SIGINT (ctrl+c) or SIGTERM (kubernetes/docker stop)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Run blocks until the pipeline goroutines exit (which happens when ctx is cancelled)
	if err := gallery.Run(ctx); err != nil {
		logger.Error(fmt.Sprintf("failed to run %s gallery service", util.ServiceGallery), "err", err.Error())
		os.Exit(1)
	}
}
