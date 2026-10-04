#!/bin/sh
set -eu
if [ -z "${COLLECTOR_DB_PASSWORD:-}" ]; then
    printf '%s\n' 'COLLECTOR_DB_PASSWORD must not be empty' >&2
    exit 1
fi
# Separate DB principal, with no grants on Terex's public tables. psql quoting
# keeps the configured password out of executable SQL syntax and process args.
if ! psql --no-psqlrc --no-password --quiet --set=ON_ERROR_STOP=1 >/dev/null 2>&1 <<'SQL'
\getenv collector_password COLLECTOR_DB_PASSWORD
BEGIN;
SELECT format('CREATE ROLE twitch_collector LOGIN PASSWORD %L', :'collector_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='twitch_collector') \gexec
SELECT format('ALTER ROLE twitch_collector PASSWORD %L', :'collector_password') \gexec
CREATE SCHEMA IF NOT EXISTS twitch_collector AUTHORIZATION twitch_collector;
ALTER SCHEMA twitch_collector OWNER TO twitch_collector;
SELECT format('GRANT CONNECT ON DATABASE %I TO twitch_collector', current_database()) \gexec
DO $check$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'twitch_collector'
               AND (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls)) THEN
        RAISE EXCEPTION 'collector role must not have administrative privileges';
    END IF;
    IF EXISTS (
        SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
          AND has_table_privilege('twitch_collector', c.oid,
              'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
    ) THEN
        RAISE EXCEPTION 'collector role must not access Terex public tables';
    END IF;
END
$check$;
COMMIT;
SQL
then
    printf '%s\n' 'Collector database provisioning failed; check administrator connection and role permissions' >&2
    exit 1
fi

# The staging Job supplies the runtime DSN for a real authentication check.
# Read it inside psql instead of exposing it as a process argument. Suppress
# psql diagnostics because malformed connection strings can include secrets.
if [ -n "${COLLECTOR_DATABASE_URL:-}" ]; then
    if ! psql --no-psqlrc --no-password --quiet --set=ON_ERROR_STOP=1 >/dev/null 2>&1 <<'SQL'
SELECT current_database() AS collector_expected_database \gset
\getenv collector_dsn COLLECTOR_DATABASE_URL
\connect -reuse-previous=off :collector_dsn
SELECT set_config('terex.collector_expected_database', :'collector_expected_database', false);
DO $check$
BEGIN
    IF current_user <> 'twitch_collector' THEN
        RAISE EXCEPTION 'collector DSN must authenticate as twitch_collector';
    END IF;
    IF current_database() <> current_setting('terex.collector_expected_database') THEN
        RAISE EXCEPTION 'collector DSN must use the provisioned database';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_namespace n JOIN pg_roles r ON r.oid = n.nspowner
        WHERE n.nspname = 'twitch_collector' AND r.rolname = current_user
    ) THEN
        RAISE EXCEPTION 'collector must own its schema';
    END IF;
    IF EXISTS (
        SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
          AND has_table_privilege(current_user, c.oid,
              'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
    ) THEN
        RAISE EXCEPTION 'collector role must not access Terex public tables';
    END IF;
END
$check$;
SQL
    then
        printf '%s\n' 'Collector runtime database verification failed; check its DSN, password and schema permissions' >&2
        exit 1
    fi
fi
