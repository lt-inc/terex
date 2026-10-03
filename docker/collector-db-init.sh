#!/bin/sh
set -eu
# Separate DB principal, with no grants on Terex's public tables. psql quoting
# keeps the configured password out of executable SQL syntax and process args.
psql --no-psqlrc --set=ON_ERROR_STOP=1 <<'SQL'
\getenv collector_password COLLECTOR_DB_PASSWORD
SELECT format('CREATE ROLE twitch_collector LOGIN PASSWORD %L', :'collector_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='twitch_collector') \gexec
SELECT format('ALTER ROLE twitch_collector PASSWORD %L', :'collector_password') \gexec
CREATE SCHEMA IF NOT EXISTS twitch_collector AUTHORIZATION twitch_collector;
ALTER SCHEMA twitch_collector OWNER TO twitch_collector;
GRANT CONNECT ON DATABASE main TO twitch_collector;
SQL
