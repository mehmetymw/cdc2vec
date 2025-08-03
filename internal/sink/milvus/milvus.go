package milvus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
	"go.uber.org/zap"
)

type Sink struct {
	cli        client.Client
	collection string
	metric     string
	indexType  string
	dim        int
	logger     *zap.Logger
}

func New(addr, collection, metric, indexType string, logger *zap.Logger) (*Sink, error) {
	logger.Info("Creating Milvus sink", 
		zap.String("addr", addr),
		zap.String("collection", collection),
		zap.String("metric", metric),
		zap.String("index_type", indexType))
	
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	logger.Debug("Connecting to Milvus")
	cli, err := client.NewClient(ctx, client.Config{Address: addr})
	if err != nil {
		logger.Error("Failed to connect to Milvus", zap.Error(err))
		return nil, err
	}
	
	if metric == "" {
		metric = "IP"
		logger.Debug("Using default metric", zap.String("metric", metric))
	}
	if indexType == "" {
		indexType = "HNSW"
		logger.Debug("Using default index type", zap.String("index_type", indexType))
	}
	
	logger.Info("Milvus sink created successfully")
	return &Sink{cli: cli, collection: collection, metric: metric, indexType: indexType, logger: logger}, nil
}

func (s *Sink) ensure(dim int) error {
	if s.dim == dim {
		s.logger.Debug("Collection already configured with correct dimension", zap.Int("dim", dim))
		return nil
	}
	
	s.logger.Debug("Ensuring collection exists", 
		zap.String("collection", s.collection),
		zap.Int("dimension", dim))
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	exists, err := s.cli.HasCollection(ctx, s.collection)
	if err != nil {
		s.logger.Error("Failed to check collection existence", zap.Error(err))
		return err
	}
	
	if !exists {
		s.logger.Info("Creating new collection", 
			zap.String("collection", s.collection),
			zap.Int("dimension", dim))
		
		fields := []*entity.Field{
			entity.NewField().WithName("id").WithDataType(entity.FieldTypeVarChar).WithIsPrimaryKey(true).WithMaxLength(512),
			entity.NewField().WithName("vector").WithDataType(entity.FieldTypeFloatVector).WithDim(int64(dim)),
			entity.NewField().WithName("payload").WithDataType(entity.FieldTypeJSON),
		}
		schema := &entity.Schema{Fields: fields, CollectionName: s.collection}
		
		if err := s.cli.CreateCollection(ctx, schema, 2); err != nil {
			s.logger.Error("Failed to create collection", zap.Error(err))
			return err
		}
		
		s.logger.Debug("Creating index", 
			zap.String("metric", s.metric),
			zap.String("index_type", s.indexType))
		
		idx, err := entity.NewIndexHNSW(entity.MetricType(s.metric), 16, 200)
		if err != nil {
			s.logger.Error("Failed to create index config", zap.Error(err))
			return err
		}
		
		if err := s.cli.CreateIndex(ctx, s.collection, "vector", idx, false); err != nil {
			s.logger.Error("Failed to create index", zap.Error(err))
			return err
		}
		
		s.logger.Debug("Loading collection")
		if err := s.cli.LoadCollection(ctx, s.collection, false); err != nil {
			s.logger.Error("Failed to load collection", zap.Error(err))
			return err
		}
		
		s.logger.Info("Collection created and loaded successfully")
	} else {
		s.logger.Debug("Collection exists, loading it")
		if err := s.cli.LoadCollection(ctx, s.collection, false); err != nil {
			s.logger.Error("Failed to load existing collection", zap.Error(err))
			return err
		}
	}
	s.dim = dim
	return nil
}

func (s *Sink) Upsert(id string, vector []float32, metadata map[string]any) error {
	s.logger.Debug("Upserting to Milvus", 
		zap.String("id", id),
		zap.Int("vector_dim", len(vector)))
	
	if err := s.ensure(len(vector)); err != nil {
		return err
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	
	ids := entity.NewColumnVarChar("id", []string{id})
	vecs := entity.NewColumnFloatVector("vector", s.dim, [][]float32{vector})
	
	var payload entity.Column
	if metadata != nil {
		bytes, err := json.Marshal(metadata)
		if err != nil {
			s.logger.Error("Failed to marshal metadata", zap.Error(err))
			return err
		}
		payload = entity.NewColumnJSONBytes("payload", [][]byte{bytes})
	} else {
		payload = entity.NewColumnJSONBytes("payload", [][]byte{[]byte("{}")})
	}
	
	s.logger.Debug("Inserting data to Milvus")
	_, err := s.cli.Insert(ctx, s.collection, "", ids, vecs, payload)
	if err != nil {
		s.logger.Error("Failed to insert to Milvus", zap.Error(err))
		return err
	}
	
	s.logger.Debug("Successfully upserted to Milvus", zap.String("id", id))
	return nil
}

func (s *Sink) Delete(id string) error {
	s.logger.Debug("Deleting from Milvus", zap.String("id", id))
	
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	
	expr := fmt.Sprintf("id in [\"%s\"]", id)
	s.logger.Debug("Delete expression", zap.String("expr", expr))
	
	err := s.cli.Delete(ctx, s.collection, expr, "")
	if err != nil {
		s.logger.Error("Failed to delete from Milvus", zap.Error(err))
		return err
	}
	
	s.logger.Debug("Successfully deleted from Milvus", zap.String("id", id))
	return nil
}

func (s *Sink) Close() error {
	s.logger.Info("Closing Milvus connection")
	return s.cli.Close()
}
