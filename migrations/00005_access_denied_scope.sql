-- +goose Up
-- Adds the access_denied_user scope (portfolio ownership denials, throttled per user). Spec
-- section 5: a new scope needs a migration.
ALTER TABLE app.rate_limit_keys DROP CONSTRAINT rate_limit_keys_scope_allowed;
ALTER TABLE app.rate_limit_keys ADD CONSTRAINT rate_limit_keys_scope_allowed CHECK (scope IN (
    'login_email', 'login_ip', 'signup_ip', 'google_start_ip',
    'email_verification_request_email', 'email_verification_request_ip', 'email_verification_confirm_ip',
    'password_reset_request_email', 'password_reset_request_ip', 'password_reset_confirm_ip',
    'instrument_lookup_user', 'access_denied_user'));

-- +goose Down
DELETE FROM app.rate_limit_events WHERE key_hash IN (SELECT key_hash FROM app.rate_limit_keys WHERE scope = 'access_denied_user');
DELETE FROM app.rate_limit_keys WHERE scope = 'access_denied_user';
ALTER TABLE app.rate_limit_keys DROP CONSTRAINT rate_limit_keys_scope_allowed;
ALTER TABLE app.rate_limit_keys ADD CONSTRAINT rate_limit_keys_scope_allowed CHECK (scope IN (
    'login_email', 'login_ip', 'signup_ip', 'google_start_ip',
    'email_verification_request_email', 'email_verification_request_ip', 'email_verification_confirm_ip',
    'password_reset_request_email', 'password_reset_request_ip', 'password_reset_confirm_ip',
    'instrument_lookup_user'));
