package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Connections holds both DB connections needed by the application.
type Connections struct {
	// Source is a connection pool to the EMR database (read-only queries + cursor connections).
	Source *pgxpool.Pool
	// Target is a connection pool to the SIMRS (read-write) database.
	Target *pgxpool.Pool
}

// Connect opens both source and target connection pools.
// The caller is responsible for calling Close when done.
func Connect(ctx context.Context, sourceDSN, targetDSN string) (*Connections, error) {
	src, err := pgxpool.New(ctx, sourceDSN)
	if err != nil {
		return nil, fmt.Errorf("connect source DB: %w", err)
	}
	log.Info().Msg("source DB connected")

	pool, err := pgxpool.New(ctx, targetDSN)
	if err != nil {
		src.Close()
		return nil, fmt.Errorf("connect target DB: %w", err)
	}
	log.Info().Msg("target DB connected")

	return &Connections{Source: src, Target: pool}, nil
}

// Close releases both connection pools.
func (c *Connections) Close(_ context.Context) {
	c.Source.Close()
	c.Target.Close()
}
