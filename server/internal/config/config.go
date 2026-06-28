package config

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Addr            string
	DatabasePath    string
	OutputRoot      string
	WebDir          string
	SessionSecret   []byte
	AdminUsername   string
	AdminPassword   string
	SessionDuration time.Duration
}

func FromEnv() Config {
	cfg := defaultConfig()
	applyEnv(&cfg)
	return cfg
}

func Load() (Config, error) {
	cfg := defaultConfig()
	if path := os.Getenv("VIBE_CONFIG"); path != "" {
		fileConfig, err := loadFile(path)
		if err != nil {
			return Config{}, err
		}
		applyFile(&cfg, fileConfig)
	}
	applyEnv(&cfg)
	return cfg, nil
}

type fileConfig struct {
	Addr          string `yaml:"addr"`
	DatabasePath  string `yaml:"database_path"`
	OutputRoot    string `yaml:"output_root"`
	WebDir        string `yaml:"web_dir"`
	SessionSecret string `yaml:"session_secret"`
	AdminUsername string `yaml:"admin_username"`
	AdminPassword string `yaml:"admin_password"`
}

func defaultConfig() Config {
	return Config{
		Addr:            ":8080",
		DatabasePath:    "data/vibe-terminal.db",
		OutputRoot:      "workspace-data",
		WebDir:          "web/dist",
		SessionSecret:   []byte("dev-session-secret-32-bytes-long"),
		SessionDuration: 24 * time.Hour,
	}
}

func loadFile(path string) (fileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fileConfig{}, fmt.Errorf("read config file %q: %w", path, err)
	}
	var cfg fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return fileConfig{}, fmt.Errorf("parse config file %q: %w", path, err)
	}
	return cfg, nil
}

func applyFile(cfg *Config, file fileConfig) {
	if file.Addr != "" {
		cfg.Addr = file.Addr
	}
	if file.DatabasePath != "" {
		cfg.DatabasePath = file.DatabasePath
	}
	if file.OutputRoot != "" {
		cfg.OutputRoot = file.OutputRoot
	}
	if file.WebDir != "" {
		cfg.WebDir = file.WebDir
	}
	if file.SessionSecret != "" {
		cfg.SessionSecret = []byte(file.SessionSecret)
	}
	if file.AdminUsername != "" {
		cfg.AdminUsername = file.AdminUsername
	}
	if file.AdminPassword != "" {
		cfg.AdminPassword = file.AdminPassword
	}
}

func applyEnv(cfg *Config) {
	if value := os.Getenv("VIBE_ADDR"); value != "" {
		cfg.Addr = value
	}
	if value := os.Getenv("VIBE_DB"); value != "" {
		cfg.DatabasePath = value
	}
	if value := os.Getenv("VIBE_OUTPUT_ROOT"); value != "" {
		cfg.OutputRoot = value
	}
	if value := os.Getenv("VIBE_WEB_DIR"); value != "" {
		cfg.WebDir = value
	}
	if value := os.Getenv("VIBE_SESSION_SECRET"); value != "" {
		cfg.SessionSecret = []byte(value)
	}
	if value := os.Getenv("VIBE_ADMIN_USER"); value != "" {
		cfg.AdminUsername = value
	}
	if value := os.Getenv("VIBE_ADMIN_PASSWORD"); value != "" {
		cfg.AdminPassword = value
	}
}
