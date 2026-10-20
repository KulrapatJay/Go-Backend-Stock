-- Every query except CredentialsByEmail leaves password_hash out (phase 1 spec section 7).

-- name: GetUserByID :one
SELECT id, email, email_verified_at, created_at, updated_at
FROM app.users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT id, email, email_verified_at, created_at, updated_at
FROM app.users WHERE email = $1;

-- name: GetUserCredentialsByEmail :one
SELECT id, email, email_verified_at, created_at, updated_at, password_hash
FROM app.users WHERE email = $1;

-- name: LockUser :one
SELECT id, email, email_verified_at, created_at, updated_at
FROM app.users WHERE id = $1
FOR UPDATE;

-- ON CONFLICT DO NOTHING keeps the transaction usable when the email is taken: zero rows
-- inserted means the email is already registered.
-- name: InsertUser :execrows
INSERT INTO app.users (id, email, email_verified_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (email) DO NOTHING;

-- name: SetUserPassword :execrows
UPDATE app.users SET password_hash = $2, updated_at = $3 WHERE id = $1;

-- Sets email_verified_at only the first time; a verified user keeps the original time.
-- name: MarkUserEmailVerified :execrows
UPDATE app.users
SET email_verified_at = COALESCE(email_verified_at, @verified_at::timestamptz),
    updated_at = CASE WHEN email_verified_at IS NULL THEN @verified_at::timestamptz ELSE updated_at END
WHERE id = @id;
