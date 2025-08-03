package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/mehmetymw/cdc2vec/internal/cdc/postgres"
	"github.com/mehmetymw/cdc2vec/internal/config"
	"github.com/mehmetymw/cdc2vec/internal/embeddings"
	"github.com/mehmetymw/cdc2vec/internal/pipeline"
	"github.com/mehmetymw/cdc2vec/internal/sink/kafka"
	"github.com/mehmetymw/cdc2vec/internal/sink/milvus"
	"github.com/mehmetymw/cdc2vec/internal/sink/qdrant"
	"github.com/mehmetymw/cdc2vec/internal/types"
)

type healthz struct {
	Status     string `json:"status"`
	LastOffset string `json:"last_offset"`
	BatchSize  int    `json:"batch_size"`
	Timestamp  string `json:"timestamp"`
}

func main() {
	zapConfig := zap.NewProductionConfig()
	zapConfig.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	logger, _ := zapConfig.Build()

	defer logger.Sync()

	logger.Info("Starting cdc2vec application")

	logger.Info("Loading configuration from environment")
	cfg, err := config.LoadFromEnv()
	if err != nil {
		logger.Fatal("config load failed", zap.Error(err))
	}
	logger.Info("Configuration loaded successfully",
		zap.String("source_type", cfg.Source.Type),
		zap.String("sink_type", cfg.Sink.Type),
		zap.String("embed_provider", cfg.Embed.Provider),
		zap.Int("batch_size", cfg.Batching.BatchSize))

	logger.Info("Initializing embeddings provider",
		zap.String("provider", cfg.Embed.Provider),
		zap.String("model", cfg.Embed.Model))
	embedder, err := embeddings.NewProvider(cfg.Embed, logger)
	if err != nil {
		logger.Fatal("embedder init failed", zap.Error(err))
	}
	defer func() {
		logger.Info("Closing embeddings provider")
		embedder.Close()
	}()

	logger.Info("Initializing sink", zap.String("type", cfg.Sink.Type))
	var sink types.Sink
	if cfg.Sink.Type == "milvus" {
		logger.Info("Creating Milvus sink",
			zap.String("addr", cfg.Sink.Milvus.Addr),
			zap.String("collection", cfg.Sink.Milvus.Collection))
		sink, err = milvus.New(cfg.Sink.Milvus.Addr, cfg.Sink.Milvus.Collection, cfg.Sink.Milvus.Metric, cfg.Sink.Milvus.IndexType, logger)
	} else if cfg.Sink.Type == "qdrant" {
		// Support both addr and url fields for backward compatibility
		qdrantAddr := cfg.Sink.Qdrant.Addr
		if qdrantAddr == "" && cfg.Sink.Qdrant.URL != "" {
			qdrantAddr = cfg.Sink.Qdrant.URL
		}
		logger.Info("Creating Qdrant sink",
			zap.String("addr", qdrantAddr),
			zap.String("collection", cfg.Sink.Qdrant.Collection))
		sink, err = qdrant.New(qdrantAddr, cfg.Sink.Qdrant.Collection, cfg.Sink.Qdrant.Distance, cfg.Embed.VectorSize, logger)
	} else if cfg.Sink.Type == "kafka" {
		logger.Info("Creating Kafka sink",
			zap.Strings("brokers", cfg.Sink.Kafka.Brokers),
			zap.String("topic", cfg.Sink.Kafka.Topic))
		sink, err = kafka.New(cfg.Sink.Kafka.Brokers, cfg.Sink.Kafka.Topic, logger)
	} else {
		err = errors.New("unknown sink type")
	}
	if err != nil {
		logger.Fatal("sink init failed", zap.Error(err))
	}
	defer func() {
		logger.Info("Closing sink")
		sink.Close()
	}()

	logger.Info("Creating pipeline",
		zap.Int("mappings_count", len(cfg.Mapping)),
		zap.Bool("normalize", cfg.Embed.Normalize))
	pl := pipeline.NewPipeline(embedder, sink, cfg.Mapping, cfg.Batching, cfg.Embed.Normalize, logger)

	changes := make(chan types.RowChange, 10000)
	logger.Info("Created change channel", zap.Int("buffer_size", 10000))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info("Starting pipeline")
		pl.Start(changes, pipeline.NewFileOffsetSaver(cfg.Source.OffsetStore, logger))
		logger.Info("Pipeline stopped")
	}()

	logger.Info("Initializing source", zap.String("type", cfg.Source.Type))
	var srcStop func()
	switch cfg.Source.Type {
	case "postgres":
		logger.Info("Creating PostgreSQL source with improved pglogrepl")
		pg, err := postgres.New(cfg, cfg.Mapping, logger)
		if err != nil {
			logger.Fatal("postgres init failed", zap.Error(err))
		}
		srcStop = pg.Stop
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info("Starting PostgreSQL source")
			pg.Run(changes)
			logger.Info("PostgreSQL source stopped")
		}()
	default:
		logger.Fatal("unknown source type", zap.String("type", cfg.Source.Type))
	}

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("Health check requested")
		st := pl.Status()
		resp := healthz{Status: "running", LastOffset: st.LastOffset, BatchSize: st.PendingBatch, Timestamp: time.Now().Format(time.RFC3339)}
		b, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(b)
	})
	server := &http.Server{Addr: cfg.HTTP.Addr}
	logger.Info("Starting HTTP server", zap.String("addr", cfg.HTTP.Addr))
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server failed", zap.Error(err))
		}
	}()

	// Set up signal handling for graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	logger.Info("Application started successfully, waiting for signals")
	<-quit

	logger.Info("Shutting down gracefully...")

	// Start graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Stop accepting new changes
	close(changes)
	logger.Info("Closed changes channel")

	// Stop the source first
	if srcStop != nil {
		logger.Info("Stopping source")
		srcStop()
	}

	// Close the pipeline
	logger.Info("Closing pipeline")
	pl.Close()

	// Shutdown HTTP server
	logger.Info("Shutting down HTTP server")
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", zap.Error(err))
	}

	// Wait for all goroutines to finish with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("All goroutines finished successfully")
	case <-shutdownCtx.Done():
		logger.Warn("Shutdown timeout reached, forcing exit")
	}

	logger.Info("Shutdown complete")
}
