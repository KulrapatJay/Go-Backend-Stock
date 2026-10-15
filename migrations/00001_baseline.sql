-- +goose Up
COMMENT ON SCHEMA app IS 'Stock portfolio tracker application schema (managed by goose)';

-- +goose Down
COMMENT ON SCHEMA app IS NULL;
