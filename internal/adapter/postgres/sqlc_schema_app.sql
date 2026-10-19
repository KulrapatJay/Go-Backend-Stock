-- Read by sqlc only, before migrations/. In real databases the bootstrap (deploy/db/bootstrap.sql)
-- creates schema app before goose runs, so migrations never create it themselves.
CREATE SCHEMA app;
