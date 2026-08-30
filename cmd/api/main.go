package main

import (
	"log/slog"
	"os"

	"github.com/caiohperlin/perlinadv/internal/application"
	"github.com/caiohperlin/perlinadv/internal/config"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := config.Defaults()
	api := application.New(cfg)
	if err := api.Run(); err != nil {
		slog.Error("Server has failed to start", "error", err)
		os.Exit(1)
	}
}
