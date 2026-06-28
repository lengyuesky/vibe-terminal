package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/config"
	"github.com/djy/vibe-terminal/server/internal/httpapi"
	"github.com/djy/vibe-terminal/server/internal/store"
	"github.com/djy/vibe-terminal/server/internal/terminal"
)

func main() {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := ensureDatabaseDir(cfg.DatabasePath); err != nil {
		log.Fatalf("prepare database directory: %v", err)
	}
	db, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		log.Fatalf("migrate database: %v", err)
	}
	if err := ensureAdmin(ctx, db, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		log.Fatalf("ensure administrator: %v", err)
	}
	router := httpapi.NewRouter(httpapi.Deps{
		Store:       db,
		Sessions:    auth.NewSessionManager(cfg.SessionSecret, cfg.SessionDuration),
		Output:      terminal.FileOutputWriter{Root: cfg.OutputRoot},
		StaticFiles: http.Dir(cfg.WebDir),
	})
	log.Printf("vibe-terminal server listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, router); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}

func ensureDatabaseDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o700)
}

func ensureAdmin(ctx context.Context, db *store.DB, username string, password string) error {
	if username == "" || password == "" {
		return nil
	}
	if _, err := db.GetUserByUsername(ctx, username); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	_, err = db.CreateUser(ctx, store.User{
		ID:           uuid.NewString(),
		Username:     username,
		PasswordHash: hash,
	})
	return err
}
