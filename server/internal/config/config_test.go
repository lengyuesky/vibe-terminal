package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadUsesDefaultsWhenNoConfigFileIsSet(t *testing.T) {
	t.Setenv("VIBE_CONFIG", "")
	t.Setenv("VIBE_ADDR", "")
	t.Setenv("VIBE_DB", "")
	t.Setenv("VIBE_OUTPUT_ROOT", "")
	t.Setenv("VIBE_WEB_DIR", "")
	t.Setenv("VIBE_SESSION_SECRET", "")
	t.Setenv("VIBE_ADMIN_USER", "")
	t.Setenv("VIBE_ADMIN_PASSWORD", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	assertConfig(t, cfg, Config{
		Addr:            ":8080",
		DatabasePath:    "data/vibe-terminal.db",
		OutputRoot:      "workspace-data",
		WebDir:          "web/dist",
		SessionSecret:   []byte("dev-session-secret-32-bytes-long"),
		AdminUsername:   "",
		AdminPassword:   "",
		SessionDuration: 24 * time.Hour,
	})
}

func TestLoadReadsYamlConfigFile(t *testing.T) {
	path := writeConfigFile(t, `
addr: ":9090"
database_path: "/data/app.db"
output_root: "/workspace-data"
web_dir: "/app/web"
session_secret: "yaml-session-secret"
admin_username: "admin"
admin_password: "yaml-password"
`)
	t.Setenv("VIBE_CONFIG", path)
	t.Setenv("VIBE_ADDR", "")
	t.Setenv("VIBE_DB", "")
	t.Setenv("VIBE_OUTPUT_ROOT", "")
	t.Setenv("VIBE_WEB_DIR", "")
	t.Setenv("VIBE_SESSION_SECRET", "")
	t.Setenv("VIBE_ADMIN_USER", "")
	t.Setenv("VIBE_ADMIN_PASSWORD", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	assertConfig(t, cfg, Config{
		Addr:            ":9090",
		DatabasePath:    "/data/app.db",
		OutputRoot:      "/workspace-data",
		WebDir:          "/app/web",
		SessionSecret:   []byte("yaml-session-secret"),
		AdminUsername:   "admin",
		AdminPassword:   "yaml-password",
		SessionDuration: 24 * time.Hour,
	})
}

func TestLoadLetsEnvironmentOverrideYamlConfig(t *testing.T) {
	path := writeConfigFile(t, `
addr: ":9090"
database_path: "/data/app.db"
output_root: "/workspace-data"
web_dir: "/app/web"
session_secret: "yaml-session-secret"
admin_username: "admin"
admin_password: "yaml-password"
`)
	t.Setenv("VIBE_CONFIG", path)
	t.Setenv("VIBE_ADDR", ":7070")
	t.Setenv("VIBE_DB", "/override/app.db")
	t.Setenv("VIBE_OUTPUT_ROOT", "/override-workspace")
	t.Setenv("VIBE_WEB_DIR", "/override-web")
	t.Setenv("VIBE_SESSION_SECRET", "env-session-secret")
	t.Setenv("VIBE_ADMIN_USER", "root")
	t.Setenv("VIBE_ADMIN_PASSWORD", "env-password")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	assertConfig(t, cfg, Config{
		Addr:            ":7070",
		DatabasePath:    "/override/app.db",
		OutputRoot:      "/override-workspace",
		WebDir:          "/override-web",
		SessionSecret:   []byte("env-session-secret"),
		AdminUsername:   "root",
		AdminPassword:   "env-password",
		SessionDuration: 24 * time.Hour,
	})
}

func TestLoadRejectsUnknownYamlFields(t *testing.T) {
	path := writeConfigFile(t, `
addr: ":9090"
unknown_field: "value"
`)
	t.Setenv("VIBE_CONFIG", path)

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want unknown field error")
	}
	if !strings.Contains(err.Error(), "unknown_field") {
		t.Fatalf("Load() error = %q, want unknown field name", err.Error())
	}
}

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	return path
}

func assertConfig(t *testing.T, got Config, want Config) {
	t.Helper()
	if got.Addr != want.Addr {
		t.Fatalf("Addr = %q, want %q", got.Addr, want.Addr)
	}
	if got.DatabasePath != want.DatabasePath {
		t.Fatalf("DatabasePath = %q, want %q", got.DatabasePath, want.DatabasePath)
	}
	if got.OutputRoot != want.OutputRoot {
		t.Fatalf("OutputRoot = %q, want %q", got.OutputRoot, want.OutputRoot)
	}
	if got.WebDir != want.WebDir {
		t.Fatalf("WebDir = %q, want %q", got.WebDir, want.WebDir)
	}
	if string(got.SessionSecret) != string(want.SessionSecret) {
		t.Fatalf("SessionSecret = %q, want %q", string(got.SessionSecret), string(want.SessionSecret))
	}
	if got.AdminUsername != want.AdminUsername {
		t.Fatalf("AdminUsername = %q, want %q", got.AdminUsername, want.AdminUsername)
	}
	if got.AdminPassword != want.AdminPassword {
		t.Fatalf("AdminPassword = %q, want %q", got.AdminPassword, want.AdminPassword)
	}
	if got.SessionDuration != want.SessionDuration {
		t.Fatalf("SessionDuration = %s, want %s", got.SessionDuration, want.SessionDuration)
	}
}
