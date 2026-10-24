package postgres

import (
	"context"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// AuditRepository appends audit events. The runtime role has INSERT only on audit_events.
type AuditRepository struct {
	q *sqlc.Queries
}

var _ port.AuditRepository = (*AuditRepository)(nil)

// Append inserts one event. Callers build events with domain.NewAuditEvent.
func (r *AuditRepository) Append(ctx context.Context, e domain.AuditEvent) error {
	return mapError(r.q.InsertAuditEvent(ctx, sqlc.InsertAuditEventParams{
		ID:          pgUUID(e.ID),
		OccurredAt:  pgTime(e.OccurredAt),
		EventType:   string(e.EventType),
		ActorUserID: pgUUID(e.ActorUserID),
		RequestID:   pgUUID(e.RequestID),
		SessionID:   pgUUID(e.SessionID),
		TargetType:  e.TargetType,
		TargetID:    pgText(e.TargetID),
		Outcome:     string(e.Outcome),
		ReasonCode:  pgText(e.ReasonCode),
		Metadata:    e.Metadata,
	}))
}
