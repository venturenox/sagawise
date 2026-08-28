#!/bin/bash

set -e
set -u

function create_user_and_database() {
	local DB=$1
	echo "  Creating user and database '$DB'"
	PGPASSWORD="$POSTGRESQL_PASSWORD" psql -v --username "$POSTGRESQL_USERNAME" <<-EOSQL
	    SELECT 'CREATE DATABASE $DB' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$DB')\gexec
	    GRANT ALL PRIVILEGES ON DATABASE $DB TO $POSTGRESQL_USERNAME;
EOSQL
}

if [ -n "$POSTGRESQL_DATABASE_LIST" ]; then
	echo "Multiple database creation requested: $POSTGRESQL_DATABASE_LIST"
	for DB in $(echo $POSTGRESQL_DATABASE_LIST | tr ',' ' '); do
		create_user_and_database $DB
	done
	echo "Multiple databases created"
fi