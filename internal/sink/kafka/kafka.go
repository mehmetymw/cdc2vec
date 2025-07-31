package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

type Sink struct {
	writer *kafka.Writer
	topic  string
	logger *zap.Logger
}

type KafkaMessage struct {
	ID       string         `json:"id"`
	Vector   []float32      `json:"vector"`
	Metadata map[string]any `json:"metadata"`
	Op       string         `json:"op"`
	Table    string         `json:"table"`
	PK       string         `json:"pk"`
}

func New(brokers []string, topic string, logger *zap.Logger) (*Sink, error) {
	logger.Info("Creating Kafka sink", 
		zap.Strings("brokers", brokers),
		zap.String("topic", topic))

	writer := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Topic:        topic,
		Balancer:     &kafka.LeastBytes{},
		BatchTimeout: 10 * time.Millisecond,
		BatchSize:    100,
		Async:        false, // Synchronous for reliability
		Logger:       kafka.LoggerFunc(func(msg string, args ...interface{}) {
			logger.Debug("Kafka writer log", zap.String("msg", fmt.Sprintf(msg, args...)))
		}),
		ErrorLogger: kafka.LoggerFunc(func(msg string, args ...interface{}) {
			logger.Error("Kafka writer error", zap.String("msg", fmt.Sprintf(msg, args...)))
		}),
	}

	sink := &Sink{
		writer: writer,
		topic:  topic,
		logger: logger,
	}

	logger.Info("Kafka sink created successfully")
	return sink, nil
}

func (s *Sink) Upsert(id string, vector []float32, metadata map[string]any) error {
	s.logger.Debug("Publishing upsert to Kafka", 
		zap.String("id", id),
		zap.Int("vector_dim", len(vector)))

	msg := KafkaMessage{
		ID:       id,
		Vector:   vector,
		Metadata: metadata,
		Op:       "upsert",
		Table:    fmt.Sprintf("%v", metadata["table"]),
		PK:       fmt.Sprintf("%v", metadata["pk"]),
	}

	return s.publishMessage(id, msg)
}

func (s *Sink) Delete(id string) error {
	s.logger.Debug("Publishing delete to Kafka", zap.String("id", id))

	msg := KafkaMessage{
		ID: id,
		Op: "delete",
	}

	return s.publishMessage(id, msg)
}

func (s *Sink) publishMessage(key string, msg KafkaMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		s.logger.Error("Failed to marshal Kafka message", zap.Error(err))
		return err
	}

	kafkaMsg := kafka.Message{
		Key:   []byte(key),
		Value: data,
		Time:  time.Now(),
	}

	s.logger.Debug("Sending message to Kafka", 
		zap.String("key", key),
		zap.String("topic", s.topic),
		zap.Int("message_size", len(data)))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	err = s.writer.WriteMessages(ctx, kafkaMsg)
	duration := time.Since(start)

	if err != nil {
		s.logger.Error("Failed to write message to Kafka", 
			zap.Error(err),
			zap.String("key", key),
			zap.Duration("duration", duration))
		return err
	}

	s.logger.Debug("Message sent to Kafka successfully", 
		zap.String("key", key),
		zap.Duration("duration", duration))

	return nil
}

func (s *Sink) Close() error {
	s.logger.Info("Closing Kafka sink")
	if s.writer != nil {
		return s.writer.Close()
	}
	return nil
}