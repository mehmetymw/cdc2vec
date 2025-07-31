package qdrant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Sink struct {
	baseURL    string
	collection string
	distance   string
	client     *http.Client
	dim        int
	mu         sync.Mutex
	logger     *zap.Logger
}

func New(addr, collection, distance string, logger *zap.Logger) (*Sink, error) {
	logger.Info("Creating Qdrant sink", 
		zap.String("addr", addr),
		zap.String("collection", collection),
		zap.String("distance", distance))
	
	base, err := normalizeBaseURL(addr)
	if err != nil {
		logger.Error("Failed to normalize base URL", zap.Error(err))
		return nil, err
	}
	if distance == "" {
		distance = "Cosine"
		logger.Debug("Using default distance metric", zap.String("distance", distance))
	}
	s := &Sink{
		baseURL:    base,
		collection: collection,
		distance:   distance,
		client:     &http.Client{Timeout: 15 * time.Second},
		logger:     logger,
	}
	logger.Info("Qdrant sink created successfully", 
		zap.String("baseURL", base), 
		zap.String("collection", collection),
		zap.String("distance", distance))
	return s, nil
}

func normalizeBaseURL(raw string) (string, error) {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	host := u.Host
	if host == "" {
		host = u.Path
		u.Path = ""
	}
	if !strings.Contains(host, ":") {
		host += ":6333"
	}
	if strings.HasSuffix(u.Scheme, "http") && strings.HasSuffix(host, ":6334") {
		return "", fmt.Errorf("use 6333 for HTTP; 6334 is gRPC")
	}
	u.Host = host
	return u.String(), nil
}

func (s *Sink) ensureCollection(dim int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	s.logger.Debug("Checking if collection exists", zap.String("collection", s.collection))
	getURL := fmt.Sprintf("%s/collections/%s", s.baseURL, s.collection)
	resp, err := s.client.Get(getURL)
	if err == nil && resp.StatusCode == 200 {
		s.logger.Info("Collection already exists", zap.String("collection", s.collection))
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		s.dim = dim
		return nil
	}
	if resp != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	
	s.logger.Info("Creating new collection", 
		zap.String("collection", s.collection),
		zap.Int("dimension", dim),
		zap.String("distance", s.distance))
	
	body := map[string]any{
		"vectors": map[string]any{
			"size":     dim,
			"distance": s.distance,
		},
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, getURL, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	cr, err := s.client.Do(req)
	if err != nil {
		s.logger.Error("Failed to create collection", zap.Error(err))
		return err
	}
	defer cr.Body.Close()
	if cr.StatusCode != 200 {
		msg, _ := io.ReadAll(cr.Body)
		s.logger.Error("Collection creation failed", 
			zap.Int("status", cr.StatusCode),
			zap.String("response", string(msg)))
		return fmt.Errorf("failed to create collection: %s", string(msg))
	}
	s.logger.Info("Collection created successfully", zap.String("collection", s.collection))
	s.dim = dim
	return nil
}

func normalizePointID(external string) string {
	if _, err := uuid.Parse(external); err == nil {
		return external
	}
	ns := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	return uuid.NewSHA1(ns, []byte(external)).String()
}

func (s *Sink) Upsert(id string, vec []float32, payload map[string]any) error {
	s.logger.Debug("Upserting point", 
		zap.String("id", id),
		zap.Int("vector_dim", len(vec)))
	
	if err := s.ensureCollection(len(vec)); err != nil {
		return err
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["ext_id"] = id
	
	normalizedID := normalizePointID(id)
	body := map[string]any{
		"ids":     []string{normalizedID},
		"vectors": [][]float32{vec},
		"payloads": []map[string]any{payload},
	}
	b, err := json.Marshal(body)
	if err != nil {
		s.logger.Error("Failed to marshal JSON", zap.Error(err))
		return err
	}
	
	// Debug: log the JSON structure (truncated for readability)
	jsonStr := string(b)
	if len(jsonStr) > 200 {
		s.logger.Debug("JSON payload (truncated)", zap.String("json", jsonStr[:200]+"..."))
	} else {
		s.logger.Debug("JSON payload", zap.String("json", jsonStr))
	}
	
	u := fmt.Sprintf("%s/collections/%s/points", s.baseURL, s.collection)
	
	s.logger.Debug("Sending upsert request", 
		zap.String("url", u),
		zap.String("normalized_id", normalizedID))
	
	resp, err := s.client.Post(u, "application/json", bytes.NewReader(b))
	if err != nil {
		s.logger.Error("Failed to send upsert request", zap.Error(err))
		return err
	}
	defer resp.Body.Close()
	
	// Always read the response body for debugging
	msg, _ := io.ReadAll(resp.Body)
	
	if resp.StatusCode != 200 && resp.StatusCode != 202 {
		s.logger.Error("Upsert failed", 
			zap.Int("status", resp.StatusCode),
			zap.String("response", string(msg)))
		return fmt.Errorf("failed to upsert point, status: %d, body: %s", resp.StatusCode, string(msg))
	}
	
	s.logger.Debug("Point upserted successfully", 
		zap.String("id", id),
		zap.Int("status", resp.StatusCode),
		zap.String("response", string(msg)))
	return nil
}

func (s *Sink) Delete(id string) error {
	s.logger.Debug("Deleting point", zap.String("id", id))
	
	normalizedID := normalizePointID(id)
	u := fmt.Sprintf("%s/collections/%s/points/delete", s.baseURL, s.collection)
	req := map[string]any{
		"points": []string{normalizedID},
	}
	b, _ := json.Marshal(req)
	
	s.logger.Debug("Sending delete request", 
		zap.String("url", u),
		zap.String("normalized_id", normalizedID))
	
	resp, err := s.client.Post(u, "application/json", bytes.NewReader(b))
	if err != nil {
		s.logger.Error("Failed to send delete request", zap.Error(err))
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		msg, _ := io.ReadAll(resp.Body)
		s.logger.Error("Delete failed", 
			zap.Int("status", resp.StatusCode),
			zap.String("response", string(msg)))
		return fmt.Errorf("failed to delete point, status: %d, body: %s", resp.StatusCode, string(msg))
	}
	io.Copy(io.Discard, resp.Body)
	s.logger.Debug("Point deleted successfully", zap.String("id", id))
	return nil
}

func (s *Sink) Close() error { return nil }
