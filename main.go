package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
)

// ==================== CONFIGURAÇÃO ====================

// Configuration represents application configuration
type Configuration struct {
	RedpandaBrokers         []string      `json:"redpanda_brokers"`
	RedpandaDefaultTopic    string        `json:"redpanda_default_topic"`
	ServerPort              string        `json:"server_port"`
	ServerHost              string        `json:"server_host"`
	Environment             string        `json:"environment"`
	LogLevel                string        `json:"log_level"`
	EnableAutoTopicCreation bool          `json:"enable_auto_topic_creation"`
	RequestTimeout          time.Duration `json:"request_timeout"`
	MaxRetries              int           `json:"max_retries"`
	BatchSize               int           `json:"batch_size"`
	CompressionType         string        `json:"compression_type"`
}

// NewConfiguration creates a new configuration with defaults and environment overrides
func NewConfiguration() *Configuration {
	config := &Configuration{
		RedpandaBrokers:         []string{"redpanda-client.production-messaging.svc.cluster.local:9092"},
		RedpandaDefaultTopic:    "go-api-topic.v1",
		ServerPort:              "8980",
		ServerHost:              "",
		Environment:             "development",
		LogLevel:                "info",
		EnableAutoTopicCreation: true,
		RequestTimeout:          10 * time.Second,
		MaxRetries:              3,
		BatchSize:               100,
		CompressionType:         "snappy", // ou "gzip", "lz4", "zstd", "none"
	}

	// Override with environment variables
	if envBrokers := os.Getenv("REDPANDA_BROKERS"); envBrokers != "" {
		config.RedpandaBrokers = parseBrokers(envBrokers)
	}

	if envTopic := os.Getenv("DEFAULT_TOPIC"); envTopic != "" {
		config.RedpandaDefaultTopic = envTopic
	}

	if envPort := os.Getenv("PORT"); envPort != "" {
		config.ServerPort = envPort
	}

	if envHost := os.Getenv("HOST"); envHost != "" {
		config.ServerHost = envHost
	}

	if envEnv := os.Getenv("ENVIRONMENT"); envEnv != "" {
		config.Environment = envEnv
	}

	if envLogLevel := os.Getenv("LOG_LEVEL"); envLogLevel != "" {
		config.LogLevel = envLogLevel
	}

	if envAutoCreate := os.Getenv("ENABLE_AUTO_TOPIC_CREATION"); envAutoCreate != "" {
		config.EnableAutoTopicCreation = strings.ToLower(envAutoCreate) == "true"
	}

	if envRetries := os.Getenv("MAX_RETRIES"); envRetries != "" {
		if n, err := fmt.Sscanf(envRetries, "%d", &config.MaxRetries); err == nil && n == 1 {
			// parsed successfully
		}
	}

	if envBatchSize := os.Getenv("BATCH_SIZE"); envBatchSize != "" {
		if n, err := fmt.Sscanf(envBatchSize, "%d", &config.BatchSize); err == nil && n == 1 {
			// parsed successfully
		}
	}

	if envCompression := os.Getenv("COMPRESSION_TYPE"); envCompression != "" {
		config.CompressionType = envCompression
	}

	return config
}

// GetServerAddress returns the formatted server address
func (c *Configuration) GetServerAddress() string {
	if c.ServerHost == "" {
		return ":" + c.ServerPort
	}
	return fmt.Sprintf("%s:%s", c.ServerHost, c.ServerPort)
}

// parseBrokers parses comma-separated broker strings into a slice
func parseBrokers(brokerString string) []string {
	brokers := strings.Split(brokerString, ",")
	for i, broker := range brokers {
		brokers[i] = strings.TrimSpace(broker)
	}
	return brokers
}

// ==================== MODELOS ====================

// Event represents a standardized event structure
type Event struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Version   string                 `json:"version"`
	Timestamp string                 `json:"timestamp"`
	Source    string                 `json:"source"`
	Data      map[string]interface{} `json:"data"`
	Metadata  map[string]string      `json:"metadata,omitempty"`
}

// Message represents the API request structure
type Message struct {
	Topic   string            `json:"topic,omitempty"`
	Key     string            `json:"key,omitempty"`
	Value   string            `json:"value"`
	Headers map[string]string `json:"headers,omitempty"`
	Event   *Event            `json:"event,omitempty"` // Formato estruturado opcional
}

// APIResponse represents the API response
type APIResponse struct {
	Success   bool                   `json:"success"`
	Message   string                 `json:"message"`
	EventID   string                 `json:"event_id,omitempty"`
	Topic     string                 `json:"topic,omitempty"`
	Partition int32                  `json:"partition,omitempty"`
	Offset    int64                  `json:"offset,omitempty"`
	Timestamp string                 `json:"timestamp,omitempty"`
	RequestID string                 `json:"request_id,omitempty"`
	LatencyMS int64                  `json:"latency_ms,omitempty"`
	Details   map[string]interface{} `json:"details,omitempty"`
}

// HealthCheckResponse represents health check response
type HealthCheckResponse struct {
	Status      string            `json:"status"`
	Service     string            `json:"service"`
	Environment string            `json:"environment"`
	Version     string            `json:"version"`
	Timestamp   string            `json:"timestamp"`
	Uptime      string            `json:"uptime"`
	Components  map[string]string `json:"components,omitempty"`
	Metrics     map[string]int64  `json:"metrics,omitempty"`
}

// Metrics holds application metrics
type Metrics struct {
	sync.RWMutex
	MessagesSent        int64            `json:"messages_sent"`
	MessagesFailed      int64            `json:"messages_failed"`
	RequestsProcessed   int64            `json:"requests_processed"`
	AvgProcessingTimeMS int64            `json:"avg_processing_time_ms"`
	StartTime           time.Time        `json:"start_time"`
	TopicMetrics        map[string]int64 `json:"topic_metrics"`
}

// ==================== KAFKA PRODUCER ====================

// KafkaProducer manages the Kafka client connection
type KafkaProducer struct {
	client  *kgo.Client
	config  *Configuration
	metrics *Metrics
}

// NewKafkaProducer creates a new Kafka producer instance
func NewKafkaProducer(config *Configuration, metrics *Metrics) (*KafkaProducer, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(config.RedpandaBrokers...),
		kgo.MaxBufferedRecords(config.BatchSize),
		kgo.RequiredAcks(kgo.AllISRAcks()), // Garantia forte de entrega
		kgo.RetryTimeout(30 * time.Second),
		kgo.ProduceRequestTimeout(15 * time.Second),
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)), // Particionador eficiente
		kgo.AllowAutoTopicCreation(),
	}

	// Configurar compressão
	switch strings.ToLower(config.CompressionType) {
	case "gzip":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.GzipCompression()))
	case "snappy":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.SnappyCompression()))
	case "lz4":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.Lz4Compression()))
	case "zstd":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.ZstdCompression()))
	default:
		opts = append(opts, kgo.ProducerBatchCompression(kgo.NoCompression()))
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kafka client: %w", err)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to connect to Redpanda brokers: %w", err)
	}

	return &KafkaProducer{
		client:  client,
		config:  config,
		metrics: metrics,
	}, nil
}

// SendMessage sends a message to Redpanda with retry logic
func (p *KafkaProducer) SendMessage(ctx context.Context, msg Message) (partition int32, offset int64, err error) {
	startTime := time.Now()
	topic := p.getTopic(msg)

	// Criar record com headers padronizados
	record := p.createRecord(topic, msg)

	// Tentar enviar com retry
	for attempt := 1; attempt <= p.config.MaxRetries; attempt++ {
		res := p.client.ProduceSync(ctx, record)

		if err = res.FirstErr(); err == nil {
			// Sucesso - CORREÇÃO: First() retorna dois valores
			result, firstErr := res.First()
			if firstErr != nil {
				err = firstErr
				log.Printf("Send attempt %d failed (First error): %v", attempt, firstErr)
				continue
			}

			if result == nil {
				err = fmt.Errorf("no result returned from producer")
				log.Printf("Send attempt %d failed: no result", attempt)
				continue
			}

			duration := time.Since(startTime).Milliseconds()

			p.metrics.Lock()
			p.metrics.MessagesSent++
			p.metrics.TopicMetrics[topic]++

			// Corrigir cálculo da média
			if p.metrics.MessagesSent > 0 {
				p.metrics.AvgProcessingTimeMS = (p.metrics.AvgProcessingTimeMS*(p.metrics.MessagesSent-1) + duration) / p.metrics.MessagesSent
			} else {
				p.metrics.AvgProcessingTimeMS = duration
			}
			p.metrics.Unlock()

			return result.Partition, result.Offset, nil
		}

		// Log do erro
		log.Printf("Send attempt %d failed: %v", attempt, err)

		// Backoff exponencial
		if attempt < p.config.MaxRetries {
			backoff := time.Duration(attempt*100) * time.Millisecond
			time.Sleep(backoff)
		}
	}

	// Todas as tentativas falharam
	p.metrics.Lock()
	p.metrics.MessagesFailed++
	p.metrics.Unlock()

	return -1, -1, fmt.Errorf("failed to send message after %d attempts: %w", p.config.MaxRetries, err)
}

// getTopic returns the topic to use
func (p *KafkaProducer) getTopic(msg Message) string {
	if msg.Topic != "" {
		return msg.Topic
	}
	return p.config.RedpandaDefaultTopic
}

// createRecord creates a standardized Kafka record
func (p *KafkaProducer) createRecord(topic string, msg Message) *kgo.Record {
	eventID := uuid.New().String()
	timestamp := time.Now().UTC()

	// Se o usuário forneceu um Event estruturado, use-o
	var value []byte
	if msg.Event != nil {
		// Garantir que o Event tenha ID e timestamp
		if msg.Event.ID == "" {
			msg.Event.ID = eventID
		}
		if msg.Event.Timestamp == "" {
			msg.Event.Timestamp = timestamp.Format(time.RFC3339)
		}
		if msg.Event.Version == "" {
			msg.Event.Version = "1.0"
		}
		if msg.Event.Source == "" {
			msg.Event.Source = "go-api-producer"
		}

		value, _ = json.Marshal(msg.Event)
	} else {
		// Usar value simples
		value = []byte(msg.Value)
	}

	record := &kgo.Record{
		Topic:     topic,
		Value:     value,
		Timestamp: timestamp,
	}

	// Key para particionamento consistente (se fornecida)
	if msg.Key != "" {
		record.Key = []byte(msg.Key)
	}

	// Headers padronizados
	record.Headers = []kgo.RecordHeader{
		{Key: "event_id", Value: []byte(eventID)},
		{Key: "timestamp", Value: []byte(timestamp.Format(time.RFC3339))},
		{Key: "source", Value: []byte("go-api-producer")},
		{Key: "environment", Value: []byte(p.config.Environment)},
		{Key: "content_type", Value: []byte("applications/json")},
	}

	// Adicionar headers do usuário
	for k, v := range msg.Headers {
		record.Headers = append(record.Headers, kgo.RecordHeader{
			Key:   k,
			Value: []byte(v),
		})
	}

	return record
}

// Close closes the connection to Redpanda
func (p *KafkaProducer) Close() {
	if p.client != nil {
		p.client.Flush(context.Background()) // Garantir envio de mensagens pendentes
		p.client.Close()
		log.Println("Kafka client connection closed gracefully")
	}
}

// ==================== HTTP HANDLER ====================

// HTTPHandler handles HTTP requests
type HTTPHandler struct {
	producer *KafkaProducer
	config   *Configuration
	metrics  *Metrics
}

// NewHTTPHandler creates a new HTTP handler
func NewHTTPHandler(producer *KafkaProducer, config *Configuration, metrics *Metrics) *HTTPHandler {
	return &HTTPHandler{
		producer: producer,
		config:   config,
		metrics:  metrics,
	}
}

// SendMessageHandler handles message sending requests
func (h *HTTPHandler) SendMessageHandler(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	requestID := r.Context().Value("request_id").(string)

	h.metrics.Lock()
	h.metrics.RequestsProcessed++
	h.metrics.Unlock()

	if r.Method != http.MethodPost {
		h.sendError(w, http.StatusMethodNotAllowed, "Method not allowed", requestID, startTime)
		return
	}

	// Decode JSON
	var msg Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		h.sendError(w, http.StatusBadRequest, fmt.Sprintf("Failed to decode JSON: %v", err), requestID, startTime)
		return
	}

	// Validate message
	if msg.Value == "" && msg.Event == nil {
		h.sendError(w, http.StatusBadRequest, "Field 'value' is required or provide 'event' object", requestID, startTime)
		return
	}

	// Send message
	ctx, cancel := context.WithTimeout(r.Context(), h.config.RequestTimeout)
	defer cancel()

	partition, offset, err := h.producer.SendMessage(ctx, msg)
	if err != nil {
		log.Printf("[%s] Error sending message: %v", requestID, err)
		h.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to send message: %v", err), requestID, startTime)
		return
	}

	// Send success response
	topic := h.producer.getTopic(msg)
	latency := time.Since(startTime).Milliseconds()

	response := APIResponse{
		Success:   true,
		Message:   "Message sent successfully",
		EventID:   msg.Event.ID,
		Topic:     topic,
		Partition: partition,
		Offset:    offset,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		RequestID: requestID,
		LatencyMS: latency,
		Details: map[string]interface{}{
			"brokers":     h.config.RedpandaBrokers,
			"batch_size":  h.config.BatchSize,
			"retries":     h.config.MaxRetries,
			"compression": h.config.CompressionType,
		},
	}

	h.sendJSON(w, http.StatusOK, response)
}

// HealthCheckHandler handles health check requests
func (h *HTTPHandler) HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(h.metrics.StartTime).Round(time.Second).String()

	h.metrics.RLock()
	metrics := map[string]int64{
		"messages_sent":          h.metrics.MessagesSent,
		"messages_failed":        h.metrics.MessagesFailed,
		"requests_processed":     h.metrics.RequestsProcessed,
		"avg_processing_time_ms": h.metrics.AvgProcessingTimeMS,
	}
	h.metrics.RUnlock()

	components := map[string]string{
		"redpanda": "connected",
		"api":      "operational",
		"producer": "healthy",
	}

	response := HealthCheckResponse{
		Status:      "healthy",
		Service:     "redpanda-producer-api",
		Environment: h.config.Environment,
		Version:     "1.0.0",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Uptime:      uptime,
		Components:  components,
		Metrics:     metrics,
	}

	h.sendJSON(w, http.StatusOK, response)
}

// MetricsHandler returns application metrics
func (h *HTTPHandler) MetricsHandler(w http.ResponseWriter, r *http.Request) {
	h.metrics.RLock()
	defer h.metrics.RUnlock()

	metrics := map[string]interface{}{
		"messages_sent":          h.metrics.MessagesSent,
		"messages_failed":        h.metrics.MessagesFailed,
		"requests_processed":     h.metrics.RequestsProcessed,
		"avg_processing_time_ms": h.metrics.AvgProcessingTimeMS,
		"uptime_seconds":         time.Since(h.metrics.StartTime).Seconds(),
		"topic_metrics":          h.metrics.TopicMetrics,
	}

	h.sendJSON(w, http.StatusOK, metrics)
}

// TopicsHandler lists available topics
func (h *HTTPHandler) TopicsHandler(w http.ResponseWriter, r *http.Request) {
	// Em produção, você obteria isso do Kafka
	topics := []string{
		h.config.RedpandaDefaultTopic,
		"system.metrics",
		"system.logs",
	}

	response := map[string]interface{}{
		"topics":              topics,
		"default_topic":       h.config.RedpandaDefaultTopic,
		"auto_topic_creation": h.config.EnableAutoTopicCreation,
	}

	h.sendJSON(w, http.StatusOK, response)
}

// sendJSON sends a JSON response
func (h *HTTPHandler) sendJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "applications/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// sendError sends an error response
func (h *HTTPHandler) sendError(w http.ResponseWriter, status int, message, requestID string, startTime time.Time) {
	latency := time.Since(startTime).Milliseconds()

	response := APIResponse{
		Success:   false,
		Message:   message,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		RequestID: requestID,
		LatencyMS: latency,
	}

	h.sendJSON(w, status, response)
}

// ==================== MIDDLEWARE ====================

// RequestLogger middleware for logging HTTP requests
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := uuid.New().String()

		// Add request ID to context
		ctx := context.WithValue(r.Context(), "request_id", requestID)
		r = r.WithContext(ctx)

		// Log the request
		log.Printf("[%s] %s %s %s", requestID, r.Method, r.URL.Path, r.RemoteAddr)

		// Create custom response writer to capture status code
		rw := &responseWriter{w, http.StatusOK}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		log.Printf("[%s] Completed in %v with status %d", requestID, duration, rw.statusCode)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// ==================== HTTP SERVER ====================

// HTTPServer manages the HTTP server
type HTTPServer struct {
	server  *http.Server
	handler *HTTPHandler
	config  *Configuration
}

// NewHTTPServer creates a new HTTP server
func NewHTTPServer(handler *HTTPHandler, config *Configuration) *HTTPServer {
	mux := http.NewServeMux()

	// Register routes
	mux.HandleFunc("/", handler.RootHandler)
	mux.HandleFunc("/health", handler.HealthCheckHandler)
	mux.HandleFunc("/metrics", handler.MetricsHandler)
	mux.HandleFunc("/topics", handler.TopicsHandler)
	mux.HandleFunc("/send", handler.SendMessageHandler)

	// Apply middleware
	handlerChain := RequestLogger(mux)

	server := &http.Server{
		Addr:         config.GetServerAddress(),
		Handler:      handlerChain,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &HTTPServer{
		server:  server,
		handler: handler,
		config:  config,
	}
}

// Start starts the HTTP server
func (s *HTTPServer) Start() error {
	log.Printf("🚀 Starting HTTP server on %s", s.config.GetServerAddress())
	log.Printf("📊 Environment: %s", s.config.Environment)
	log.Printf("🔗 Redpanda brokers: %v", s.config.RedpandaBrokers)
	log.Printf("📨 Default topic: %s", s.config.RedpandaDefaultTopic)
	log.Printf("⚙️  Configuration: retries=%d, batch=%d, compression=%s",
		s.config.MaxRetries, s.config.BatchSize, s.config.CompressionType)

	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start server: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the server
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// ==================== MAIN ====================

func main() {
	// Initialize configuration
	config := NewConfiguration()

	// Initialize metrics
	metrics := &Metrics{
		StartTime:    time.Now(),
		TopicMetrics: make(map[string]int64),
	}

	// Initialize Kafka producer
	producer, err := NewKafkaProducer(config, metrics)
	if err != nil {
		log.Fatalf("❌ Failed to initialize Kafka producer: %v", err)
	}
	defer producer.Close()

	// Initialize HTTP handler
	handler := NewHTTPHandler(producer, config, metrics)

	// Initialize HTTP server
	httpServer := NewHTTPServer(handler, config)

	// Graceful shutdown
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	// Start server in goroutine
	go func() {
		if err := httpServer.Start(); err != nil {
			log.Fatalf("❌ HTTP server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-shutdownChan
	log.Println("🛑 Received shutdown signal, initiating graceful shutdown...")

	// Create shutdown context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Shutdown HTTP server
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("⚠️ Error during server shutdown: %v", err)
	}

	log.Println("✅ Server shutdown completed")
}

// RootHandler handles the root endpoint
func (h *HTTPHandler) RootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	html := `
<!DOCTYPE html>
<html>
<head>
	<title>Redpanda Producer API</title>
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<style>
		/* Estilos mantidos do seu código original */
		body {
			font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif;
			line-height: 1.6;
			margin: 0;
			padding: 20px;
			color: #333;
			max-width: 1200px;
			margin: 0 auto;
		}
		.header {
			background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
			color: white;
			padding: 2rem;
			border-radius: 10px;
			margin-bottom: 2rem;
		}
		.code-block {
			background: #f8f9fa;
			border: 1px solid #e9ecef;
			border-radius: 6px;
			padding: 1rem;
			margin: 1rem 0;
			font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
			font-size: 14px;
			overflow-x: auto;
		}
		.endpoint {
			background: white;
			border: 1px solid #dee2e6;
			border-radius: 8px;
			padding: 1.5rem;
			margin: 1rem 0;
			box-shadow: 0 2px 4px rgba(0,0,0,0.1);
		}
		.method {
			display: inline-block;
			padding: 0.25rem 0.75rem;
			border-radius: 4px;
			font-weight: bold;
			margin-right: 0.5rem;
		}
		.get { background: #28a745; color: white; }
		.post { background: #007bff; color: white; }
		.badge {
			display: inline-block;
			padding: 0.25rem 0.5rem;
			font-size: 0.875rem;
			border-radius: 4px;
			margin-left: 0.5rem;
		}
		.env-dev { background: #17a2b8; color: white; }
		.env-prod { background: #dc3545; color: white; }
		.stats {
			display: grid;
			grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
			gap: 1rem;
			margin: 1rem 0;
		}
		.stat-card {
			background: white;
			border: 1px solid #dee2e6;
			border-radius: 8px;
			padding: 1rem;
			text-align: center;
		}
		.stat-value {
			font-size: 2rem;
			font-weight: bold;
			color: #667eea;
		}
		.stat-label {
			font-size: 0.875rem;
			color: #6c757d;
			margin-top: 0.5rem;
		}
	</style>
</head>
<body>
	<div class="header">
		<h1>Redpanda Producer API</h1>
		<p>High-performance message producer API for Redpanda/Kafka</p>
		<span class="badge env-` + h.config.Environment + `">Environment: ` + h.config.Environment + `</span>
	</div>
	
	<div class="stats">
		<div class="stat-card">
			<div class="stat-value" id="messagesSent">0</div>
			<div class="stat-label">Messages Sent</div>
		</div>
		<div class="stat-card">
			<div class="stat-value" id="avgLatency">0</div>
			<div class="stat-label">Avg Latency (ms)</div>
		</div>
		<div class="stat-card">
			<div class="stat-value" id="uptime">0</div>
			<div class="stat-label">Uptime</div>
		</div>
	</div>
	
	<div class="endpoint">
		<span class="method get">GET</span>
		<strong>/health</strong> - Health check endpoint
		<p>Returns service health status and component information.</p>
	</div>
	
	<div class="endpoint">
		<span class="method get">GET</span>
		<strong>/metrics</strong> - Application metrics
		<p>Returns real-time metrics about message processing.</p>
	</div>
	
	<div class="endpoint">
		<span class="method get">GET</span>
		<strong>/topics</strong> - Available topics
		<p>Lists available topics and configuration.</p>
	</div>
	
	<div class="endpoint">
		<span class="method post">POST</span>
		<strong>/send</strong> - Send message to Redpanda
		<p>Send messages with structured events or simple values.</p>
		
		<h4>Simple Request Example:</h4>
		<div class="code-block">
curl -X POST http://` + h.config.GetServerAddress() + `/send \
  -H "Content-Type: applications/json" \
  -d '{
    "topic": "user.events.v1",
    "key": "user-123",
    "value": "User logged in successfully",
    "headers": {
      "app": "authentication-service",
      "env": "` + h.config.Environment + `"
    }
  }'
		</div>
		
		<h4>Structured Event Example:</h4>
		<div class="code-block">
curl -X POST http://` + h.config.GetServerAddress() + `/send \
  -H "Content-Type: applications/json" \
  -d '{
    "topic": "user.events.v1",
    "key": "user-123",
    "event": {
      "type": "user.created",
      "version": "1.0",
      "source": "auth-service",
      "data": {
        "user_id": "123",
        "email": "user@example.com",
        "name": "John Doe"
      },
      "metadata": {
        "correlation_id": "corr-789",
        "tenant_id": "tenant-456"
      }
    }
  }'
		</div>
		
		<h4>Response Example:</h4>
		<div class="code-block">
{
  "success": true,
  "message": "Message sent successfully",
  "event_id": "123e4567-e89b-12d3-a456-426614174000",
  "topic": "user.events.v1",
  "partition": 0,
  "offset": 12345,
  "timestamp": "2024-01-19T15:30:45Z",
  "request_id": "1705671045123456789",
  "latency_ms": 25,
  "details": {
    "brokers": ["localhost:9092"],
    "batch_size": 100,
    "retries": 3,
    "compression": "snappy"
  }
}
		</div>
	</div>
	
	<h3>📊 Current Configuration</h3>
	<ul>
		<li><strong>Brokers:</strong> ` + fmt.Sprintf("%v", h.config.RedpandaBrokers) + `</li>
		<li><strong>Default Topic:</strong> ` + h.config.RedpandaDefaultTopic + `</li>
		<li><strong>Server:</strong> http://` + h.config.GetServerAddress() + `</li>
		<li><strong>Request Timeout:</strong> ` + h.config.RequestTimeout.String() + `</li>
		<li><strong>Max Retries:</strong> ` + fmt.Sprintf("%d", h.config.MaxRetries) + `</li>
		<li><strong>Batch Size:</strong> ` + fmt.Sprintf("%d", h.config.BatchSize) + `</li>
		<li><strong>Compression:</strong> ` + h.config.CompressionType + `</li>
	</ul>
	
	<script>
		// Auto-refresh metrics
		function updateMetrics() {
			fetch('/metrics')
				.then(response => response.json())
				.then(data => {
					document.getElementById('messagesSent').textContent = 
						data.messages_sent || 0;
					document.getElementById('avgLatency').textContent = 
						data.avg_processing_time_ms || 0;
				})
				.catch(console.error);
			
			fetch('/health')
				.then(response => response.json())
				.then(data => {
					document.getElementById('uptime').textContent = 
						data.uptime.split('.')[0];
				})
				.catch(console.error);
		}
		
		// Update every 5 seconds
		updateMetrics();
		setInterval(updateMetrics, 5000);
	</script>
</body>
</html>
	`
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}
