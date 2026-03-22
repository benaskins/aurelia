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
	"strconv"
	"strings"
	"time"

	"github.com/benaskins/aurelia/internal/daemon"
	"github.com/benaskins/aurelia/internal/gpu"
	"github.com/benaskins/aurelia/internal/node"
)

// Server serves the aurelia REST API over a Unix socket.
type Server struct {
	daemon     *daemon.Daemon
	gpu        *gpu.Observer
	listener   net.Listener
	server     *http.Server
	tcpServer  *http.Server // separate server for TCP with auth middleware
	logger     *slog.Logger
	token      string // bearer token for TCP auth (empty = no auth)
	nodeName   string // local node name for stamping on service states
	laminaRoot string // workspace root for lamina CLI execution
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
	mux.HandleFunc("GET /v1/services/{name}/inspect", s.inspectService)
	mux.HandleFunc("GET /v1/services/{name}", s.getService)
	mux.HandleFunc("POST /v1/services/{name}/start", s.startService)
	mux.HandleFunc("POST /v1/services/{name}/stop", s.stopService)
	mux.HandleFunc("POST /v1/services/{name}/restart", s.restartService)
	mux.HandleFunc("POST /v1/services/{name}/deploy", s.deployService)
	mux.HandleFunc("POST /v1/services/{name}/ship", s.shipService)
	mux.HandleFunc("GET /v1/services/{name}/logs", s.serviceLogs)
	mux.HandleFunc("POST /v1/reload", s.reload)
	mux.HandleFunc("GET /v1/gpu", s.gpuInfo)
	mux.HandleFunc("GET /v1/health", s.health)

	// Cluster endpoints — aggregate across peers
	mux.HandleFunc("GET /v1/cluster/services", s.clusterListServices)
	mux.HandleFunc("POST /v1/cluster/services/{name}/{action}", s.clusterServiceAction)

	// Lamina workspace CLI execution
	mux.HandleFunc("POST /v1/lamina", s.laminaExec)

	s.server = &http.Server{
		Handler:           mux,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute, // deploy endpoint blocks for health checks + drain
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1MB
	}
	return s
}

// GenerateToken loads an existing bearer token from tokenPath, or creates a
// new one if the file does not exist. This ensures tokens are stable across
// daemon restarts so peer nodes don't need re-configuration.
func (s *Server) GenerateToken(tokenPath string) error {
	// Reuse existing token if present
	if data, err := os.ReadFile(tokenPath); err == nil {
		token := strings.TrimSpace(string(data))
		if len(token) > 0 {
			s.token = token
			s.logger.Info("API token loaded", "path", tokenPath)
			return nil
		}
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Errorf("generating token: %w", err)
	}
	s.token = hex.EncodeToString(b)
	if err := os.WriteFile(tokenPath, []byte(s.token), 0600); err != nil {
		return fmt.Errorf("writing token file: %w", err)
	}
	s.logger.Info("API token generated", "path", tokenPath)
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

	// Warn if binding to a non-loopback address
	if host, _, err := net.SplitHostPort(addr); err == nil {
		switch host {
		case "127.0.0.1", "::1", "localhost":
			// loopback — safe
		default:
			s.logger.Warn("TCP API binding to non-loopback address — the API will be accessible from other machines on the network",
				"addr", addr)
		}
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
		writeJSON(w, http.StatusNotFound, map[string]string{"error": errorMessage("service not found", err, r)})
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) inspectService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	inspect, err := s.daemon.InspectService(name)
	if err != nil {
		s.logger.Warn("inspectService: service not found", "service", name, "error", err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": errorMessage("service not found", err, r)})
		return
	}
	writeJSON(w, http.StatusOK, inspect)
}

func (s *Server) isExternalGuard(w http.ResponseWriter, name, action string) bool {
	if s.daemon.IsExternal(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("cannot %s external service %q", action, name),
		})
		return true
	}
	return false
}

func (s *Server) startService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.isExternalGuard(w, name, "start") {
		return
	}
	if err := s.daemon.StartService(r.Context(), name); err != nil {
		s.logger.Error("startService: failed to start service", "service", name, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errorMessage("failed to start service", err, r)})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "starting"})
}

func (s *Server) stopService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.isExternalGuard(w, name, "stop") {
		return
	}
	if err := s.daemon.StopService(name, daemon.DefaultStopTimeout); err != nil {
		s.logger.Error("stopService: failed to stop service", "service", name, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errorMessage("failed to stop service", err, r)})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
}

func (s *Server) restartService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.isExternalGuard(w, name, "restart") {
		return
	}
	if err := s.daemon.RestartService(name, daemon.DefaultStopTimeout); err != nil {
		s.logger.Error("restartService: failed to restart service", "service", name, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errorMessage("failed to restart service", err, r)})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
}

func (s *Server) deployService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.isExternalGuard(w, name, "deploy") {
		return
	}
	drain := daemon.DefaultDrainTimeout
	if d := r.URL.Query().Get("drain"); d != "" {
		if parsed, err := time.ParseDuration(d); err == nil && parsed > 0 {
			drain = parsed
		}
	}
	s.logger.Info("deploy request", "service", name, "drain", drain)
	if err := s.daemon.DeployService(name, drain); err != nil {
		s.logger.Error("deployService: failed to deploy service", "service", name, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errorMessage("failed to deploy service", err, r)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deployed"})
}

func (s *Server) shipService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.logger.Info("ship request", "service", name)
	result, err := s.daemon.ShipService(name)
	if err != nil {
		s.logger.Error("shipService: failed", "service", name, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errorMessage("ship failed", err, r)})
		return
	}
	status := http.StatusOK
	if !result.Success {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, result)
}

func (s *Server) serviceLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	const maxLogLines = 10000
	n := 100
	if qn := r.URL.Query().Get("n"); qn != "" {
		if parsed, err := strconv.Atoi(qn); err == nil && parsed > 0 {
			n = min(parsed, maxLogLines)
		}
	}
	lines, err := s.daemon.ServiceLogs(name, n)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": errorMessage("service not found", err, r)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}
func (s *Server) reload(w http.ResponseWriter, r *http.Request) {
	result, err := s.daemon.Reload(r.Context())
	if err != nil {
		s.logger.Error("reload: failed to reload daemon", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": errorMessage("reload failed", err, r)})
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

// errorMessage returns the full error for Unix socket clients (already
// authenticated by file permissions) or a generic message for TCP clients.
func errorMessage(generic string, err error, r *http.Request) string {
	if isUnixSocket(r) {
		return err.Error()
	}
	return generic
}

// isUnixSocket returns true if the request arrived via a Unix socket.
// Unix socket connections have an empty RemoteAddr or one starting with @.
func isUnixSocket(r *http.Request) bool {
	addr := r.RemoteAddr
	return addr == "" || strings.HasPrefix(addr, "@")
}

// SetNodeName sets the local node name used to stamp service states.
func (s *Server) SetNodeName(name string) {
	s.nodeName = name
}

// SetLaminaRoot sets the workspace root for lamina CLI execution.
func (s *Server) SetLaminaRoot(root string) {
	s.laminaRoot = root
}

func (s *Server) clusterListServices(w http.ResponseWriter, r *http.Request) {
	// Get local services and stamp node name
	localStates := s.daemon.ServiceStates()
	nodeName := s.nodeName
	if nodeName == "" {
		nodeName = "local"
	}
	for i := range localStates {
		localStates[i].Node = nodeName
	}

	allStates := localStates

	// Fan out to reachable peers in parallel
	peers := s.daemon.Peers()
	peerReachable := s.daemon.PeerStates()

	type peerResult struct {
		states []daemon.ServiceState
		err    error
	}
	results := make(chan peerResult, len(peers))

	for name, c := range peers {
		if !peerReachable[name] {
			continue
		}
		go func(name string, c *node.Client) {
			raw, err := c.Status()
			if err != nil {
				results <- peerResult{err: err}
				return
			}
			var states []daemon.ServiceState
			if err := json.Unmarshal(raw, &states); err != nil {
				results <- peerResult{err: fmt.Errorf("decoding %s: %w", name, err)}
				return
			}
			// Stamp node name on each state
			for i := range states {
				if states[i].Node == "" {
					states[i].Node = name
				}
			}
			results <- peerResult{states: states}
		}(name, c)
	}

	// Collect results
	expected := 0
	for name := range peers {
		if peerReachable[name] {
			expected++
		}
	}
	for i := 0; i < expected; i++ {
		res := <-results
		if res.err != nil {
			s.logger.Warn("failed to get peer status", "error", res.err)
			continue
		}
		allStates = append(allStates, res.states...)
	}

	writeJSON(w, http.StatusOK, allStates)
}

func (s *Server) clusterServiceAction(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	action := r.PathValue("action")
	targetNode := r.URL.Query().Get("node")

	if targetNode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "node query parameter required"})
		return
	}

	// Route to local daemon if targeting self
	nodeName := s.nodeName
	if nodeName == "" {
		nodeName = "local"
	}
	if targetNode == nodeName {
		s.routeLocalAction(w, r, name, action)
		return
	}

	// Find peer
	peers := s.daemon.Peers()
	peer, ok := peers[targetNode]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("node %q not found", targetNode)})
		return
	}

	// Proxy to peer
	var err error
	switch action {
	case "start":
		err = peer.StartService(name)
	case "stop":
		err = peer.StopService(name)
	case "restart":
		err = peer.RestartService(name)
	case "deploy":
		err = peer.DeployService(name)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("unknown action %q", action)})
		return
	}

	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": action + "ing", "node": targetNode})
}

func (s *Server) routeLocalAction(w http.ResponseWriter, r *http.Request, name, action string) {
	var err error
	switch action {
	case "start":
		err = s.daemon.StartService(r.Context(), name)
	case "stop":
		err = s.daemon.StopService(name, daemon.DefaultStopTimeout)
	case "restart":
		err = s.daemon.RestartService(name, daemon.DefaultStopTimeout)
	case "deploy":
		err = s.daemon.DeployService(name, daemon.DefaultDrainTimeout)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("unknown action %q", action)})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": action + "ing"})
}
