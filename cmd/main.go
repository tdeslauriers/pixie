package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/tdeslauriers/carapace/pkg/config"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/gallery"
)

func main() {

	// set logging to json format for application
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(jsonHandler))

	// create a logger for the main package
	logger := slog.Default().
		With(slog.String(util.ServiceKey, util.ServiceGallery)).
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
			Identity:         true,
			S2sVerifyingKey:  true,
			UserVerifyingKey: true,
			ObjectStorage:    true,
		},
	}

	config, err := config.Load(def)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to load %s gallery service config: %v", util.ServiceGallery, err))
		os.Exit(1)
	}

	gallery, err := gallery.New(config)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to create %s gallery service: %v", util.ServiceGallery, err))
		os.Exit(1)
	}

	defer gallery.CloseDb()

	if err := gallery.Run(); err != nil {
		logger.Error(fmt.Sprintf("failed to run %s gallery service: %v", util.ServiceGallery, err))
		os.Exit(1)
	}

	select {} // block forever
}
