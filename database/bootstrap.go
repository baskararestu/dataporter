package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/baskararestu/dataporter/config"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// Bootstrap ensures that the EMR and SIMRS databases exist, their schemas
// are applied, and the EMR is seeded with data if empty.
// It connects to the default "postgres" database on each host to run
// CREATE DATABASE, then applies the DDL scripts.
//
// Fully idempotent — safe to call on every startup.
func Bootstrap(ctx context.Context, cfg *config.Config, emrDDL, simrsDDL string) error {
	// ── 1. Create database_emr if it does not exist ──
	if err := ensureDatabase(ctx, cfg.Source, cfg.Source.Name); err != nil {
		return fmt.Errorf("ensure EMR database: %w", err)
	}

	// ── 2. Create database_simrs if it does not exist ──
	if err := ensureDatabase(ctx, cfg.Target, cfg.Target.Name); err != nil {
		return fmt.Errorf("ensure SIMRS database: %w", err)
	}

	// ── 3. Apply EMR schema (CREATE TABLE IF NOT EXISTS only) + seed if empty ──
	if err := initEMR(ctx, cfg.Source, emrDDL); err != nil {
		return fmt.Errorf("init EMR: %w", err)
	}

	// ── 4. Apply SIMRS schema (idempotent — CREATE IF NOT EXISTS) ──
	if err := applyDDL(ctx, cfg.Target, simrsDDL); err != nil {
		return fmt.Errorf("apply SIMRS DDL: %w", err)
	}

	return nil
}

// ensureDatabase connects to the "postgres" admin database on the same host
// and creates the target database if it doesn't exist.
func ensureDatabase(ctx context.Context, dbCfg config.DBConfig, dbName string) error {
	// Build admin DSN — connect to "postgres" database instead of the target.
	adminDSN := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/postgres?sslmode=%s",
		dbCfg.User, dbCfg.Password, dbCfg.Host, dbCfg.Port, dbCfg.SSLMode,
	)

	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return fmt.Errorf("connect admin DB on %s:%d: %w", dbCfg.Host, dbCfg.Port, err)
	}
	defer conn.Close(ctx)

	// Check if database already exists.
	var exists bool
	err = conn.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`,
		dbName,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check database existence: %w", err)
	}

	if exists {
		log.Info().Str("database", dbName).Str("host", dbCfg.Host).Msg("database already exists")
		return nil
	}

	// CREATE DATABASE cannot run inside a transaction, and pgx doesn't use
	// implicit transactions for Exec, so this is fine.
	// Sanitize dbName — it comes from config, not user input, but be safe.
	sanitized := sanitizeIdentifier(dbName)
	_, err = conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", sanitized))
	if err != nil {
		return fmt.Errorf("create database %s: %w", dbName, err)
	}

	log.Info().Str("database", dbName).Str("host", dbCfg.Host).Msg("database created")
	return nil
}

// applyDDL connects to the target database and executes the DDL script.
// The DDL is expected to be idempotent (CREATE IF NOT EXISTS, etc.).
func applyDDL(ctx context.Context, dbCfg config.DBConfig, ddl string) error {
	conn, err := pgx.Connect(ctx, dbCfg.DSN())
	if err != nil {
		return fmt.Errorf("connect %s: %w", dbCfg.Name, err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("exec DDL on %s: %w", dbCfg.Name, err)
	}

	log.Info().Str("database", dbCfg.Name).Msg("DDL applied successfully")
	return nil
}

// initEMR applies the full EMR DDL (schema + seed) only if the pasien table
// is missing or empty. This makes the seed operation idempotent — if 2M rows
// already exist, the expensive INSERT is skipped entirely.
func initEMR(ctx context.Context, dbCfg config.DBConfig, emrDDL string) error {
	conn, err := pgx.Connect(ctx, dbCfg.DSN())
	if err != nil {
		return fmt.Errorf("connect EMR %s: %w", dbCfg.Name, err)
	}
	defer conn.Close(ctx)

	// Check if pasien table exists and has data.
	var count int64
	err = conn.QueryRow(ctx, `
		SELECT COUNT(*) FROM information_schema.tables 
		WHERE table_schema = 'public' AND table_name = 'pasien'
	`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check pasien table: %w", err)
	}

	if count == 0 {
		// Table doesn't exist — run full DDL (CREATE + INSERT 2M rows).
		log.Info().Str("database", dbCfg.Name).Msg("EMR tables not found, creating schema and seeding data (2M rows)...")
		if _, err := conn.Exec(ctx, emrDDL); err != nil {
			return fmt.Errorf("exec EMR DDL: %w", err)
		}
		log.Info().Str("database", dbCfg.Name).Msg("EMR schema created and data seeded")
		return nil
	}

	// Table exists — check if it has data.
	var rowCount int64
	err = conn.QueryRow(ctx, `SELECT COUNT(*) FROM pasien`).Scan(&rowCount)
	if err != nil {
		return fmt.Errorf("count pasien rows: %w", err)
	}

	if rowCount > 0 {
		log.Info().Str("database", dbCfg.Name).Int64("rows", rowCount).Msg("EMR pasien table already seeded, skipping")
		return nil
	}

	// Table exists but empty — run full DDL (INSERTs will populate).
	log.Info().Str("database", dbCfg.Name).Msg("EMR pasien table empty, seeding data (2M rows)...")
	if _, err := conn.Exec(ctx, emrDDL); err != nil {
		return fmt.Errorf("exec EMR DDL (seed): %w", err)
	}
	log.Info().Str("database", dbCfg.Name).Msg("EMR data seeded")
	return nil
}

// SeedEMRExtra inserts 1.5M additional patients (IDs 2000001–3500000) into the EMR
// database using the provided SQL script. Idempotent — uses ON CONFLICT DO NOTHING.
//
// Returns the row counts before and after the operation so callers can report
// how many rows were actually added.
//
// Intended for dev/staging use only to simulate EMR data growth.
func SeedEMRExtra(ctx context.Context, dbCfg config.DBConfig, extraSQL string) (beforeCount, afterCount int64, err error) {
	conn, err := pgx.Connect(ctx, dbCfg.DSN())
	if err != nil {
		return 0, 0, fmt.Errorf("connect EMR %s: %w", dbCfg.Name, err)
	}
	defer conn.Close(ctx)

	if err := conn.QueryRow(ctx, `SELECT COUNT(id_pasien) FROM pasien`).Scan(&beforeCount); err != nil {
		return 0, 0, fmt.Errorf("count pasien before seed: %w", err)
	}

	log.Info().
		Str("database", dbCfg.Name).
		Int64("before_count", beforeCount).
		Msg("seeding extra 1.5M EMR rows...")

	if _, err := conn.Exec(ctx, extraSQL); err != nil {
		return beforeCount, 0, fmt.Errorf("exec extra seed SQL: %w", err)
	}

	if err := conn.QueryRow(ctx, `SELECT COUNT(id_pasien) FROM pasien`).Scan(&afterCount); err != nil {
		return beforeCount, 0, fmt.Errorf("count pasien after seed: %w", err)
	}

	log.Info().
		Str("database", dbCfg.Name).
		Int64("before_count", beforeCount).
		Int64("after_count", afterCount).
		Int64("added", afterCount-beforeCount).
		Msg("extra EMR seed complete")

	return beforeCount, afterCount, nil
}

// sanitizeIdentifier wraps a database name in double quotes for safe SQL injection
// prevention in DDL statements (CREATE DATABASE, etc.).
func sanitizeIdentifier(name string) string {
	// Escape any embedded double quotes, then wrap.
	escaped := strings.ReplaceAll(name, `"`, `""`)
	return fmt.Sprintf(`"%s"`, escaped)
}
