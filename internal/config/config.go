package config

import (
	"errors"
	"os"

	"gopkg.in/yaml.v3"
)

type SourceConfig struct {
	Type        string         `yaml:"type"`
	Postgres    PostgresSource `yaml:"postgres"`
	OffsetStore string         `yaml:"offset_store"`
}

type PostgresSource struct {
	DSN                string   `yaml:"dsn"`
	Slot               string   `yaml:"slot"`
	Publication        string   `yaml:"publication"`
	StartLSN           string   `yaml:"start_lsn"`
	CreatePublication  bool     `yaml:"create_publication"`
	CreateSlot         bool     `yaml:"create_slot"`
	Tables             []string `yaml:"tables"`
}


type EmbedConfig struct {
	Provider   string `yaml:"provider"`
	Model      string `yaml:"model"`
	URL        string `yaml:"url"`
	Normalize  bool   `yaml:"normalize"`
	VectorSize int    `yaml:"vector_size"`
}

type MilvusSink struct {
	Addr       string `yaml:"addr"`
	Collection string `yaml:"collection"`
	Metric     string `yaml:"metric"`
	IndexType  string `yaml:"index_type"`
}

type QdrantSink struct {
	Addr       string `yaml:"addr"`
	URL        string `yaml:"url"`        // Support both addr and url
	Collection string `yaml:"collection"`
	Distance   string `yaml:"distance"`
}

type KafkaSink struct {
	Brokers []string `yaml:"brokers"`
	Topic   string   `yaml:"topic"`
}

type SinkConfig struct {
	Type   string      `yaml:"type"`
	Milvus MilvusSink  `yaml:"milvus"`
	Qdrant QdrantSink  `yaml:"qdrant"`
	Kafka  KafkaSink   `yaml:"kafka"`
}

type Mapping struct {
	Table           string   `yaml:"table"`
	IDColumn        string   `yaml:"id_column"`
	TextColumns     []string `yaml:"text_columns"`
	MetadataColumns []string `yaml:"metadata_columns"`
}

type Batching struct {
	BatchSize       int `yaml:"batch_size"`
	FlushIntervalMs int `yaml:"flush_interval_ms"`
}

type HTTPConfig struct {
	Addr string `yaml:"addr"`
}

type Config struct {
	Source   SourceConfig `yaml:"source"`
	Embed    EmbedConfig  `yaml:"embed"`
	Sink     SinkConfig   `yaml:"sink"`
	Mapping  []Mapping    `yaml:"mapping"`
	Batching Batching     `yaml:"batching"`
	HTTP     HTTPConfig   `yaml:"http"`
}

func LoadFromEnv() (Config, error) {
	path := os.Getenv("CONFIG_PATH")
	if path == "" {
		return Config{}, errors.New("CONFIG_PATH is not set")
	}
	
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return Config{}, err
	}
	
	// Apply defaults
	if c.Batching.BatchSize <= 0 {
		c.Batching.BatchSize = 64
	}
	if c.Batching.FlushIntervalMs <= 0 {
		c.Batching.FlushIntervalMs = 500
	}
	if c.HTTP.Addr == "" {
		c.HTTP.Addr = ":8080"
	}
	if c.Embed.VectorSize <= 0 {
		c.Embed.VectorSize = 768 // Default for most embedding models
	}
	
	return c, nil
}
