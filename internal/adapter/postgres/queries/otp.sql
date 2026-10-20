-- Every query filters by purpose, so a challenge ID from one flow never works in the other.

-- name: GetOTPChallenge :one
SELECT id, user_id, purpose, otp_hash, created_at, expires_at, failed_attempts, consumed_at, invalidated_at
FROM app.otp_challenges WHERE id = $1 AND purpose = $2;

-- name: LockOTPChallenge :one
SELECT id, user_id, purpose, otp_hash, created_at, expires_at, failed_attempts, consumed_at, invalidated_at
FROM app.otp_challenges WHERE id = $1 AND purpose = $2
FOR UPDATE;

-- Closes every open challenge of the flow, expired ones included, before a new one is issued.
-- name: InvalidateOpenOTPChallenges :exec
UPDATE app.otp_challenges SET invalidated_at = $3
WHERE user_id = $1 AND purpose = $2 AND consumed_at IS NULL AND invalidated_at IS NULL;

-- name: InsertOTPChallenge :exec
INSERT INTO app.otp_challenges (id, user_id, purpose, otp_hash, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: RecordOTPFailure :execrows
UPDATE app.otp_challenges SET failed_attempts = failed_attempts + 1
WHERE id = $1 AND purpose = $2;

-- A challenge can be consumed once, and only while it is open and unexpired.
-- name: ConsumeOTPChallenge :execrows
UPDATE app.otp_challenges SET consumed_at = @at
WHERE id = @id AND purpose = @purpose AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at > @at;
