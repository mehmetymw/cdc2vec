# CDC2Vec

A Change Data Capture (CDC) to Vector Database pipeline that captures database changes and converts them to embeddings for vector search.

## Features

- **Multiple CDC Sources**: PostgreSQL (with pglogrepl)
- **Multiple Sinks**: Milvus, Qdrant, Kafka
- **Embedding Providers**: Ollama HTTP
- **Real-time Processing**: Streaming CDC with configurable batching
- **Comprehensive Logging**: Structured logging with zap
- **Health Monitoring**: HTTP health endpoint

## Architecture

```
Database Changes → CDC Capture → Text Extraction → Embedding Generation → Vector Storage/Kafka
```

## Quick Start

### 1. Configuration

Copy the example configuration:
```bash
cp config.example.yaml config.yaml
```

Edit `config.yaml` with your settings:

```yaml
source:
  type: "postgres"
  postgres:
    dsn: "postgres://user:password@localhost:5432/mydb"
    slot: "cdc2vec_slot"
    publication: "cdc2vec_pub"
    use_wal_listener: true  # Use improved WAL listener
    
sink:
  type: "kafka"  # or "milvus", "qdrant"
  kafka:
    brokers: ["localhost:9092"]
    topic: "cdc2vec-embeddings"

embed:
  provider: "ollama_http"
  model: "nomic-embed-text"
  url: "http://localhost:11434"
```

### 2. Setup PostgreSQL

Enable logical replication:
```sql
-- Set wal_level to logical
ALTER SYSTEM SET wal_level = logical;
-- Restart PostgreSQL

-- Create publication (optional, can be auto-created)
CREATE PUBLICATION cdc2vec_pub FOR ALL TABLES;

-- Create replication slot (optional, can be auto-created)
SELECT pg_create_logical_replication_slot('cdc2vec_slot', 'pgoutput');
```

### 3. Run the Application

```bash
export CONFIG_PATH=./config.yaml
go run cmd/cdc2vec/main.go
```

## Configuration Options

### Source Configuration

#### PostgreSQL
```yaml
source:
  type: "postgres"
  postgres:
    dsn: "postgres://user:pass@host:port/db"
    slot: "replication_slot_name"
    publication: "publication_name"
    create_publication: true  # Auto-create publication
    create_slot: true         # Auto-create replication slot
    use_wal_listener: true    # Use wal-listener library (recommended)
    tables: ["schema.table1", "schema.table2"]
```

### Sink Configuration

#### Kafka
```yaml
sink:
  type: "kafka"
  kafka:
    brokers: ["broker1:9092", "broker2:9092"]
    topic: "embeddings-topic"
```

#### Milvus
```yaml
sink:
  type: "milvus"
  milvus:
    addr: "localhost:19530"
    collection: "documents"
    metric: "IP"  # or "L2", "COSINE"
    index_type: "HNSW"
```

#### Qdrant
```yaml
sink:
  type: "qdrant"
  qdrant:
    addr: "localhost:6333"
    collection: "documents"
    distance: "Cosine"  # or "Dot", "Euclid"
```

### Embedding Configuration

```yaml
embed:
  provider: "ollama_http"
  model: "nomic-embed-text"
  url: "http://localhost:11434"
  normalize: true  # L2 normalize vectors
```

### Table Mapping

```yaml
mapping:
  - table: "public.documents"
    id_column: "id"
    text_columns: ["title", "content", "description"]
    metadata_columns: ["created_at", "author", "category"]
```

## Kafka Message Format

When using Kafka sink, messages are published in this format:

```json
{
  "id": "public.documents:123",
  "vector": [0.1, 0.2, ...],
  "metadata": {
    "table": "public.documents",
    "pk": "123",
    "created_at": "2024-01-01T00:00:00Z",
    "author": "John Doe"
  },
  "op": "upsert",  // or "delete"
  "table": "public.documents",
  "pk": "123"
}
```

## Monitoring

### Health Endpoint

```bash
curl http://localhost:8080/healthz
```

Response:
```json
{
  "status": "running",
  "last_offset": "16/B374D848",
  "batch_size": 5,
  "timestamp": "2024-01-01T12:00:00Z"
}
```

### Logging

The application uses structured logging with different levels:
- `INFO`: Important operational events
- `DEBUG`: Detailed tracing (enable with debug level)
- `WARN`: Non-fatal issues
- `ERROR`: Error conditions

## Development

### Building

```bash
go build -o cdc2vec cmd/cdc2vec/main.go
```

### Testing

```bash
go test ./...
```

### Dependencies

- `github.com/ihippik/wal-listener`: PostgreSQL WAL listener
- `github.com/segmentio/kafka-go`: Kafka client
- `github.com/milvus-io/milvus-sdk-go/v2`: Milvus client
- `go.uber.org/zap`: Structured logging

## Troubleshooting

### PostgreSQL Issues

1. **Permission denied for replication**
   ```sql
   ALTER USER your_user REPLICATION;
   ```

2. **WAL level not logical**
   ```sql
   ALTER SYSTEM SET wal_level = logical;
   -- Restart PostgreSQL
   ```

3. **Unknown XLog message types**
   - Use `use_wal_listener: true` for better message handling

### Performance Tuning

1. **Batch Size**: Increase `batch_size` for higher throughput
2. **Flush Interval**: Decrease `flush_interval_ms` for lower latency
3. **Channel Buffer**: Increase buffer size in code if needed

## License

MIT License