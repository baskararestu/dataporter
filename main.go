package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/baskararestu/dataporter/api"
	"github.com/baskararestu/dataporter/config"
	"github.com/baskararestu/dataporter/database"
	applogger "github.com/baskararestu/dataporter/logger"
	"github.com/baskararestu/dataporter/migration"
	"github.com/baskararestu/dataporter/monitoring"
	"github.com/baskararestu/dataporter/repository"
	"github.com/baskararestu/dataporter/server"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

//go:embed scripts/002_init_simrs.sql
var schemaDDL string

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	applogger.Setup(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	db, err := database.Connect(ctx, cfg.Source.DSN(), cfg.Target.DSN())
	if err != nil {
		log.Fatal().Err(err).Msg("database connection failed")
	}
	defer db.Close(ctx)

	if err := ensureSchema(ctx, db.Target); err != nil {
		log.Fatal().Err(err).Msg("ensure migration schema")
	}

	jobRepo := repository.NewJobRepository(db.Target)
	mappingRepo := repository.NewMappingRepository(db.Target)
	tracker := monitoring.NewTracker()
	migrator := migration.NewMigrator(db.Source, db.Target, jobRepo, mappingRepo, tracker)

	handler := api.NewHandler(jobRepo, mappingRepo, migrator, tracker, db.Source, db.Target)
	mux := api.NewRouter(handler)

	srv := server.New(cfg.HTTPPort, mux)
	if err := srv.Run(ctx, cfg.HTTPPort); err != nil {
		log.Error().Err(err).Msg("server shutdown error")
	}
}

func ensureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, schemaDDL); err != nil {
		return fmt.Errorf("ensureSchema: %w", err)
	}
	log.Info().Msg("migration schema ready")
	return nil
}
