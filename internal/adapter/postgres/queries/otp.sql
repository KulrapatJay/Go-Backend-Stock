-- Every query filters by purpose, so a challenge ID from one flow never works in the other.

-- name: GetOTPChallenge :one
SELECT id, user_id, purpose, otp_hash, created_at, expires_at, failed_attempts, consumed_at, invalidated_at,
       verified_at, reset_token_hash, reset_binding_hash, reset_token_expires_at
FROM app.otp_challenges WHERE id = $1 AND purpose = $2;

-- name: LockOTPChallenge :one
SELECT id, user_id, purpose, otp_hash, created_at, expires_at, failed_attempts, consumed_at, invalidated_at,
       verified_at, reset_token_hash, reset_binding_hash, reset_token_expires_at
FROM app.otp_challenges WHERE id = $1 AND purpose = $2
FOR UPDATE;

-- Closes every open challenge of the flow, expired ones included, before a new one is issued.
-- name: InvalidateOpenOTPChallenges :exec
UPDATE app.otp_challenges SET invalidated_at = $3
WHERE user_id = $1 AND purpose = $2 AND consumed_at IS NULL AND invalidated_at IS NULL;

-- name: InsertOTPChallenge :exec
INSERT INTO app.otp_challenges (id, user_id, purpose, otp_hash, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- Saturates at 20, the ceiling of otp_challenges_attempts_valid, so counting one failure too many
-- cannot break the caller's transaction.
-- name: RecordOTPFailure :execrows
UPDATE app.otp_challenges SET failed_attempts = LEAST(failed_attempts + 1, 20)
WHERE id = $1 AND purpose = $2;

-- A challenge can be consumed once, and only while it is open and unexpired. A password reset
-- challenge whose code was already verified can only be consumed with its reset token.
-- name: ConsumeOTPChallenge :execrows
UPDATE app.otp_challenges SET consumed_at = @at
WHERE id = @id AND purpose = @purpose AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at > @at
  AND verified_at IS NULL;

-- Stores the reset token of a password reset challenge whose code was just verified. Once only,
-- and only while the challenge is open and its code unexpired.
-- name: GrantPasswordReset :execrows
UPDATE app.otp_challenges
SET verified_at = @verified_at, reset_token_hash = @reset_token_hash,
    reset_binding_hash = @reset_binding_hash, reset_token_expires_at = @reset_token_expires_at
WHERE id = @id AND purpose = 'password_reset' AND consumed_at IS NULL AND invalidated_at IS NULL
  AND verified_at IS NULL AND expires_at > @verified_at;

-- name: GetOTPChallengeByResetToken :one
SELECT id, user_id, purpose, otp_hash, created_at, expires_at, failed_attempts, consumed_at, invalidated_at,
       verified_at, reset_token_hash, reset_binding_hash, reset_token_expires_at
FROM app.otp_challenges WHERE reset_token_hash = $1 AND purpose = 'password_reset';

-- Spends the reset token: once, only while the challenge is open and the token unexpired. The
-- code's own expiry no longer matters, the token has its own.
-- name: ConsumePasswordResetGrant :execrows
UPDATE app.otp_challenges SET consumed_at = @at
WHERE id = @id AND purpose = 'password_reset' AND consumed_at IS NULL AND invalidated_at IS NULL
  AND verified_at IS NOT NULL AND reset_token_expires_at > @at;
