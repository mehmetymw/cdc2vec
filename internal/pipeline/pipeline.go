package pipeline

import (
	"fmt"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/mehmetymw/cdc2vec/internal/config"
	"github.com/mehmetymw/cdc2vec/internal/embeddings"
	"github.com/mehmetymw/cdc2vec/internal/types"
	"github.com/mehmetymw/cdc2vec/internal/util"
)

type Pipeline struct {
	embedder   embeddings.Provider
	sink       types.Sink
	mappings   map[string]config.Mapping
	cfg        config.Batching
	logger     *zap.Logger
	batch      []types.RowChange
	mu         sync.Mutex
	lastOffset string
	normalize  bool
}

type OffsetSaver interface {
	SaveOffset(offset string) error
}

func NewFileOffsetSaver(dir string, logger *zap.Logger) *fileOffsetSaver {
	logger.Debug("Creating file offset saver", zap.String("dir", dir))
	os.MkdirAll(dir, 0o755)
	return &fileOffsetSaver{path: dir, logger: logger}
}

type fileOffsetSaver struct {
	path   string
	logger *zap.Logger
}

func (f *fileOffsetSaver) SaveOffset(offset string) error {
	if offset == "" {
		return nil
	}
	p := f.path + "/offset"
	f.logger.Debug("Saving offset to file", 
		zap.String("path", p),
		zap.String("offset", offset))
	return os.WriteFile(p, []byte(offset), 0o644)
}

func NewPipeline(embedder embeddings.Provider, sink types.Sink, mappings []config.Mapping, cfg config.Batching, normalize bool, logger *zap.Logger) *Pipeline {
	logger.Info("Creating new pipeline", 
		zap.Int("mappings_count", len(mappings)),
		zap.Int("batch_size", cfg.BatchSize),
		zap.Int("flush_interval_ms", cfg.FlushIntervalMs),
		zap.Bool("normalize", normalize))
	
	mappingMap := make(map[string]config.Mapping)
	for _, m := range mappings {
		mappingMap[m.Table] = m
		logger.Debug("Added table mapping", 
			zap.String("table", m.Table),
			zap.String("id_column", m.IDColumn),
			zap.Strings("text_columns", m.TextColumns),
			zap.Strings("metadata_columns", m.MetadataColumns))
	}
	return &Pipeline{embedder: embedder, sink: sink, mappings: mappingMap, cfg: cfg, logger: logger, normalize: normalize}
}

func (p *Pipeline) Start(changeCh <-chan types.RowChange, offsetSaver OffsetSaver) {
	p.logger.Info("Starting pipeline processing loop")
	ticker := time.NewTicker(time.Duration(p.cfg.FlushIntervalMs) * time.Millisecond)
	defer ticker.Stop()
	
	changeCount := 0
	for {
		select {
		case change, ok := <-changeCh:
			if !ok {
				p.logger.Info("Change channel closed, flushing final batch")
				p.flush(offsetSaver)
				return
			}
			changeCount++
			p.logger.Info("Received change from CDC", 
				zap.String("table", change.Schema+"."+change.Table),
				zap.String("op", change.Op),
				zap.String("primary_key", change.PrimaryKey),
				zap.String("lsn", change.LSN),
				zap.Int("total_changes", changeCount))
			
			p.addToBatch(change)
			if len(p.batch) >= p.cfg.BatchSize {
				p.logger.Debug("Batch size reached, flushing", 
					zap.Int("batch_size", len(p.batch)),
					zap.Int("total_changes", changeCount))
				p.flush(offsetSaver)
			}
		case <-ticker.C:
			if len(p.batch) > 0 {
				p.logger.Debug("Flush interval reached, flushing", 
					zap.Int("batch_size", len(p.batch)))
				p.flush(offsetSaver)
			}
		}
	}
}

func (p *Pipeline) addToBatch(change types.RowChange) {
	p.mu.Lock()
	defer p.mu.Unlock()
	tableName := fmt.Sprintf("%s.%s", change.Schema, change.Table)
	_, exists := p.mappings[tableName]
	if !exists {
		p.logger.Debug("Skipping change for unmapped table", zap.String("table", tableName))
		return
	}
	p.batch = append(p.batch, change)
	if change.LSN != "" {
		p.lastOffset = change.LSN
	} else if change.BinlogPos != "" {
		p.lastOffset = change.BinlogPos
	}
	p.logger.Debug("Added change to batch", 
		zap.String("op", change.Op),
		zap.String("table", tableName),
		zap.String("primary_key", change.PrimaryKey),
		zap.Int("batch_size", len(p.batch)))
}

func (p *Pipeline) flush(offsetSaver OffsetSaver) {
	p.mu.Lock()
	batch := p.batch
	offset := p.lastOffset
	p.batch = nil
	p.mu.Unlock()
	
	if len(batch) == 0 {
		return
	}
	
	p.logger.Info("Flushing batch", 
		zap.Int("batch_size", len(batch)),
		zap.String("offset", offset))
	
	start := time.Now()
	ok := true
	processed := 0
	
	for _, change := range batch {
		if err := p.processChange(change); err != nil {
			p.logger.Error("Failed to process change", 
				zap.Error(err), 
				zap.String("table", change.Table),
				zap.String("op", change.Op),
				zap.String("primary_key", change.PrimaryKey))
			ok = false
		} else {
			processed++
		}
	}
	
	duration := time.Since(start)
	p.logger.Info("Batch processing completed", 
		zap.Int("processed", processed),
		zap.Int("failed", len(batch)-processed),
		zap.Duration("duration", duration))
	
	if ok && offset != "" && offsetSaver != nil {
		if err := offsetSaver.SaveOffset(offset); err != nil {
			p.logger.Error("Failed to save offset", zap.Error(err))
		} else {
			p.logger.Debug("Offset saved successfully", zap.String("offset", offset))
		}
	}
}

func (p *Pipeline) processChange(change types.RowChange) error {
	tableName := fmt.Sprintf("%s.%s", change.Schema, change.Table)
	mapping, exists := p.mappings[tableName]
	if !exists {
		return nil
	}
	
	id := fmt.Sprintf("%s:%s", tableName, change.PrimaryKey)
	
	if change.Op == "d" {
		p.logger.Debug("Processing delete", 
			zap.String("id", id),
			zap.String("table", tableName))
		return p.sink.Delete(id)
	}
	
	data := change.After
	if data == nil {
		p.logger.Debug("Skipping change with no after data", zap.String("id", id))
		return nil
	}
	
	text := util.ConcatenateColumns(data, mapping.TextColumns)
	if text == "" {
		p.logger.Debug("Skipping change with empty text", zap.String("id", id))
		return nil
	}
	
	p.logger.Debug("Generating embedding", 
		zap.String("id", id),
		zap.Int("text_length", len(text)))
	
	vector, err := p.embedder.Embed(text)
	if err != nil {
		p.logger.Error("Failed to generate embedding", 
			zap.Error(err),
			zap.String("id", id),
			zap.String("text", text[:min(100, len(text))]))
		return err
	}
	
	if len(vector) == 0 {
		p.logger.Error("Received empty vector from embedder", 
			zap.String("id", id),
			zap.String("text", text))
		return fmt.Errorf("empty vector received for text: %s", text)
	}
	
	if p.normalize {
		vector = util.NormalizeVector(vector)
	}
	
	metadata := make(map[string]any)
	metadata["table"] = tableName
	metadata["pk"] = change.PrimaryKey
	for _, col := range mapping.MetadataColumns {
		if val, ok := data[col]; ok {
			metadata[col] = val
		}
	}
	
	p.logger.Debug("Upserting to sink", 
		zap.String("id", id),
		zap.Int("vector_dim", len(vector)),
		zap.Any("metadata", metadata))
	
	if err := p.sink.Upsert(id, vector, metadata); err != nil {
		p.logger.Error("Failed to upsert to sink", 
			zap.Error(err),
			zap.String("id", id),
			zap.Int("vector_dim", len(vector)))
		return err
	}
	
	p.logger.Debug("Successfully upserted to sink", zap.String("id", id))
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (p *Pipeline) Close() error {
	p.logger.Info("Closing pipeline")
	if p.embedder != nil {
		p.logger.Debug("Closing embedder")
		p.embedder.Close()
	}
	if p.sink != nil {
		p.logger.Debug("Closing sink")
		return p.sink.Close()
	}
	return nil
}

type pipelineStatus struct {
	LastOffset   string
	PendingBatch int
}

func (p *Pipeline) Status() pipelineStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return pipelineStatus{LastOffset: p.lastOffset, PendingBatch: len(p.batch)}
}
