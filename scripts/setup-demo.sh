#!/bin/bash
# Setup script for cdc2vec demo

echo "Setting up demo environment..."

# Copy test.sql to postgres container
docker-compose exec postgres mkdir -p /tmp || true
docker cp test.sql $(docker-compose ps -q postgres):/tmp/test.sql

echo "Demo setup complete!"
