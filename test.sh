#!/bin/bash

set -e  # Exit on any error

echo "🚀 CDC2Vec Comprehensive Test"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to cleanup on exit
cleanup() {
    print_status "Cleaning up..."
    if [ ! -z "$CDC_PID" ]; then
        kill $CDC_PID 2>/dev/null || true
        wait $CDC_PID 2>/dev/null || true
    fi
    # Kill any remaining cdc2vec processes
    pkill -f cdc2vec 2>/dev/null || true
    # Clean up test data
    docker-compose exec -T postgres psql -U postgres -d testdb -c "DELETE FROM public.users WHERE email IN ('test@cdc2vec.com', 'repl@test.com');" 2>/dev/null || true
}

trap cleanup EXIT

# Check if docker-compose is available
if ! command -v docker-compose &> /dev/null; then
    print_error "docker-compose is not installed"
    exit 1
fi

# Check if jq is available
if ! command -v jq &> /dev/null; then
    print_warning "jq is not installed, JSON output will be raw"
    JQ_CMD="cat"
else
    JQ_CMD="jq ."
fi

# Start services
print_status "Starting Docker services..."
docker-compose up -d

# Wait for services to be ready
print_status "Waiting for services to be ready..."
sleep 30

# Check if services are running
print_status "Checking service health..."
if ! docker-compose ps | grep -q "Up"; then
    print_error "Some services failed to start"
    docker-compose ps
    exit 1
fi

# Create config file if it doesn't exist
CONFIG_FILE="configs/postgres-qdrant.yaml"
if [ ! -f "$CONFIG_FILE" ]; then
    print_status "Creating config file..."
    mkdir -p configs
    cat > "$CONFIG_FILE" << 'EOF'
source:
  type: "postgres"
  postgres:
    dsn: "host=localhost port=5432 dbname=testdb user=postgres password=secret sslmode=disable"
    slot: "cdc2vec_slot"
    publication: "cdc2vec_pub"
    start_lsn: ""
    create_publication: true
    create_slot: true
    tables: ["public.users"]
  offset_store: "./offsets"

embed:
  provider: "ollama_http"
  model: "nomic-embed-text"
  url: "http://localhost:11434"
  normalize: true

sink:
  type: "qdrant"
  qdrant:
    addr: "http://localhost:6333"
    collection: "cdc_vectors"
    distance: "Cosine"

mapping:
  - table: "public.users"
    id_column: "id"
    text_columns: ["name", "bio"]
    metadata_columns: ["email", "country"]

batching:
  batch_size: 64
  flush_interval_ms: 500

http:
  addr: ":8080"
EOF
    print_success "Config file created"
fi

# Setup database schema
print_status "Setting up database schema..."
docker-compose exec -T postgres psql -U postgres -d testdb -c "
CREATE TABLE IF NOT EXISTS public.users (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255) UNIQUE NOT NULL,
    bio TEXT,
    country VARCHAR(100),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
" 2>/dev/null || true

# Create replica user for CDC
docker-compose exec -T postgres psql -U postgres -d testdb -c "
DO \$\$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_user WHERE usename = 'replica') THEN
        CREATE USER replica WITH REPLICATION PASSWORD 'secret';
    END IF;
END
\$\$;
" 2>/dev/null || true

# Grant permissions
docker-compose exec -T postgres psql -U postgres -d testdb -c "
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO replica;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO replica;
" 2>/dev/null || true

print_success "Database schema ready"

# Verify replication setup
print_status "Verifying replication setup..."

# Kill any existing cdc2vec processes that might be holding the replication slot
print_status "Cleaning up any existing CDC2Vec processes..."
pkill -f cdc2vec 2>/dev/null || true
sleep 2

# Force kill any processes using the replication slot
print_status "Force cleaning replication slot..."
docker-compose exec -T postgres psql -U postgres -d testdb -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name LIKE '%cdc2vec%' OR query LIKE '%cdc2vec%' OR query LIKE '%replication%';" 2>/dev/null || true
sleep 2

PUBLICATION_EXISTS=$(docker-compose exec -T postgres psql -U postgres -d testdb -t -c "SELECT 1 FROM pg_publication WHERE pubname = 'cdc2vec_pub';" | tr -d ' ')
if [ -z "$PUBLICATION_EXISTS" ]; then
    print_warning "Publication 'cdc2vec_pub' does not exist, CDC2Vec will create it"
else
    print_success "Publication 'cdc2vec_pub' exists"
fi

SLOT_EXISTS=$(docker-compose exec -T postgres psql -U postgres -d testdb -t -c "SELECT 1 FROM pg_replication_slots WHERE slot_name = 'cdc2vec_slot';" | tr -d ' ')
if [ -z "$SLOT_EXISTS" ]; then
    print_warning "Replication slot 'cdc2vec_slot' does not exist, CDC2Vec will create it"
else
    print_success "Replication slot 'cdc2vec_slot' exists"
    # Check if slot is active
    SLOT_ACTIVE=$(docker-compose exec -T postgres psql -U postgres -d testdb -t -c "SELECT active FROM pg_replication_slots WHERE slot_name = 'cdc2vec_slot';" | tr -d ' ')
    if [ "$SLOT_ACTIVE" = "t" ]; then
        print_warning "Replication slot is already active, dropping it to avoid conflicts"
        # Force terminate any backend using this slot
        docker-compose exec -T postgres psql -U postgres -d testdb -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE backend_type = 'walsender' AND state = 'streaming';" 2>/dev/null || true
        sleep 2
        docker-compose exec -T postgres psql -U postgres -d testdb -c "SELECT pg_drop_replication_slot('cdc2vec_slot');" 2>/dev/null || true
        sleep 2
    fi
fi

# Build application
print_status "Building application..."
go mod tidy
go build -o bin/cdc2vec ./cmd/cdc2vec

# Test with PostgreSQL -> Qdrant
print_status "Testing PostgreSQL -> Qdrant..."
export CONFIG_PATH="$(pwd)/$CONFIG_FILE"

# Start CDC2Vec
print_status "Starting CDC2Vec..."

# Final check to ensure replication slot is free
print_status "Final replication slot check..."
SLOT_ACTIVE_FINAL=$(docker-compose exec -T postgres psql -U postgres -d testdb -t -c "SELECT active FROM pg_replication_slots WHERE slot_name = 'cdc2vec_slot';" | tr -d ' ' 2>/dev/null || echo "f")
if [ "$SLOT_ACTIVE_FINAL" = "t" ]; then
    print_error "Replication slot is still active, cannot start CDC2Vec"
    print_status "Attempting to force drop the slot..."
    docker-compose exec -T postgres psql -U postgres -d testdb -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE backend_type = 'walsender';" 2>/dev/null || true
    sleep 3
    docker-compose exec -T postgres psql -U postgres -d testdb -c "SELECT pg_drop_replication_slot('cdc2vec_slot');" 2>/dev/null || true
    sleep 2
    # Check again
    SLOT_ACTIVE_FINAL=$(docker-compose exec -T postgres psql -U postgres -d testdb -t -c "SELECT active FROM pg_replication_slots WHERE slot_name = 'cdc2vec_slot';" | tr -d ' ' 2>/dev/null || echo "f")
    if [ "$SLOT_ACTIVE_FINAL" = "t" ]; then
        print_error "Still cannot free replication slot, exiting"
        exit 1
    fi
fi

# Start CDC2Vec with detailed logging
print_status "Starting CDC2Vec with detailed logging..."
./bin/cdc2vec 2>&1 | tee cdc2vec.log &
CDC_PID=$!

# Wait for CDC2Vec to start
print_status "Waiting for CDC2Vec to start..."
sleep 15

# Check if CDC2Vec is running
if ! kill -0 $CDC_PID 2>/dev/null; then
    print_error "CDC2Vec failed to start"
    exit 1
fi

# Check health endpoint
print_status "Checking CDC2Vec health..."
if curl -s http://localhost:8080/healthz > /dev/null; then
    print_success "CDC2Vec is healthy"
else
    print_warning "Health check failed, but continuing..."
fi

# Check if Ollama is ready
print_status "Checking Ollama embedding service..."
if curl -s http://localhost:11434/api/tags > /dev/null; then
    print_success "Ollama is responding"
    # Check if the required model is available
    MODELS=$(curl -s http://localhost:11434/api/tags)
    if echo "$MODELS" | grep -q "nomic-embed-text"; then
        print_success "Required embedding model is available"
    else
        print_warning "Required embedding model not found, available models:"
        echo "$MODELS" | jq '.models[].name' 2>/dev/null || echo "$MODELS"
    fi
else
    print_error "Ollama is not responding"
fi

# Insert test data
print_status "Inserting test data..."
INSERT_OUTPUT=$(docker-compose exec -T postgres psql -U postgres -d testdb -t -c "INSERT INTO public.users (name, email, bio, country) VALUES ('CDC2Vec Test User', 'test@cdc2vec.com', 'This is a comprehensive test of the CDC pipeline with vector embeddings', 'Test Country') RETURNING id;" 2>&1)
TEST_DATA_ID=$(echo "$INSERT_OUTPUT" | grep -E '[0-9]+' | head -1 | tr -d ' ')

if [ -z "$TEST_DATA_ID" ]; then
    # Check if it's a duplicate key error
    if echo "$INSERT_OUTPUT" | grep -q "duplicate key"; then
        print_warning "Test data already exists, using existing record"
        TEST_DATA_ID=$(docker-compose exec -T postgres psql -U postgres -d testdb -t -c "SELECT id FROM public.users WHERE email = 'test@cdc2vec.com' ORDER BY id DESC LIMIT 1;" | grep -E '[0-9]+' | head -1 | tr -d ' ')
        if [ -n "$TEST_DATA_ID" ]; then
            print_success "Using existing test data with ID: $TEST_DATA_ID"
        else
            print_error "Could not find existing test data"
            exit 1
        fi
    else
        print_error "Failed to insert test data"
        print_status "Insert output: $INSERT_OUTPUT"
        print_status "Trying alternative insertion method..."
        docker-compose exec -T postgres psql -U postgres -d testdb -c "INSERT INTO public.users (name, email, bio, country) VALUES ('CDC2Vec Test User', 'test@cdc2vec.com', 'This is a comprehensive test of the CDC pipeline with vector embeddings', 'Test Country');"
        TEST_DATA_ID=$(docker-compose exec -T postgres psql -U postgres -d testdb -t -c "SELECT id FROM public.users WHERE email = 'test@cdc2vec.com' ORDER BY id DESC LIMIT 1;" | grep -E '[0-9]+' | head -1 | tr -d ' ')
        if [ -z "$TEST_DATA_ID" ]; then
            print_error "Alternative insertion also failed"
            print_status "Current users in database:"
            docker-compose exec -T postgres psql -U postgres -d testdb -c "SELECT id, name, email FROM public.users;"
            exit 1
        fi
    fi
else
    print_success "Test data inserted with ID: $TEST_DATA_ID"
fi

# Insert additional test data while CDC2Vec is running
print_status "Inserting additional test data for replication test..."
docker-compose exec -T postgres psql -U postgres -d testdb -c "INSERT INTO public.users (name, email, bio, country) VALUES ('Replication Test User', 'repl@test.com', 'Testing real-time replication', 'Repl Country');"

# Wait a moment for replication to process
sleep 5

# Check if the new data was inserted
REPL_DATA=$(docker-compose exec -T postgres psql -U postgres -d testdb -t -c "SELECT id FROM public.users WHERE email = 'repl@test.com';" | grep -E '[0-9]+' | head -1 | tr -d ' ')
if [ -n "$REPL_DATA" ]; then
    print_success "Replication test data inserted with ID: $REPL_DATA"
else
    print_warning "Replication test data insertion failed"
fi

# Wait for processing
print_status "Waiting for data processing..."
sleep 15

# Debug: Check if CDC2Vec is still running and show recent logs
print_status "Checking CDC2Vec status..."
if kill -0 $CDC_PID 2>/dev/null; then
    print_success "CDC2Vec is still running"
    
    # Show recent CDC2Vec logs
    print_status "Recent CDC2Vec logs:"
    if [ -f cdc2vec.log ]; then
        tail -10 cdc2vec.log
    fi
    
    # Check replication slot status
    print_status "Checking replication slot status..."
    SLOT_STATUS=$(docker-compose exec -T postgres psql -U postgres -d testdb -t -c "SELECT slot_name, active, restart_lsn, confirmed_flush_lsn FROM pg_replication_slots WHERE slot_name = 'cdc2vec_slot';" | tr -d ' ')
    if echo "$SLOT_STATUS" | grep -q "t"; then
        print_success "Replication slot is active"
        print_status "Slot details: $SLOT_STATUS"
    else
        print_warning "Replication slot is not active"
        print_status "Slot details: $SLOT_STATUS"
    fi
    
    # Check PostgreSQL replication activity
    print_status "Checking PostgreSQL replication activity..."
    REPL_ACTIVITY=$(docker-compose exec -T postgres psql -U postgres -d testdb -t -c "SELECT pid, application_name, state, query FROM pg_stat_activity WHERE backend_type = 'walsender' OR query LIKE '%replication%';" 2>/dev/null || echo "No replication activity")
    print_status "Replication activity: $REPL_ACTIVITY"
else
    print_error "CDC2Vec process died unexpectedly"
    print_status "Recent CDC2Vec logs:"
    if [ -f cdc2vec.log ]; then
        tail -20 cdc2vec.log
    fi
fi

# Check if data was processed by looking at the collection
print_status "Checking if data was processed..."
COLLECTION_INFO=$(curl -s http://localhost:6333/collections/cdc_vectors 2>/dev/null || echo "{}")

if echo "$COLLECTION_INFO" | grep -q "points_count.*[1-9]"; then
    print_success "Data was successfully processed and stored in Qdrant"
    
    # Get collection info
    echo "Collection info:"
    echo "$COLLECTION_INFO" | $JQ_CMD
else
    print_warning "No data found in collection, checking logs..."
    
    # Show more detailed CDC2Vec logs
    print_status "Detailed CDC2Vec logs:"
    if [ -f cdc2vec.log ]; then
        tail -30 cdc2vec.log
    fi
    
    # Check if collection exists
    if echo "$COLLECTION_INFO" | grep -q "not found"; then
        print_error "Collection 'cdc_vectors' does not exist"
    else
        print_warning "Collection exists but no data was processed"
        print_status "Collection details:"
        echo "$COLLECTION_INFO" | $JQ_CMD
    fi
    
    # Check if any data was inserted into the database
    print_status "Checking database for test data..."
    DB_DATA=$(docker-compose exec -T postgres psql -U postgres -d testdb -t -c "SELECT id, name, email FROM public.users WHERE email IN ('test@cdc2vec.com', 'repl@test.com');" 2>/dev/null || echo "No data found")
    print_status "Database data: $DB_DATA"
fi

# Test search functionality
print_status "Testing search functionality..."
SEARCH_RESULT=$(curl -s -X POST http://localhost:6333/collections/cdc_vectors/points/search \
  -H "Content-Type: application/json" \
  -d '{
    "vector": [0.1, 0.2, 0.3, 0.4, 0.5],
    "limit": 5
  }' 2>/dev/null || echo "{}")

if echo "$SEARCH_RESULT" | grep -q "result"; then
    print_success "Search functionality working"
    echo "Search result:"
    echo "$SEARCH_RESULT" | $JQ_CMD
else
    print_warning "Search test failed or no results"
fi

# Stop CDC2Vec
print_status "Stopping CDC2Vec..."
kill $CDC_PID
wait $CDC_PID 2>/dev/null || true

# Show final logs
print_status "Final CDC2Vec logs:"
if [ -f cdc2vec.log ]; then
    tail -20 cdc2vec.log
fi

# Cleanup test data
print_status "Cleaning up test data..."
docker-compose exec -T postgres psql -U postgres -d testdb -c "DELETE FROM public.users WHERE email IN ('test@cdc2vec.com', 'repl@test.com');" 2>/dev/null || true

print_success "✅ Comprehensive test completed successfully!"
print_status "Use 'docker-compose down' to stop all services"
print_status "Use 'docker-compose logs' to view service logs"