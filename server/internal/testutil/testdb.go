package testutil

import (
	"context"
	"testing"

	"github.com/djy/vibe-terminal/server/internal/store"
)

func NewStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
