package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/mehmetymw/cdc2vec/internal/config"
)

type Provider interface {
	Embed(text string) ([]float32, error)
	Close() error
}

type ollamaHTTP struct {
	baseURL string
	model   string
	http    *http.Client
	logger  *zap.Logger
}

type ollamaReq struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaResp struct {
	Embedding []float32 `json:"embedding"`
}

func (o *ollamaHTTP) Embed(text string) ([]float32, error) {
	o.logger.Debug("Generating embedding", 
		zap.String("model", o.model),
		zap.Int("text_length", len(text)))
	
	b, _ := json.Marshal(ollamaReq{Model: o.model, Prompt: text})
	req, _ := http.NewRequest(http.MethodPost, o.baseURL+"/api/embeddings", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	
	start := time.Now()
	resp, err := o.http.Do(req)
	if err != nil {
		o.logger.Error("Failed to send embedding request", zap.Error(err))
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		var msg struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&msg)
		o.logger.Error("Ollama embedding failed", 
			zap.Int("status", resp.StatusCode),
			zap.String("error", msg.Error))
		return nil, errors.New("ollama embed failed")
	}
	
	var r ollamaResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		o.logger.Error("Failed to decode embedding response", zap.Error(err))
		return nil, err
	}
	
	duration := time.Since(start)
	o.logger.Debug("Embedding generated successfully", 
		zap.Int("vector_dim", len(r.Embedding)),
		zap.Duration("duration", duration))
	
	return r.Embedding, nil
}

func (o *ollamaHTTP) Close() error { return nil }

func NewProvider(ec config.EmbedConfig, logger *zap.Logger) (Provider, error) {
	logger.Info("Creating embeddings provider", 
		zap.String("provider", ec.Provider),
		zap.String("model", ec.Model),
		zap.String("url", ec.URL))
	
	if ec.Provider == "gorag_ollama" || ec.Provider == "ollama_http" {
		provider := &ollamaHTTP{
			baseURL: ec.URL, 
			model: ec.Model, 
			http: &http.Client{Timeout: 60 * time.Second}, 
			logger: logger,
		}
		logger.Info("Ollama HTTP provider created successfully")
		return provider, nil
	}
	
	logger.Error("Unknown embedding provider", zap.String("provider", ec.Provider))
	return nil, errors.New("unknown embed provider")
}
