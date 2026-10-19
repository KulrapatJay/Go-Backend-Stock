-- name: InsertAuditEvent :exec
INSERT INTO app.audit_events (
    id, occurred_at, event_type, actor_user_id, request_id, session_id,
    target_type, target_id, outcome, reason_code, metadata
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
);
