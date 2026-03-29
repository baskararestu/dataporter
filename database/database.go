package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Connections holds both DB connections needed by the application.
type Connections struct {
	// Source is a single read-only connection to the EMR database.
	Source *pgx.Conn
	// Target is a connection pool to the SIMRS (write) database.
	Target *pgxpool.Pool
}

// Connect opens the source connection and target pool.
// The caller is responsible for calling Close when done.
func Connect(ctx context.Context, sourceDSN, targetDSN string) (*Connections, error) {
	src, err := pgx.Connect(ctx, sourceDSN)
	if err != nil {
		return nil, fmt.Errorf("connect source DB: %w", err)
	}
	log.Info().Msg("source DB connected")

	pool, err := pgxpool.New(ctx, targetDSN)
	if err != nil {
		src.Close(ctx)
		return nil, fmt.Errorf("connect target DB: %w", err)
	}
	log.Info().Msg("target DB connected")

	return &Connections{Source: src, Target: pool}, nil
}

// Close releases both connections.
func (c *Connections) Close(ctx context.Context) {
	c.Source.Close(ctx)
	c.Target.Close()
}
