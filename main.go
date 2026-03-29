package main

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/baskararestu/dataporter/api"
	"github.com/baskararestu/dataporter/config"
	"github.com/baskararestu/dataporter/migration"
	"github.com/baskararestu/dataporter/monitoring"
	"github.com/baskararestu/dataporter/repository"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	srcConn, err := pgx.Connect(ctx, cfg.SourceDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("connect source DB")
	}
	defer srcConn.Close(ctx)
	log.Info().Msg("source DB connected")

	targetPool, err := pgxpool.New(ctx, cfg.TargetDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("connect target DB")
	}
	defer targetPool.Close()
	log.Info().Msg("target DB connected")

	if err := ensureSchema(ctx, targetPool); err != nil {
		log.Fatal().Err(err).Msg("ensure migration schema")
	}

	jobRepo := repository.NewJobRepository(targetPool)
	mappingRepo := repository.NewMappingRepository(targetPool)
	tracker := monitoring.NewTracker()
	migrator := migration.NewMigrator(srcConn, targetPool, jobRepo, mappingRepo, tracker)

	handler := api.NewHandler(jobRepo, mappingRepo, migrator, tracker, srcConn, targetPool)
	mux := api.NewRouter(handler)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info().Int("port", cfg.HTTPPort).Msg("HTTP server starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("HTTP server error")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("HTTP server shutdown error")
	}
	log.Info().Msg("server stopped")
}

//go:embed scripts/002_init_simrs.sql
var schemaDDL string

func ensureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, schemaDDL)
	if err != nil {
		return fmt.Errorf("ensureSchema: %w", err)
	}
	log.Info().Msg("migration schema ready")
	return nil
}
