package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"

	"github.com/benaskins/aurelia/internal/daemon"
	"github.com/benaskins/aurelia/internal/gpu"
)

// Server serves the aurelia REST API over a Unix socket.
type Server struct {
	daemon   *daemon.Daemon
	gpu      *gpu.Observer
	listener net.Listener
	server   *http.Server
	logger   *slog.Logger
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

	s.server = &http.Server{Handler: mux}
	return s
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

// ListenTCP starts the server on a TCP address.
func (s *Server) ListenTCP(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.listener = ln
	s.logger.Info("API listening", "addr", addr)
	return s.server.Serve(ln)
}

// Shutdown gracefully shuts down the API server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	states := s.daemon.ServiceStates()
	writeJSON(w, http.StatusOK, states)
}

func (s *Server) getService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	state, err := s.daemon.ServiceState(name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) startService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.daemon.StartService(r.Context(), name); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "starting"})
}

func (s *Server) stopService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.daemon.StopService(name, daemon.DefaultStopTimeout); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
}

func (s *Server) restartService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.daemon.RestartService(r.Context(), name, daemon.DefaultStopTimeout); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
}

func (s *Server) reload(w http.ResponseWriter, r *http.Request) {
	result, err := s.daemon.Reload(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
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
	json.NewEncoder(w).Encode(v)
}
