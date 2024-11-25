#!/bin/bash

set -e
set -u

function create_user_and_database() {
	local DB=$1
	echo "  Creating user and database '$DB'"
	psql -v --username "$POSTGRES_USER" <<-EOSQL
	    CREATE DATABASE $DB;
	    GRANT ALL PRIVILEGES ON DATABASE $DB TO $POSTGRES_USER;
EOSQL
}

if [ -n "$POSTGRES_DB_LIST" ]; then
	echo "Multiple database creation requested: $POSTGRES_DB_LIST"
	for DB in $(echo $POSTGRES_DB_LIST | tr ',' ' '); do
		create_user_and_database $DB
	done
	echo "Multiple databases created"
fi