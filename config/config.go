package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	// SourceDSN is the PostgreSQL connection string for the EMR database (read-only source).
	SourceDSN string `env:"SOURCE_DSN,required"`

	// TargetDSN is the PostgreSQL connection string for the SIMRS database (write target).
	TargetDSN string `env:"TARGET_DSN,required"`

	// BatchSize is the default number of rows fetched per cursor FETCH.
	// Can be overridden per-job via POST /api/jobs body.
	BatchSize int `env:"BATCH_SIZE" envDefault:"5000"`

	// BatchDelayMs is the default delay in milliseconds between batches (backpressure).
	// 0 = no delay. Can be overridden per-job via POST /api/jobs body.
	BatchDelayMs int `env:"BATCH_DELAY_MS" envDefault:"0"`

	// HTTPPort is the port the monitoring/control API listens on.
	HTTPPort int `env:"HTTP_PORT" envDefault:"8080"`

	// LogLevel controls zerolog output level (debug, info, warn, error).
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
}

// Load reads configuration from environment variables.
// Returns an error if required variables (SOURCE_DSN, TARGET_DSN) are missing.
func Load() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return &cfg, nil
}
