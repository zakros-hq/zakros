#!/bin/bash
# Bootstrap a Postgres 17 + pgvector on the Zakros Postgres LXC.
# Idempotent: safe to re-run. Reads env:
#   POSTGRES_PASSWORD  required — set the zakros role's password
#   POSTGRES_ALLOWED_CIDR  default 0.0.0.0/0 — pg_hba network for zakros role
#   POSTGRES_DB        default 'zakros'
#   POSTGRES_USER      default 'zakros'
#
# Run from the operator's workstation:
#   POSTGRES_PASSWORD=... ssh root@<crete-ip> "pct exec 211 -- bash" < deploy/postgres-bootstrap.sh
# (the env var doesn't traverse ssh by default — see deploy/README.md
# for the env-passing recipe)

set -euo pipefail

: "${POSTGRES_PASSWORD:?must be set}"
: "${POSTGRES_ALLOWED_CIDR:=0.0.0.0/0}"
: "${POSTGRES_DB:=zakros}"
: "${POSTGRES_USER:=zakros}"

PG_MAJOR=17

echo "==> apt update + base deps"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl ca-certificates gnupg lsb-release

if [ ! -f /etc/apt/sources.list.d/pgdg.list ]; then
  echo "==> Adding PGDG apt repo (Postgres ${PG_MAJOR})"
  install -d -m 0755 /etc/apt/keyrings
  curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc \
    | gpg --dearmor -o /etc/apt/keyrings/postgresql.gpg
  echo "deb [signed-by=/etc/apt/keyrings/postgresql.gpg] http://apt.postgresql.org/pub/repos/apt $(lsb_release -cs)-pgdg main" \
    > /etc/apt/sources.list.d/pgdg.list
  apt-get update -qq
fi

echo "==> Installing postgresql-${PG_MAJOR} + pgvector"
apt-get install -y -qq "postgresql-${PG_MAJOR}" "postgresql-${PG_MAJOR}-pgvector"

PG_CONF="/etc/postgresql/${PG_MAJOR}/main/postgresql.conf"
PG_HBA="/etc/postgresql/${PG_MAJOR}/main/pg_hba.conf"

echo "==> Configuring listen_addresses + pg_hba"
sed -i "s/^#\?listen_addresses = .*/listen_addresses = '*'/" "${PG_CONF}"

HBA_LINE="host    ${POSTGRES_DB}    ${POSTGRES_USER}    ${POSTGRES_ALLOWED_CIDR}    scram-sha-256"
if ! grep -qF "${HBA_LINE}" "${PG_HBA}"; then
  echo "${HBA_LINE}" >> "${PG_HBA}"
fi

systemctl enable --now "postgresql@${PG_MAJOR}-main"
systemctl restart "postgresql@${PG_MAJOR}-main"

echo "==> Creating role + database"
runuser -u postgres -- psql -v ON_ERROR_STOP=1 <<SQL
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '${POSTGRES_USER}') THEN
    CREATE ROLE ${POSTGRES_USER} LOGIN PASSWORD '${POSTGRES_PASSWORD}';
  ELSE
    ALTER ROLE ${POSTGRES_USER} WITH PASSWORD '${POSTGRES_PASSWORD}';
  END IF;
END
\$\$;

SELECT 'CREATE DATABASE ${POSTGRES_DB} OWNER ${POSTGRES_USER}'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${POSTGRES_DB}')\gexec
SQL

echo "==> Enabling pgvector extension"
runuser -u postgres -- psql -d "${POSTGRES_DB}" -c "CREATE EXTENSION IF NOT EXISTS vector;"

echo "==> Done. Test from your workstation:"
echo "    PGPASSWORD='${POSTGRES_PASSWORD}' psql -h <postgres-vm-ip> -U ${POSTGRES_USER} -d ${POSTGRES_DB} -c 'SELECT version();'"
