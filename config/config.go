package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

// DBConfig holds individual connection parameters for one PostgreSQL instance.
// The DSN is assembled from these fields via DSN().
type DBConfig struct {
	Host     string `env:"HOST,required"`
	Port     int    `env:"PORT,required"`
	User     string `env:"USER,required"`
	Password string `env:"PASSWORD,required"`
	Name     string `env:"NAME,required"`
	SSLMode  string `env:"SSL_MODE" envDefault:"disable"`
}

// DSN returns a fully-formed PostgreSQL connection string.
func (d DBConfig) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		d.User, d.Password, d.Host, d.Port, d.Name, d.SSLMode,
	)
}

type Config struct {
	// Source is the EMR database (read-only).
	Source DBConfig `envPrefix:"EMR_DB_"`

	// Target is the SIMRS database (read-write).
	Target DBConfig `envPrefix:"SIMRS_DB_"`

	// BatchSize is the number of rows fetched per cursor FETCH.
	BatchSize int `env:"BATCH_SIZE" envDefault:"5000"`

	// BatchDelayMs is the delay in milliseconds between batches (backpressure).
	BatchDelayMs int `env:"BATCH_DELAY_MS" envDefault:"0"`

	// HTTPPort is the port the API server listens on.
	HTTPPort int `env:"HTTP_PORT" envDefault:"8080"`

	// LogLevel controls zerolog output level (debug, info, warn, error).
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return &cfg, nil
}

