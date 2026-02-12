/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/gorilla/websocket"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/clock"

	streamingv1alpha1 "github.com/ardikabs/hibernator/api/streaming/v1alpha1"
	"github.com/ardikabs/hibernator/internal/streaming/auth"
)

const (
	// DefaultWebSocketPingInterval is the interval between server pings.
	DefaultWebSocketPingInterval = 30 * time.Second

	// DefaultWebSocketWriteTimeout is the timeout for writing to WebSocket.
	DefaultWebSocketWriteTimeout = 10 * time.Second

	// DefaultWebSocketReadTimeout is the timeout for reading from WebSocket.
	DefaultWebSocketReadTimeout = 60 * time.Second

	// DefaultWebSocketMaxMessageSize is the max message size in bytes.
	DefaultWebSocketMaxMessageSize = 10 * 1024 * 1024 // 10 MB
)

// WebSocketMessage wraps messages sent over WebSocket.
type WebSocketMessage struct {
	Type string          `json:"type"` // "log", "progress", "completion", "heartbeat"
	Data json.RawMessage `json:"data"`
}

// WebSocketServer provides WebSocket streaming endpoints for runner communication.
type WebSocketServer struct {
	clock          clock.Clock
	addr           string
	execService    *ExecutionServiceServer
	validator      *auth.TokenValidator
	k8sClientset   kubernetes.Interface
	log            logr.Logger
	upgrader       websocket.Upgrader
	connections    map[string]*websocket.Conn
	connectionsMu  sync.RWMutex
	pingInterval   time.Duration
	writeTimeout   time.Duration
	readTimeout    time.Duration
	maxMessageSize int64
}

// WebSocketServerOptions configures the WebSocket server.
type WebSocketServerOptions struct {
	Addr           string
	Clock          clock.Clock
	ExecService    *ExecutionServiceServer
	Validator      *auth.TokenValidator
	K8sClientset   kubernetes.Interface
	Log            logr.Logger
	PingInterval   time.Duration
	WriteTimeout   time.Duration
	ReadTimeout    time.Duration
	MaxMessageSize int64
}

// NewWebSocketServer creates a new WebSocket streaming server.
func NewWebSocketServer(opts WebSocketServerOptions) *WebSocketServer {
	if opts.PingInterval == 0 {
		opts.PingInterval = DefaultWebSocketPingInterval
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = DefaultWebSocketWriteTimeout
	}
	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = DefaultWebSocketReadTimeout
	}
	if opts.MaxMessageSize == 0 {
		opts.MaxMessageSize = DefaultWebSocketMaxMessageSize
	}

	srv := &WebSocketServer{
		addr:         opts.Addr,
		clock:        clock.RealClock{},
		execService:  opts.ExecService,
		validator:    opts.Validator,
		k8sClientset: opts.K8sClientset,
		log:          opts.Log.WithName("websocket-server"),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins (adjust for production)
			},
		},
		connections:    make(map[string]*websocket.Conn),
		pingInterval:   opts.PingInterval,
		writeTimeout:   opts.WriteTimeout,
		readTimeout:    opts.ReadTimeout,
		maxMessageSize: opts.MaxMessageSize,
	}

	if opts.Clock != nil {
		srv.clock = opts.Clock
	}

	return srv
}

// Start starts the WebSocket server.
func (s *WebSocketServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1alpha1/stream/", s.handleWebSocket)

	server := &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	s.log.Info("starting WebSocket server", "addr", s.addr)

	// Start server in goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.log.Error(err, "WebSocket server error")
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()
	s.log.Info("shutting down WebSocket server")

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return server.Shutdown(shutdownCtx)
}

// NeedLeaderElection indicates whether the websocket server requires leader election.
func (s *WebSocketServer) NeedLeaderElection() bool {
	return false
}

// handleWebSocket handles WebSocket upgrade and streaming.
func (s *WebSocketServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Extract execution ID from URL path
	// Expected format: /v1alpha1/stream/{executionId}
	executionID := r.URL.Path[len("/v1alpha1/stream/"):]
	if executionID == "" {
		http.Error(w, "missing execution ID", http.StatusBadRequest)
		return
	}

	// Authenticate request
	token := r.Header.Get("Authorization")
	if token == "" {
		http.Error(w, "missing Authorization header", http.StatusUnauthorized)
		return
	}

	// Remove "Bearer " prefix
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}

	// Validate token
	if err := s.validateToken(r.Context(), token, executionID); err != nil {
		s.log.Error(err, "token validation failed", "executionId", executionID)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Upgrade to WebSocket
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Error(err, "failed to upgrade to WebSocket", "executionId", executionID)
		return
	}

	s.log.Info("WebSocket connection established", "executionId", executionID)

	// Store connection
	s.connectionsMu.Lock()
	s.connections[executionID] = conn
	s.connectionsMu.Unlock()

	// Remove connection on close
	defer func() {
		s.connectionsMu.Lock()
		delete(s.connections, executionID)
		s.connectionsMu.Unlock()
		if err := conn.Close(); err != nil {
			s.log.Error(err, "failed to close WebSocket connection", "executionId", executionID)
		}
		s.log.Info("WebSocket connection closed", "executionId", executionID)
	}()

	// Configure connection
	conn.SetReadLimit(s.maxMessageSize)
	if err := conn.SetReadDeadline(s.clock.Now().Add(s.readTimeout)); err != nil {
		s.log.Error(err, "failed to set read deadline", "executionId", executionID)
		return
	}
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(s.clock.Now().Add(s.readTimeout))
	})

	// Start ping ticker
	pingTicker := time.NewTicker(s.pingInterval)
	defer pingTicker.Stop()

	// Handle messages
	done := make(chan struct{})
	go s.readMessages(conn, executionID, done)

	// Wait for completion or context cancellation
	for {
		select {
		case <-done:
			return
		case <-pingTicker.C:
			if err := s.sendPing(conn); err != nil {
				s.log.Error(err, "failed to send ping", "executionId", executionID)
				return
			}
		}
	}
}

// readMessages reads and processes WebSocket messages.
func (s *WebSocketServer) readMessages(conn *websocket.Conn, executionID string, done chan struct{}) {
	defer close(done)

	for {
		// Read message
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				s.log.Error(err, "WebSocket read error", "executionId", executionID)
			}
			return
		}

		if messageType != websocket.TextMessage {
			s.log.Info("ignoring non-text message", "executionId", executionID, "type", messageType)
			continue
		}

		// Parse message
		var msg WebSocketMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			s.log.Error(err, "failed to unmarshal message", "executionId", executionID)
			continue
		}

		// Process message based on type
		if err := s.processMessage(executionID, &msg); err != nil {
			s.log.Error(err, "failed to process message", "executionId", executionID, "type", msg.Type)
		}
	}
}

// processMessage processes a WebSocket message.
func (s *WebSocketServer) processMessage(executionID string, msg *WebSocketMessage) error {
	ctx := context.Background()

	switch msg.Type {
	case "log":
		var logEntry streamingv1alpha1.LogEntry
		if err := json.Unmarshal(msg.Data, &logEntry); err != nil {
			return fmt.Errorf("failed to unmarshal log entry: %w", err)
		}
		return s.handleLog(ctx, &logEntry)

	case "progress":
		var progress streamingv1alpha1.ProgressReport
		if err := json.Unmarshal(msg.Data, &progress); err != nil {
			return fmt.Errorf("failed to unmarshal progress: %w", err)
		}
		_, err := s.execService.ReportProgress(ctx, &progress)
		return err

	case "completion":
		var completion streamingv1alpha1.CompletionReport
		if err := json.Unmarshal(msg.Data, &completion); err != nil {
			return fmt.Errorf("failed to unmarshal completion: %w", err)
		}
		_, err := s.execService.ReportCompletion(ctx, &completion)
		return err

	case "heartbeat":
		var heartbeat streamingv1alpha1.HeartbeatRequest
		if err := json.Unmarshal(msg.Data, &heartbeat); err != nil {
			return fmt.Errorf("failed to unmarshal heartbeat: %w", err)
		}
		_, err := s.execService.Heartbeat(ctx, &heartbeat)
		return err

	default:
		return fmt.Errorf("unknown message type: %s", msg.Type)
	}
}

// handleLog processes a log entry.
func (s *WebSocketServer) handleLog(ctx context.Context, entry *streamingv1alpha1.LogEntry) error {
	// Delegate to business logic layer (EmitLog pipes logs with full context)
	if err := s.execService.EmitLog(ctx, entry); err != nil {
		s.log.Error(err, "failed to process log entry")
		return err
	}

	return nil
}

// sendPing sends a ping to keep the connection alive.
func (s *WebSocketServer) sendPing(conn *websocket.Conn) error {
	if err := conn.SetWriteDeadline(s.clock.Now().Add(s.writeTimeout)); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.PingMessage, nil)
}

// validateToken validates the authentication token.
func (s *WebSocketServer) validateToken(ctx context.Context, token, executionID string) error {
	// Create TokenReview request
	review := &authenticationv1.TokenReview{
		Spec: authenticationv1.TokenReviewSpec{
			Token: token,
			Audiences: []string{
				"hibernator-control-plane",
			},
		},
	}

	// Submit token review
	result, err := s.k8sClientset.AuthenticationV1().TokenReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create TokenReview: %w", err)
	}

	// Check if authenticated
	if !result.Status.Authenticated {
		return fmt.Errorf("token not authenticated")
	}

	// Verify audience
	if !contains(result.Status.Audiences, "hibernator-control-plane") {
		return fmt.Errorf("invalid token audience")
	}

	s.log.V(1).Info("token validated",
		"executionId", executionID,
		"user", result.Status.User.Username,
	)

	return nil
}

// contains checks if a slice contains a string.
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
