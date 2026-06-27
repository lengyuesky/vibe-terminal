package audit

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/djy/vibe-terminal/server/internal/store"
)

type Writer struct {
	Store *store.DB
}

func (w Writer) Log(ctx context.Context, event store.AuditEvent) error {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.MetadataJSON == "" {
		event.MetadataJSON = "{}"
	}
	_, err := w.Store.CreateAuditEvent(ctx, event)
	return err
}
