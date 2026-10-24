-- name: GetIdentityBySubject :one
SELECT id, user_id, provider, provider_subject, created_at
FROM app.auth_providers WHERE provider = $1 AND provider_subject = $2;

-- Zero rows inserted means the subject, or this user's link to the provider, already exists.
-- name: InsertIdentity :execrows
INSERT INTO app.auth_providers (id, user_id, provider, provider_subject, created_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT DO NOTHING;
