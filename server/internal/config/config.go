package config

import (
	"os"
	"time"
)

type Config struct {
	Addr            string
	DatabasePath    string
	OutputRoot      string
	SessionSecret   []byte
	AdminUsername   string
	AdminPassword   string
	SessionDuration time.Duration
}

func FromEnv() Config {
	addr := getenv("VIBE_ADDR", ":8080")
	dbPath := getenv("VIBE_DB", "data/vibe-terminal.db")
	outputRoot := getenv("VIBE_OUTPUT_ROOT", "workspace-data")
	secret := []byte(getenv("VIBE_SESSION_SECRET", "dev-session-secret-32-bytes-long"))
	return Config{
		Addr:            addr,
		DatabasePath:    dbPath,
		OutputRoot:      outputRoot,
		SessionSecret:   secret,
		AdminUsername:   os.Getenv("VIBE_ADMIN_USER"),
		AdminPassword:   os.Getenv("VIBE_ADMIN_PASSWORD"),
		SessionDuration: 24 * time.Hour,
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
