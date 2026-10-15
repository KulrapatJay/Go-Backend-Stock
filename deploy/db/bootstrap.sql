-- Bootstraps roles and the app schema before goose runs for the first time.
-- Local Docker and CI only. Supabase gets its own runbook in milestone 11:
-- do not run this file against Supabase as-is.
--
-- Usage (as a superuser):
--   psql "$ADMIN_DATABASE_URL" -v ON_ERROR_STOP=1 \
--     -v migrator_password=... -v runtime_password=... -v maintenance_password=... \
--     -f deploy/db/bootstrap.sql

\set ON_ERROR_STOP on

CREATE ROLE app_migrator LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD :'migrator_password';
CREATE ROLE app_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD :'runtime_password';
CREATE ROLE app_maintenance LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD :'maintenance_password';

SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', current_database()) \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO app_migrator, app_runtime, app_maintenance', current_database()) \gexec

REVOKE CREATE ON SCHEMA public FROM PUBLIC;

CREATE SCHEMA app AUTHORIZATION app_migrator;
REVOKE ALL ON SCHEMA app FROM PUBLIC;
GRANT USAGE ON SCHEMA app TO app_runtime, app_maintenance;
