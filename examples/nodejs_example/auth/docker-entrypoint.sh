#!/bin/bash

set -e

echo "Running database migrations"
yarn migrate

echo "Seeding database"
yarn seed

echo "Starting server"
yarn start
