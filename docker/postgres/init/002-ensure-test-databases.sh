#!/bin/sh
# File này bảo đảm database integration và migration probe tồn tại cả khi Docker volume đã được tạo từ phiên bản cũ.

set -eu

# ensure_database tạo database do user PostgreSQL local sở hữu nếu database đó chưa tồn tại.
ensure_database() {
    database_name="$1"

    if ! psql -U "$POSTGRES_USER" -d postgres -tAc \
        "SELECT 1 FROM pg_database WHERE datname = '$database_name'" | grep -q 1; then
        createdb -U "$POSTGRES_USER" -O "$POSTGRES_USER" "$database_name"
    fi
}

ensure_database nexus_commerce_test
ensure_database nexus_commerce_migration_probe
