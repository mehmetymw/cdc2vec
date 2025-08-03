# CDC2Vec 🚀

**Real-time Change Data Capture to Vector Database Pipeline**

CDC2Vec is a high-performance, real-time pipeline that captures database changes using Change Data Capture (CDC) and automatically converts them into vector embeddings for storage in vector databases. Perfect for building real-time semantic search, RAG systems, and AI-powered applications.

## ⚠️ **IMPORTANT: Development Status**

**This project is currently in active development and is NOT production-ready.**

### 🚨 **Critical Warnings:**

- **DO NOT use with production databases** that contain critical business data
- **DO NOT use with databases** where data loss would be catastrophic
- **Test thoroughly** in isolated environments before any production deployment
- **Monitor closely** during initial deployments
- **Backup your data** before testing with any database

### 🔬 **Recommended Usage:**
- Development and testing environments only
- Non-critical databases for experimentation
- Staging environments with test data
- Isolated Docker containers for learning

**Use at your own risk. The authors are not responsible for any data loss or system issues.**

## ✨ Features

- **Real-time CDC**: Captures database changes instantly using PostgreSQL logical replication
- **Automatic Vectorization**: Converts text data to embeddings using various providers
- **Multiple Sinks**: Support for Qdrant, Milvus (in progress), and Kafka
- **Batch Processing**: Efficient batching for high-throughput scenarios
- **Health Monitoring**: Built-in health checks and metrics
- **Docker Ready**: Containerized deployment with GitHub Container Registry

## 🗺️ Roadmap

| Feature                     | Status             | Description                                                            |
| --------------------------- | ------------------ | ---------------------------------------------------------------------- |
| **Sources**                 |                    |                                                                        |
| PostgreSQL CDC              | ✅ **Completed**   | Logical replication support with automatic slot/publication management |
| MySQL CDC                   | 📋 **Planned**     | Binlog-based change data capture                                       |
| MongoDB CDC                 | 📋 **Planned**     | Change streams support                                                 |
| **Embedding Providers**     |                    |                                                                        |
| Ollama HTTP                 | ✅ **Completed**   | Local embedding models via Ollama                                      |
| OpenAI API                  | 🚧 **In Progress** | OpenAI embedding models (text-embedding-ada-002, etc.)                 |
| Hugging Face                | 📋 **Planned**     | Direct model inference and API support                                 |
| Cohere                      | 📋 **Planned**     | Cohere embedding API integration                                       |
| **Vector Databases**        |                    |                                                                        |
| Qdrant                      | ✅ **Completed**   | Full HTTP API support with collection management                       |
| Milvus                      | 🚧 **In Progress** | Scalable vector database support                                       |
| Pinecone                    | 📋 **Planned**     | Managed vector database service                                        |
| Weaviate                    | 📋 **Planned**     | GraphQL-based vector database                                          |
| Chroma                      | 📋 **Planned**     | Open-source embedding database                                         |
| **Streaming & Messaging**   |                    |                                                                        |
| Kafka                       | ✅ **Completed**   | Stream vectors to Kafka topics                                         |
| Redis Streams               | 📋 **Planned**     | Redis-based streaming                                                  |
| Apache Pulsar               | 📋 **Planned**     | Distributed messaging system                                           |
| **Operations & Monitoring** |                    |                                                                        |
| Docker Support              | ✅ **Completed**   | Full containerization with multi-stage builds                          |
| Health Checks               | ✅ **Completed**   | HTTP endpoints for monitoring                                          |
| Structured Logging          | ✅ **Completed**   | JSON logging with configurable levels                                  |
| Prometheus Metrics          | 📋 **Planned**     | Detailed performance and business metrics                              |
| Grafana Dashboard           | 📋 **Planned**     | Pre-built monitoring dashboards                                        |
| **Deployment & Scaling**    |                    |                                                                        |
| Kubernetes Manifests        | 📋 **Planned**     | Production-ready K8s deployments                                       |
| Helm Charts                 | 📋 **Planned**     | Parameterized Kubernetes deployments                                   |
| Horizontal Scaling          | 📋 **Planned**     | Multi-instance coordination                                            |


### Legend

- ✅ **Completed**: Feature is implemented and tested
- 🚧 **In Progress**: Currently under development
- 📋 **Planned**: Scheduled for future development

## 🏗️ Architecture

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│  PostgreSQL │───▶│   CDC2Vec   │───▶│  Embedding  │───▶│   Vector    │
│  (Source)   │    │  Pipeline   │    │  Provider   │    │  Database   │
└─────────────┘    └─────────────┘    └─────────────┘    └─────────────┘
                           │
                           ▼
                   ┌─────────────┐
                   │   Health    │
                   │  Endpoint   │
                   └─────────────┘
```

## 🚀 Quick Start

> ⚠️ **Before you start:** This is a development project. Only use with test databases or isolated environments. Never use with production data without thorough testing.

### Using Docker (Recommended)

1. **Pull the Docker image:**

```bash
docker pull ghcr.io/mehmetymw/cdc2vec:latest
```

2. **Create a configuration file:**

```bash
curl -o config.yaml https://raw.githubusercontent.com/mehmetymw/cdc2vec/master/configs/postgres-qdrant.yaml

```

3. **Edit the configuration:**

```yaml
source:
  type: "postgres"
  postgres:
    dsn: "postgres://username:password@your-postgres:5432/database?sslmode=disable"
    slot: "cdc2vec_slot"
    publication: "cdc2vec_pub"
    create_publication: true
    create_slot: true
    tables: ["public.documents"]
  offset_store: "/data/offsets"

embed:
  provider: "ollama_http"
  model: "nomic-embed-text"
  url: "http://your-ollama:11434"
  normalize: true

sink:
  type: "qdrant"
  qdrant:
    addr: "http://your-qdrant:6333"
    collection: "documents"
    distance: "Cosine"

mapping:
  - table: "public.documents"
    id_column: "id"
    text_columns: ["title", "content"]
    metadata_columns: ["created_at", "author"]

batching:
  batch_size: 64
  flush_interval_ms: 1000

http:
  addr: ":8080"
```

4. **Run the container:**

```bash
docker run -d \
  --name cdc2vec \
  -p 8080:8080 \
  -v $(pwd)/config.yaml:/config.yaml \
  -v $(pwd)/offsets:/data/offsets \
  -e CONFIG_PATH=/config.yaml \
  ghcr.io/mehmetymw/cdc2vec:latest
```

### Using Docker Compose

```yaml
version: "3.8"
services:
  cdc2vec:
    image: ghcr.io/mehmetymw/cdc2vec:latest
    ports:
      - "8080:8080"
    volumes:
      - ./config.yaml:/config.yaml
      - ./offsets:/data/offsets
    environment:
      - CONFIG_PATH=/config.yaml
    depends_on:
      - postgres
      - qdrant
      - ollama
    restart: unless-stopped

  # Add your PostgreSQL, Qdrant, and Ollama services here
```

### Building from Source

1. **Clone the repository:**

```bash
git clone https://github.com/mehmetymw/cdc2vec.git
cd cdc2vec
```

2. **Build the application:**

```bash
go mod tidy
go build -o bin/cdc2vec ./cmd/cdc2vec
```

3. **Run with configuration:**

```bash
export CONFIG_PATH=config.yaml
./bin/cdc2vec
```

## 📋 Prerequisites

> ⚠️ **Setup Warning:** Configure PostgreSQL logical replication only on test/development databases. Production databases require careful planning and testing.

### PostgreSQL Setup

Your PostgreSQL instance must be configured for logical replication:

1. **Enable logical replication in `postgresql.conf`:**

```ini
wal_level = logical
max_wal_senders = 10
max_replication_slots = 10
```

2. **Allow replication connections in `pg_hba.conf`:**

```
host replication all 0.0.0.0/0 md5
```

3. **Create a replication user:**

```sql
CREATE USER replica WITH REPLICATION PASSWORD 'your_password';
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO replica;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO replica;
```

4. **Restart PostgreSQL** to apply configuration changes.

### Vector Database Setup

#### Qdrant

```bash
docker run -p 6333:6333 qdrant/qdrant:latest
```

#### Milvus (In Progress)

```bash
# Coming soon
```

### Embedding Provider Setup

#### Ollama

```bash
# Install Ollama
curl -fsSL https://ollama.ai/install.sh | sh

# Pull embedding model
ollama pull nomic-embed-text
```

#### OpenAI (In Progress)

```bash
# Coming soon
```

## ⚙️ Configuration

### Source Configuration

#### PostgreSQL

```yaml
source:
  type: "postgres"
  postgres:
    dsn: "postgres://user:pass@host:port/db?sslmode=disable"
    slot: "cdc2vec_slot" # Replication slot name
    publication: "cdc2vec_pub" # Publication name
    create_publication: true # Auto-create publication
    create_slot: true # Auto-create replication slot
    tables: ["public.table1"] # Tables to monitor
  offset_store: "/data/offsets" # Offset storage directory
```

#### MySQL (Planned)

```yaml
# Coming soon
```

### Embedding Providers

#### Ollama HTTP

```yaml
embed:
  provider: "ollama_http"
  model: "mxbai-embed-large"
  url: "http://localhost:11434"
  normalize: true # Normalize vectors
  vector_size: 1024 # Vector dimension
```

#### OpenAI (In Progress)

```yaml
embed:
  provider: "openai"
  model: "text-embedding-ada-002"
  api_key: "your-api-key"
  normalize: true
```

### Vector Database Sinks

#### Qdrant

```yaml
sink:
  type: "qdrant"
  qdrant:
    addr: "http://localhost:6333"
    collection: "documents"
    distance: "Cosine" # Cosine, Dot, Euclid
```

#### Milvus (In Progress)

```yaml
sink:
  type: "milvus"
  milvus:
    addr: "localhost:19530"
    collection: "documents"
    metric: "COSINE"
```

#### Kafka

```yaml
sink:
  type: "kafka"
  kafka:
    brokers: ["localhost:9092"]
    topic: "vectors"
```

### Table Mapping

```yaml
mapping:
  - table: "public.documents" # Source table
    id_column: "id" # Primary key column
    text_columns: ["title", "content"] # Columns to embed
    metadata_columns: ["author", "created_at"] # Metadata to store
```

### Batching Configuration

```yaml
batching:
  batch_size: 64 # Batch size for processing
  flush_interval_ms: 1000 # Max wait time for batch
```

## 🔍 Monitoring

### Health Check

CDC2Vec provides a health endpoint at `/healthz`:

```bash
curl http://localhost:8080/healthz
```

Response:

```json
{
  "status": "running",
  "last_offset": "0/1234567",
  "batch_size": 5,
  "timestamp": "2024-01-15T10:30:00Z"
}
```

### Logging

CDC2Vec uses structured logging with different levels:

- `DEBUG`: Detailed processing information
- `INFO`: General operational information
- `WARN`: Warning conditions
- `ERROR`: Error conditions

## 🛠️ Development

### Running Tests

```bash
go test -v ./...
```

### Building Docker Image

```bash
docker build -t cdc2vec .
```

### Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

## 📊 Performance

CDC2Vec is designed for high-performance scenarios:

- **Throughput**: Processes thousands of changes per second
- **Latency**: Sub-second change detection and processing
- **Memory**: Efficient batching minimizes memory usage
- **Scalability**: Horizontal scaling through multiple instances

### Benchmarks

Performance metrics from real benchmark tests on AMD Ryzen 9 9950X (32 cores):

#### Embedding Generation Performance

| Text Size | Avg Latency | Throughput | Memory/Op | Allocs/Op |
|-----------|-------------|------------|-----------|-----------|
| Small (~20 chars) | 212μs | ~4,700 ops/sec | 22.5KB | 127 |
| Medium (~250 chars) | 231μs | ~4,300 ops/sec | 23.0KB | 128 |
| Large (~2.5KB) | 292μs | ~3,400 ops/sec | 40.0KB | 131 |

#### System Performance Characteristics

| Component | Performance | Notes |
|-----------|-------------|-------|
| **PostgreSQL CDC** | ~1,000-5,000 changes/sec | Depends on WAL volume and network latency |
| **Vector Storage** | ~500-2,000 inserts/sec | Varies by vector dimension and batch size |
| **Memory Usage** | 50-200MB | Base usage, scales with batch size |
| **CPU Usage** | 10-30% | Single core, embedding generation dominant |

*Benchmarks measured with Ollama HTTP provider, results may vary with different embedding models and hardware.*

## 🔧 Troubleshooting

### Common Issues

#### Replication Slot Already Exists

```bash
# Connect to PostgreSQL and drop the slot
SELECT pg_drop_replication_slot('cdc2vec_slot');
```

#### Permission Denied

```bash
# Grant necessary permissions
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO your_user;
```

#### Connection Refused

- Check if PostgreSQL allows replication connections
- Verify `pg_hba.conf` configuration
- Ensure firewall allows connections

### Debug Mode

Enable debug logging by setting log level:

```yaml
# Add to your config or set environment variable
LOG_LEVEL: "DEBUG"
```


## 🤝 Contributing

Contributions are welcome! Please feel free to submit a Pull Request. For major changes, please open an issue first to discuss what you would like to change.

---

## ⚖️ **Legal Disclaimer**

**This software is provided "as is" without warranty of any kind.**

- The authors and contributors are not responsible for any data loss, system damage, or business interruption
- Users are responsible for testing the software thoroughly before any production use
- This project is in active development and may contain bugs or incomplete features
- Always backup your data before testing any CDC/vector database tools
- Use only in development, testing, or staging environments until the project reaches production-ready status

**By using this software, you acknowledge that you understand the risks and accept full responsibility for any consequences.**
