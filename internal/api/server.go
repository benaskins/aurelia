package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/benaskins/aurelia/internal/daemon"
	"github.com/benaskins/aurelia/internal/gpu"
)

// Server serves the aurelia REST API over a Unix socket.
type Server struct {
	daemon    *daemon.Daemon
	gpu       *gpu.Observer
	listener  net.Listener
	server    *http.Server
	tcpServer *http.Server // separate server for TCP with auth middleware
	logger    *slog.Logger
	token     string // bearer token for TCP auth (empty = no auth)
}

// NewServer creates an API server backed by the given daemon.
// The GPU observer is optional — if nil, /v1/gpu returns empty.
func NewServer(d *daemon.Daemon, gpuObs *gpu.Observer) *Server {
	s := &Server{
		daemon: d,
		gpu:    gpuObs,
		logger: slog.With("component", "api"),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/services", s.listServices)
	mux.HandleFunc("GET /v1/services/{name}", s.getService)
	mux.HandleFunc("POST /v1/services/{name}/start", s.startService)
	mux.HandleFunc("POST /v1/services/{name}/stop", s.stopService)
	mux.HandleFunc("POST /v1/services/{name}/restart", s.restartService)
	mux.HandleFunc("POST /v1/reload", s.reload)
	mux.HandleFunc("GET /v1/gpu", s.gpuInfo)
	mux.HandleFunc("GET /v1/health", s.health)

	s.server = &http.Server{
		Handler:           mux,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1MB
	}
	return s
}

// GenerateToken creates a random bearer token and writes it to tokenPath.
// The token is required for TCP API connections.
func (s *Server) GenerateToken(tokenPath string) error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Errorf("generating token: %w", err)
	}
	s.token = hex.EncodeToString(b)
	if err := os.WriteFile(tokenPath, []byte(s.token), 0600); err != nil {
		return fmt.Errorf("writing token file: %w", err)
	}
	s.logger.Info("API token written", "path", tokenPath)
	return nil
}

// ListenUnix starts the server on a Unix socket.
func (s *Server) ListenUnix(path string) error {
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0600); err != nil {
		ln.Close()
		return fmt.Errorf("setting socket permissions: %w", err)
	}
	s.listener = ln
	s.logger.Info("API listening", "socket", path)
	return s.server.Serve(ln)
}

// ListenTCP starts the server on a TCP address with bearer token authentication.
// GenerateToken must be called before ListenTCP.
func (s *Server) ListenTCP(addr string) error {
	if s.token == "" {
		return fmt.Errorf("TCP API requires authentication; call GenerateToken first")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.logger.Info("API listening", "addr", addr)

	// Wrap with auth middleware for TCP connections
	s.tcpServer = &http.Server{
		Handler:           s.requireToken(s.server.Handler),
		ReadTimeout:       s.server.ReadTimeout,
		WriteTimeout:      s.server.WriteTimeout,
		ReadHeaderTimeout: s.server.ReadHeaderTimeout,
		IdleTimeout:       s.server.IdleTimeout,
		MaxHeaderBytes:    s.server.MaxHeaderBytes,
	}
	return s.tcpServer.Serve(ln)
}

// requireToken returns middleware that validates the Authorization header.
func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		provided := strings.TrimPrefix(auth, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Shutdown gracefully shuts down both the Unix and TCP API servers.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.server.Shutdown(ctx)
	if s.tcpServer != nil {
		if tcpErr := s.tcpServer.Shutdown(ctx); tcpErr != nil && err == nil {
			err = tcpErr
		}
	}
	return err
}

func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	states := s.daemon.ServiceStates()
	writeJSON(w, http.StatusOK, states)
}

func (s *Server) getService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	state, err := s.daemon.ServiceState(name)
	if err != nil {
		s.logger.Warn("getService: service not found", "service", name, "error", err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "service not found"})
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) startService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.daemon.StartService(r.Context(), name); err != nil {
		s.logger.Error("startService: failed to start service", "service", name, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to start service"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "starting"})
}

func (s *Server) stopService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.daemon.StopService(name, daemon.DefaultStopTimeout); err != nil {
		s.logger.Error("stopService: failed to stop service", "service", name, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to stop service"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
}

func (s *Server) restartService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.daemon.RestartService(r.Context(), name, daemon.DefaultStopTimeout); err != nil {
		s.logger.Error("restartService: failed to restart service", "service", name, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to restart service"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
}

func (s *Server) reload(w http.ResponseWriter, r *http.Request) {
	result, err := s.daemon.Reload(r.Context())
	if err != nil {
		s.logger.Error("reload: failed to reload daemon", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) gpuInfo(w http.ResponseWriter, r *http.Request) {
	if s.gpu == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, s.gpu.Info())
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode JSON response", "error", err)
	}
}
