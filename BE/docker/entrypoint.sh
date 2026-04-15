#!/bin/sh
set -e

# Create the 'main' database if it doesn't exist
python - <<'EOF'
import os, psycopg2
url = os.environ["DATABASE_URL"]
conn_url = url.rsplit("/", 1)[0] + "/postgres"
conn = psycopg2.connect(conn_url)
conn.autocommit = True
cur = conn.cursor()
cur.execute("SELECT 1 FROM pg_database WHERE datname = 'main'")
if not cur.fetchone():
    cur.execute("CREATE DATABASE main")
    print("Created database 'main'")
cur.close()
conn.close()

# Reset alembic version so consolidated migration runs cleanly
conn = psycopg2.connect(url)
conn.autocommit = True
cur = conn.cursor()
try:
    cur.execute("TRUNCATE TABLE alembic_version")
except Exception:
    pass
cur.close()
conn.close()
EOF

# Run migrations
alembic upgrade head

# Start the app — hot reload in dev, multi-worker in prod
if [ "${DEV:-false}" = "true" ]; then
    exec uvicorn main:app --host 0.0.0.0 --port 8000 --reload --log-config log_config.yaml
else
    exec uvicorn main:app --host 0.0.0.0 --port 8000 --workers 2 --log-config log_config.yaml
fi
