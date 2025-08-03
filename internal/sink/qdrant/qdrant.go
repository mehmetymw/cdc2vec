package qdrant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

type Sink struct {
	baseURL    string
	collection string
	distance   string
	vectorSize int
	client     *http.Client
	dim        int
	mu         sync.Mutex
	logger     *zap.Logger
}

func New(addr, collection, distance string, vectorSize int, logger *zap.Logger) (*Sink, error) {
	base, err := normalizeBaseURL(addr)
	if err != nil {
		return nil, err
	}
	if distance == "" {
		distance = "Cosine"
	}
	return &Sink{
		baseURL:    base,
		collection: collection,
		distance:   distance,
		vectorSize: vectorSize,
		client:     &http.Client{Timeout: 15 * time.Second},
		logger:     logger,
	}, nil
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
	if strings.HasPrefix(u.Scheme, "http") && strings.HasSuffix(host, ":6334") {
		return "", fmt.Errorf("use 6333 for HTTP; 6334 is gRPC")
	}
	u.Host = host
	return u.String(), nil
}

func (s *Sink) ensureCollection(dim int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If we already know the dimension, skip the check
	if s.dim > 0 && s.dim == dim {
		return nil
	}

	infoURL := fmt.Sprintf("%s/collections/%s", s.baseURL, s.collection)
	s.logger.Debug("Checking if collection exists", zap.String("url", infoURL))
	
	resp, err := s.client.Get(infoURL)
	if err == nil && resp.StatusCode == 200 {
		defer resp.Body.Close()
		var doc map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
			s.logger.Error("Failed to decode collection info", zap.Error(err))
			return err
		}
		existing := extractVectorSize(doc)
		s.logger.Debug("Collection exists", 
			zap.String("collection", s.collection),
			zap.Int("existing_dim", existing),
			zap.Int("required_dim", dim))
		
		if existing > 0 && dim > 0 && existing != dim {
			return fmt.Errorf("collection exists with size=%d but payload has dim=%d; drop or recreate the collection", existing, dim)
		}
		if existing > 0 {
			s.dim = existing
		} else {
			s.dim = dim
		}
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

	createBody := map[string]any{
		"vectors": map[string]any{
			"size":     dim,
			"distance": s.distance,
		},
	}
	b, _ := json.Marshal(createBody)
	req, _ := http.NewRequest(http.MethodPut, infoURL, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	cr, err := s.client.Do(req)
	if err != nil {
		s.logger.Error("Failed to create collection", zap.Error(err))
		return err
	}
	defer cr.Body.Close()
	
	msg, _ := io.ReadAll(cr.Body)
	if cr.StatusCode != 200 {
		s.logger.Error("Collection creation failed", 
			zap.Int("status", cr.StatusCode),
			zap.String("response", string(msg)))
		return fmt.Errorf("failed to create collection: %s", string(msg))
	}
	
	s.logger.Info("Collection created successfully", 
		zap.String("collection", s.collection),
		zap.Int("dimension", dim))
	s.dim = dim
	return nil
}

func extractVectorSize(doc map[string]any) int {
	r, ok := doc["result"].(map[string]any)
	if !ok {
		return 0
	}
	cfg, ok := r["config"].(map[string]any)
	if !ok {
		return 0
	}
	params, ok := cfg["params"].(map[string]any)
	if !ok {
		return 0
	}
	vectors, ok := params["vectors"].(map[string]any)
	if !ok {
		return 0
	}
	switch sz := vectors["size"].(type) {
	case float64:
		return int(sz)
	case int:
		return sz
	default:
		return 0
	}
}

func normalizePointID(external string) uint64 {
	// Use hash of the external ID to create a consistent uint64
	h := fnv.New64a()
	h.Write([]byte(external))
	return h.Sum64()
}

func (s *Sink) Upsert(id string, vec []float32, payload map[string]any) error {
	if len(vec) == 0 {
		return fmt.Errorf("empty vector")
	}
	if err := s.ensureCollection(len(vec)); err != nil {
		return err
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["ext_id"] = id
	normalizedID := normalizePointID(id)
	
	// Use the correct Qdrant API format for batch upsert
	body := map[string]any{
		"points": []map[string]any{
			{
				"id":      normalizedID,
				"vector":  vec,
				"payload": payload,
			},
		},
	}
	
	b, err := json.Marshal(body)
	if err != nil {
		s.logger.Error("Failed to marshal JSON", zap.Error(err))
		return err
	}
	
	// Debug: log the request details
	s.logger.Debug("Preparing upsert request", 
		zap.String("id", id),
		zap.Uint64("normalized_id", normalizedID),
		zap.Int("vector_dim", len(vec)),
		zap.Any("payload", payload))
	
	u := fmt.Sprintf("%s/collections/%s/points", s.baseURL, s.collection)
	
	s.logger.Debug("Sending upsert request", 
		zap.String("url", u),
		zap.Int("payload_size", len(b)))
	
	req, err := http.NewRequest(http.MethodPut, u, bytes.NewReader(b))
	if err != nil {
		s.logger.Error("Failed to create request", zap.Error(err))
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := s.client.Do(req)
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
			zap.String("response", string(msg)),
			zap.String("request_body", string(b)))
		return fmt.Errorf("failed to upsert point, status: %d, body: %s", resp.StatusCode, string(msg))
	}
	
	s.logger.Info("Point upserted successfully", 
		zap.String("id", id),
		zap.Uint64("normalized_id", normalizedID),
		zap.Int("vector_dim", len(vec)),
		zap.Int("status", resp.StatusCode))
	return nil
}

func (s *Sink) Delete(id string) error {
	normalizedID := normalizePointID(id)
	u := fmt.Sprintf("%s/collections/%s/points/delete", s.baseURL, s.collection)
	req := map[string]any{
		"points": []uint64{normalizedID},
	}
	b, _ := json.Marshal(req)
	resp, err := s.client.Post(u, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete point, status: %d, body: %s", resp.StatusCode, string(msg))
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

func (s *Sink) Close() error { return nil }
