package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// Config holds all configuration loaded from environment variables or .env file.
type Config struct {
	MatrixHomeserver string
	MatrixUserID     string
	MatrixPassword   string
	MatrixOwnerID    string
	OpencodeURL      string
	OpencodePassword string
	// PickleKey is used to encrypt the local E2E crypto store (SQLite).
	// Required for E2E encryption. Must be at least 32 bytes when used.
	PickleKey string
	// CryptoDBPath is the path to the SQLite database for E2E crypto state.
	// Defaults to ~/.local/share/opencode-matrix-bot/crypto.db
	CryptoDBPath string
}

// LoadConfig loads configuration from .env file (if present) and environment variables.
func LoadConfig() (*Config, error) {
	// Load .env if it exists; ignore error if file is missing
	_ = godotenv.Load()

	cfg := &Config{
		MatrixHomeserver: os.Getenv("MATRIX_HOMESERVER"),
		MatrixUserID:     os.Getenv("MATRIX_USER_ID"),
		MatrixPassword:   os.Getenv("MATRIX_PASSWORD"),
		MatrixOwnerID:    os.Getenv("MATRIX_OWNER_ID"),
		OpencodeURL:      os.Getenv("OPENCODE_URL"),
		OpencodePassword: os.Getenv("OPENCODE_PASSWORD"),
		PickleKey:        os.Getenv("MATRIX_PICKLE_KEY"),
		CryptoDBPath:     os.Getenv("MATRIX_CRYPTO_DB"),
	}

	if cfg.MatrixHomeserver == "" {
		return nil, fmt.Errorf("MATRIX_HOMESERVER is required")
	}
	if cfg.MatrixUserID == "" {
		return nil, fmt.Errorf("MATRIX_USER_ID is required")
	}
	if cfg.MatrixPassword == "" {
		return nil, fmt.Errorf("MATRIX_PASSWORD is required")
	}
	if cfg.MatrixOwnerID == "" {
		return nil, fmt.Errorf("MATRIX_OWNER_ID is required")
	}
	if cfg.OpencodeURL == "" {
		cfg.OpencodeURL = "http://localhost:4096"
	}
	if cfg.PickleKey == "" {
		return nil, fmt.Errorf("MATRIX_PICKLE_KEY is required (used to encrypt local E2E crypto store)")
	}
	if cfg.CryptoDBPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot determine home directory: %w", err)
		}
		cfg.CryptoDBPath = filepath.Join(home, ".local", "share", "opencode-matrix-bot", "crypto.db")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.CryptoDBPath), 0o700); err != nil {
		return nil, fmt.Errorf("cannot create crypto DB directory: %w", err)
	}

	return cfg, nil
}
