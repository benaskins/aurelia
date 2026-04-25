package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/benaskins/aurelia/internal/config"
	"github.com/benaskins/aurelia/internal/daemon"
	"github.com/benaskins/aurelia/internal/gpu"
	"github.com/benaskins/aurelia/internal/health"
	"github.com/benaskins/aurelia/internal/keychain"
	"github.com/benaskins/aurelia/internal/node"
	"github.com/benaskins/aurelia/internal/sysinfo"
)

//go:embed ui
var uiFS embed.FS

// Server serves the aurelia REST API over a Unix socket.
type Server struct {
	daemon         *daemon.Daemon
	gpu            *gpu.Observer
	listener       net.Listener
	server         *http.Server
	tcpServer      *http.Server // separate server for TCP with auth middleware
	logger         *slog.Logger
	token          string // bearer token for TCP auth (empty = no auth)
	prevToken      string // previous token during rotation (valid until rotation completes)
	tokenPath      string // path to token file on disk
	tokenMu        sync.RWMutex
	nodeName       string // local node name for stamping on service states
	laminaRoot     string // workspace root for lamina CLI execution
	configPath     string // path to config file for token updates
	rateLimiter    *rateLimitMiddleware
	tokenVendor    *keychain.BaoTokenVendor
	knownNodes     map[string]bool // valid peer CNs for token vending
	pkiIssuer      *keychain.BaoPKIIssuer
	secretCache    *keychain.CachedStore
	shutdownCancel context.CancelFunc // cancels all request contexts on Shutdown
}

// NewServer creates an API server backed by the given daemon.
// The GPU observer is optional — if nil, /v1/gpu returns empty.
func NewServer(d *daemon.Daemon, gpuObs *gpu.Observer) *Server {
	s := &Server{
		daemon:      d,
		gpu:         gpuObs,
		logger:      slog.With("component", "api"),
		rateLimiter: newRateLimitMiddleware(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/services", s.listServices)
	mux.HandleFunc("GET /v1/services/{name}/inspect", s.inspectService)
	mux.HandleFunc("GET /v1/services/{name}/health", s.serviceHealth)
	mux.HandleFunc("GET /v1/services/{name}/deps", s.serviceDeps)
	mux.HandleFunc("GET /v1/services/{name}", s.getService)
	mux.HandleFunc("POST /v1/services/{name}/start", s.startService)
	mux.HandleFunc("POST /v1/services/{name}/stop", s.stopService)
	mux.HandleFunc("POST /v1/services/{name}/restart", s.restartService)
	mux.HandleFunc("POST /v1/services/{name}/deploy", s.deployService)
	mux.HandleFunc("POST /v1/services/{name}/ship", s.shipService)
	mux.HandleFunc("DELETE /v1/services/{name}", s.removeService)
	mux.HandleFunc("GET /v1/services/{name}/logs", s.serviceLogs)
	mux.HandleFunc("GET /v1/graph", s.graph)
	mux.HandleFunc("POST /v1/reload", s.reload)
	mux.HandleFunc("GET /v1/gpu", s.gpuInfo)
	mux.HandleFunc("GET /v1/system", s.systemInfo)
	mux.HandleFunc("GET /v1/health", s.health)

	// Cluster endpoints — aggregate across peers
	mux.HandleFunc("GET /v1/cluster/services", s.clusterListServices)
	mux.HandleFunc("GET /v1/cluster/graph", s.clusterGraph)
	mux.HandleFunc("GET /v1/cluster/services/{name}/logs", s.clusterServiceLogs)
	mux.HandleFunc("POST /v1/cluster/services/{name}/{action}", s.clusterServiceAction)

	// Secret cache (local socket)
	mux.HandleFunc("GET /v1/secrets/{key}", s.secretGet)

	// Tessera: bulk secret fetch and cache invalidation (mTLS-only)
	mux.HandleFunc("GET /v1/secrets", s.secretsList)
	mux.HandleFunc("POST /v1/cache/invalidate", s.cacheInvalidate)

	// Lamina workspace CLI execution
	mux.HandleFunc("POST /v1/lamina", s.laminaExec)

	// Token management
	mux.HandleFunc("POST /v1/token/rotate", s.tokenRotate)

	// Peer token distribution (mTLS-only)
	mux.HandleFunc("POST /v1/peer/token", s.peerTokenUpdate)

	// OpenBao token vending (mTLS-only)
	mux.HandleFunc("POST /v1/openbao/token", s.openbaoToken)

	// PKI cert renewal (mTLS-only, node certs only)
	mux.HandleFunc("POST /v1/pki/renew", s.pkiRenew)

	// PKI cert issuance (mTLS-only, any role)
	mux.HandleFunc("POST /v1/pki/issue", s.pkiIssue)

	// Web UI — serve embedded static files
	uiContent, _ := fs.Sub(uiFS, "ui")
	mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.FS(uiContent))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	s.shutdownCancel = shutdownCancel

	s.server = &http.Server{
		Handler:           mux,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0, // disabled: safe on Unix socket (localhost-only, no untrusted slow clients); required for streaming endpoints
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1MB
		BaseContext: func(net.Listener) context.Context {
			return shutdownCtx
		},
	}
	return s
}

// GenerateToken loads an existing bearer token from tokenPath, or creates a
// new one if the file does not exist. This ensures tokens are stable across
// daemon restarts so peer nodes don't need re-configuration.
func (s *Server) GenerateToken(tokenPath string) error {
	s.tokenPath = tokenPath
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

// RotateToken generates a new token, keeping the old one valid until
// CommitTokenRotation is called. Returns the new token.
func (s *Server) RotateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}
	newToken := hex.EncodeToString(b)

	s.tokenMu.Lock()
	s.prevToken = s.token
	s.token = newToken
	s.tokenMu.Unlock()

	// Write new token to disk
	if s.tokenPath != "" {
		if err := os.WriteFile(s.tokenPath, []byte(newToken), 0600); err != nil {
			return "", fmt.Errorf("writing token file: %w", err)
		}
	}

	s.logger.Info("token rotated, dual-token window active")
	return newToken, nil
}

// CommitTokenRotation invalidates the previous token, completing the rotation.
func (s *Server) CommitTokenRotation() {
	s.tokenMu.Lock()
	s.prevToken = ""
	s.tokenMu.Unlock()
	s.logger.Info("token rotation committed, previous token invalidated")
}

// SetConfigPath sets the path to the config file for token updates from peers.
func (s *Server) SetConfigPath(path string) {
	s.configPath = path
}

// validToken returns true if the provided token matches either the current or previous token.
func (s *Server) validToken(provided string) bool {
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	if subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) == 1 {
		return true
	}
	if s.prevToken != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(s.prevToken)) == 1 {
		return true
	}
	return false
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

	// Wrap with rate limit + auth + audit middleware for TCP connections
	s.tcpServer = &http.Server{
		Handler:           s.rateLimiter.handler(s.requireToken(s.auditLog(s.server.Handler))),
		ReadTimeout:       s.server.ReadTimeout,
		WriteTimeout:      s.server.WriteTimeout,
		ReadHeaderTimeout: s.server.ReadHeaderTimeout,
		IdleTimeout:       s.server.IdleTimeout,
		MaxHeaderBytes:    s.server.MaxHeaderBytes,
		BaseContext:       s.server.BaseContext,
	}
	return s.tcpServer.Serve(ln)
}

// LoadTLSConfig creates a tls.Config for the TCP listener from cert, key, and CA paths.
// The config requests (but does not require) client certs, allowing both mTLS peers
// and bearer-token CLI clients.
//
// Certificates are reloaded from disk on each TLS handshake, so replacing cert
// files on disk takes effect without restarting the daemon.
func LoadTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	// Validate that files are readable at startup (fail-fast).
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		return nil, fmt.Errorf("loading server cert/key: %w", err)
	}

	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("reading CA cert: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("CA cert file contains no valid certificates")
	}

	return &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			cert, err := tls.LoadX509KeyPair(certFile, keyFile)
			if err != nil {
				return nil, err
			}
			return &cert, nil
		},
		ClientCAs:  caPool,
		ClientAuth: tls.VerifyClientCertIfGiven,
		MinVersion: tls.VersionTLS13,
	}, nil
}

// LoadPeerTLSConfig creates a tls.Config for outbound peer connections (mTLS client).
// Uses the same cert/key as the server to authenticate as a peer.
//
// The client certificate is reloaded from disk on each TLS handshake, so
// replacing cert files on disk takes effect without restarting the daemon.
func LoadPeerTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		return nil, fmt.Errorf("loading client cert/key: %w", err)
	}

	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("reading CA cert: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("CA cert file contains no valid certificates")
	}

	return &tls.Config{
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			cert, err := tls.LoadX509KeyPair(certFile, keyFile)
			if err != nil {
				return nil, err
			}
			return &cert, nil
		},
		RootCAs:    caPool,
		MinVersion: tls.VersionTLS13,
	}, nil
}

// ListenTLS starts the server on a TLS-encrypted TCP address.
// Clients presenting a valid client certificate (mTLS) are authenticated by cert CN.
// Clients without a client certificate must provide a bearer token.
func (s *Server) ListenTLS(addr string, tlsConfig *tls.Config) error {
	if s.token == "" {
		return fmt.Errorf("TLS API requires authentication; call GenerateToken first")
	}

	if host, _, err := net.SplitHostPort(addr); err == nil {
		switch host {
		case "127.0.0.1", "::1", "localhost":
		default:
			s.logger.Warn("TCP API binding to non-loopback address",
				"addr", addr)
		}
	}

	ln, err := tls.Listen("tcp", addr, tlsConfig)
	if err != nil {
		return err
	}
	s.logger.Info("API listening (TLS)", "addr", addr)

	s.tcpServer = &http.Server{
		Handler:           s.rateLimiter.handler(s.requireAuth(s.auditLog(s.server.Handler))),
		ReadTimeout:       s.server.ReadTimeout,
		WriteTimeout:      s.server.WriteTimeout,
		ReadHeaderTimeout: s.server.ReadHeaderTimeout,
		IdleTimeout:       s.server.IdleTimeout,
		MaxHeaderBytes:    s.server.MaxHeaderBytes,
		BaseContext:       s.server.BaseContext,
	}
	return s.tcpServer.Serve(ln)
}

// requireAuth returns middleware that authenticates via client cert (mTLS) or bearer token.
// If a verified client cert is present, the peer identity (cert CN) is set on the request
// context. Otherwise, the bearer token is validated.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for verified client certificate (mTLS)
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			cn := r.TLS.PeerCertificates[0].Subject.CommonName
			ctx := context.WithValue(r.Context(), peerIdentityKey, cn)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Fall back to bearer token
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		provided := strings.TrimPrefix(auth, "Bearer ")
		if !s.validToken(provided) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		ctx := context.WithValue(r.Context(), peerIdentityKey, "cli")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type contextKey string

const peerIdentityKey contextKey = "peer_identity"

// PeerIdentity returns the authenticated peer identity from the request context.
// Returns "cli" for bearer-token clients, or the cert CN for mTLS peers.
func PeerIdentity(ctx context.Context) string {
	if v, ok := ctx.Value(peerIdentityKey).(string); ok {
		return v
	}
	return ""
}

// auditLog returns middleware that logs every request with peer identity, method, path,
// status code, and duration.
func (s *Server) auditLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		peer := PeerIdentity(r.Context())
		if peer == "" {
			peer = "unknown"
		}
		s.logger.Info("api.request",
			"peer", peer,
			"remote_addr", r.RemoteAddr,
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
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
		if !s.validToken(provided) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Shutdown gracefully shuts down both the Unix and TCP API servers.
// It cancels all active request contexts first, allowing streaming handlers
// (such as logs --follow) to exit promptly.
func (s *Server) Shutdown(ctx context.Context) error {
	s.shutdownCancel() // cancel all active request contexts
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

func (s *Server) serviceHealth(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	state, err := s.daemon.ServiceState(name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": errorMessage("service not found", err, r)})
		return
	}
	history, _ := s.daemon.ServiceHealthHistory(name)

	type healthResponse struct {
		Status  string               `json:"status"`
		History []health.CheckRecord `json:"history"`
	}
	writeJSON(w, http.StatusOK, healthResponse{
		Status:  string(state.Health),
		History: history,
	})
}

func (s *Server) graph(w http.ResponseWriter, r *http.Request) {
	nodes := s.daemon.ServiceGraph()
	writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) serviceDeps(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	deps, err := s.daemon.ServiceDeps(name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": errorMessage("service not found", err, r)})
		return
	}
	writeJSON(w, http.StatusOK, deps)
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

func (s *Server) removeService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.isExternalGuard(w, name, "remove") {
		return
	}
	if err := s.daemon.RemoveService(name, daemon.DefaultStopTimeout); err != nil {
		s.logger.Error("removeService: failed to remove service", "service", name, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errorMessage("failed to remove service", err, r)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
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

	if r.URL.Query().Get("follow") == "true" {
		s.serviceLogsFollow(w, r, name)
		return
	}

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

func (s *Server) serviceLogsFollow(w http.ResponseWriter, r *http.Request, name string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	// Get initial snapshot and verify the service exists in one call.
	lines, gen, err := s.daemon.ServiceLogsSince(name, 0)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": errorMessage("service not found", err, r)})
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	flusher.Flush() // send headers to client immediately

	// emit writes lines to the response, flushing if any were written.
	// Returns false if the client disconnected.
	emit := func(lines []string) bool {
		for _, line := range lines {
			if _, werr := fmt.Fprintln(w, line); werr != nil {
				return false
			}
		}
		if len(lines) > 0 {
			flusher.Flush()
		}
		return true
	}
	if !emit(lines) {
		return
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}

		lines, newGen, err := s.daemon.ServiceLogsSince(name, gen)
		if err != nil {
			// Service removed while following — close stream
			return
		}
		if !emit(lines) {
			return
		}
		gen = newGen
	}
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

func (s *Server) systemInfo(w http.ResponseWriter, r *http.Request) {
	snap, err := sysinfo.Snapshot()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snap)
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

// SetTokenVendor configures the OpenBao token vendor and the set of known
// node names that are allowed to request scoped tokens.
func (s *Server) SetTokenVendor(vendor *keychain.BaoTokenVendor, nodes []config.Node) {
	s.tokenVendor = vendor
	s.knownNodes = make(map[string]bool, len(nodes))
	for _, n := range nodes {
		s.knownNodes[n.Name] = true
	}
}

// openbaoToken handles scoped token vending for authenticated peers.
// Requires mTLS — the peer CN must match a known node name.
// Returns a short-lived OpenBao token with policy "node-{CN}".
func (s *Server) openbaoToken(w http.ResponseWriter, r *http.Request) {
	peer := PeerIdentity(r.Context())
	if peer == "" || peer == "cli" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "token vending requires mTLS authentication",
		})
		return
	}

	if s.tokenVendor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "token vending not configured",
		})
		return
	}

	if !s.knownNodes[peer] {
		s.logger.Warn("token vend rejected: unknown node", "peer", peer)
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": fmt.Sprintf("unknown node %q", peer),
		})
		return
	}

	policy := "node-" + peer
	ttl := 60 * time.Second

	resp, err := s.tokenVendor.VendToken([]string{policy}, ttl)
	if err != nil {
		s.logger.Error("token vend failed", "peer", peer, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "failed to create token",
		})
		return
	}

	s.logger.Info("token vended", "peer", peer, "policy", policy, "ttl", ttl)
	writeJSON(w, http.StatusOK, resp)
}

// SetPKIIssuer configures the PKI certificate issuer and the set of known
// node names that are allowed to renew certificates.
func (s *Server) SetPKIIssuer(issuer *keychain.BaoPKIIssuer, nodes []config.Node) {
	s.pkiIssuer = issuer
	// Ensure knownNodes is initialized (may already be set by SetTokenVendor)
	if s.knownNodes == nil {
		s.knownNodes = make(map[string]bool, len(nodes))
	}
	for _, n := range nodes {
		s.knownNodes[n.Name] = true
	}
}

// pkiRenew handles certificate renewal for authenticated peers.
// Requires mTLS — the peer CN must match a known node name.
// Issues a new node cert via the PKI secrets engine and returns it.
func (s *Server) pkiRenew(w http.ResponseWriter, r *http.Request) {
	peer := PeerIdentity(r.Context())
	if peer == "" || peer == "cli" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "certificate renewal requires mTLS authentication",
		})
		return
	}

	if s.pkiIssuer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "PKI issuer not configured",
		})
		return
	}

	if !s.knownNodes[peer] {
		s.logger.Warn("pki renew rejected: unknown node", "peer", peer)
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": fmt.Sprintf("unknown node %q", peer),
		})
		return
	}

	cert, err := s.pkiIssuer.IssueNodeCert(peer, "72h")
	if err != nil {
		s.logger.Error("pki renew failed", "peer", peer, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "failed to issue certificate",
		})
		return
	}

	s.logger.Info("pki cert renewed", "peer", peer, "serial", cert.Serial)
	writeJSON(w, http.StatusOK, cert)
}

// pkiIssue handles general-purpose certificate issuance for authenticated peers.
// Requires mTLS — the peer CN must match a known node name.
// Accepts role, common_name, and ttl in the request body.
func (s *Server) pkiIssue(w http.ResponseWriter, r *http.Request) {
	peer := PeerIdentity(r.Context())
	if peer == "" || peer == "cli" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "certificate issuance requires mTLS authentication",
		})
		return
	}

	if s.pkiIssuer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "PKI issuer not configured",
		})
		return
	}

	if !s.knownNodes[peer] {
		s.logger.Warn("pki issue rejected: unknown node", "peer", peer)
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": fmt.Sprintf("unknown node %q", peer),
		})
		return
	}

	var req struct {
		Role       string `json:"role"`
		CommonName string `json:"common_name"`
		TTL        string `json:"ttl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Role == "" || req.CommonName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role and common_name are required"})
		return
	}
	if req.TTL == "" {
		req.TTL = "720h"
	}

	cert, err := s.pkiIssuer.Issue(req.Role, req.CommonName, req.TTL)
	if err != nil {
		s.logger.Error("pki issue failed", "peer", peer, "role", req.Role, "cn", req.CommonName, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "failed to issue certificate",
		})
		return
	}

	s.logger.Info("pki cert issued", "peer", peer, "role", req.Role, "cn", req.CommonName, "serial", cert.Serial)
	writeJSON(w, http.StatusOK, cert)
}

const clusterAggregationTimeout = 10 * time.Second

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

	// Fan out to reachable peers with an overall deadline
	ctx, cancel := context.WithTimeout(r.Context(), clusterAggregationTimeout)
	defer cancel()

	peers := s.daemon.Peers()
	peerReachable := s.daemon.PeerStates()
	peerStatus := make(map[string]string) // peer name -> "ok", "timeout", "error", "unreachable"

	type peerResult struct {
		name   string
		states []daemon.ServiceState
		err    error
	}
	results := make(chan peerResult, len(peers))

	expected := 0
	for name, c := range peers {
		if !peerReachable[name] {
			peerStatus[name] = "unreachable"
			continue
		}
		expected++
		go func(name string, c *node.Client) {
			raw, err := c.StatusContext(ctx)
			if err != nil {
				results <- peerResult{name: name, err: err}
				return
			}
			var states []daemon.ServiceState
			if err := json.Unmarshal(raw, &states); err != nil {
				results <- peerResult{name: name, err: fmt.Errorf("decoding %s: %w", name, err)}
				return
			}
			for i := range states {
				if states[i].Node == "" {
					states[i].Node = name
				}
			}
			results <- peerResult{name: name, states: states}
		}(name, c)
	}

	for i := 0; i < expected; i++ {
		res := <-results
		if res.err != nil {
			if ctx.Err() != nil {
				peerStatus[res.name] = "timeout"
			} else {
				peerStatus[res.name] = "error"
			}
			s.logger.Warn("failed to get peer status", "peer", res.name, "error", res.err)
			continue
		}
		peerStatus[res.name] = "ok"
		allStates = append(allStates, res.states...)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"services": allStates,
		"peers":    peerStatus,
	})
}

func (s *Server) clusterGraph(w http.ResponseWriter, r *http.Request) {
	// Get local graph and stamp node name
	localNodes := s.daemon.ServiceGraph()
	nodeName := s.nodeName
	if nodeName == "" {
		nodeName = "local"
	}
	for i := range localNodes {
		localNodes[i].Node = nodeName
	}

	allNodes := localNodes

	// Fan out to reachable peers
	ctx, cancel := context.WithTimeout(r.Context(), clusterAggregationTimeout)
	defer cancel()

	peers := s.daemon.Peers()
	peerReachable := s.daemon.PeerStates()
	peerStatus := make(map[string]string)

	type peerResult struct {
		name  string
		nodes []daemon.GraphNode
		err   error
	}
	results := make(chan peerResult, len(peers))

	expected := 0
	for name, c := range peers {
		if !peerReachable[name] {
			peerStatus[name] = "unreachable"
			continue
		}
		expected++
		go func(name string, c *node.Client) {
			raw, err := c.GraphContext(ctx)
			if err != nil {
				results <- peerResult{name: name, err: err}
				return
			}
			var nodes []daemon.GraphNode
			if err := json.Unmarshal(raw, &nodes); err != nil {
				results <- peerResult{name: name, err: fmt.Errorf("decoding %s: %w", name, err)}
				return
			}
			for i := range nodes {
				if nodes[i].Node == "" {
					nodes[i].Node = name
				}
			}
			results <- peerResult{name: name, nodes: nodes}
		}(name, c)
	}

	for i := 0; i < expected; i++ {
		res := <-results
		if res.err != nil {
			if ctx.Err() != nil {
				peerStatus[res.name] = "timeout"
			} else {
				peerStatus[res.name] = "error"
			}
			s.logger.Warn("failed to get peer graph", "peer", res.name, "error", res.err)
			continue
		}
		peerStatus[res.name] = "ok"
		allNodes = append(allNodes, res.nodes...)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": allNodes,
		"peers": peerStatus,
	})
}

func (s *Server) clusterServiceLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	targetNode := r.URL.Query().Get("node")

	if targetNode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "node query parameter required"})
		return
	}

	const maxLogLines = 10000
	n := 100
	if qn := r.URL.Query().Get("n"); qn != "" {
		if parsed, err := strconv.Atoi(qn); err == nil && parsed > 0 {
			n = min(parsed, maxLogLines)
		}
	}

	// Route to local daemon if targeting self
	nodeName := s.nodeName
	if nodeName == "" {
		nodeName = "local"
	}
	if targetNode == nodeName {
		lines, err := s.daemon.ServiceLogs(name, n)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": errorMessage("service not found", err, r)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
		return
	}

	// Find peer
	peers := s.daemon.Peers()
	peer, ok := peers[targetNode]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("node %q not found", targetNode)})
		return
	}

	lines, err := peer.Logs(name, n)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
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

// tokenRotate handles the full token rotation flow:
// 1. Generate new token (old remains valid)
// 2. Push to all reachable peers via mTLS
// 3. On quorum, invalidate old token
func (s *Server) tokenRotate(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "true"

	newToken, err := s.RotateToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Push to peers
	peers := s.daemon.Peers()
	peerResults := make(map[string]string)
	confirmed := 0
	total := 0

	for name, c := range peers {
		total++
		if err := c.PushToken(s.nodeName, newToken); err != nil {
			peerResults[name] = "failed: " + err.Error()
			s.logger.Warn("failed to push token to peer", "peer", name, "error", err)
		} else {
			peerResults[name] = "ok"
			confirmed++
		}
	}

	// Quorum check: all reachable peers confirmed
	if confirmed == total || force {
		s.CommitTokenRotation()
		writeJSON(w, http.StatusOK, map[string]any{
			"status":    "rotated",
			"peers":     peerResults,
			"confirmed": confirmed,
			"total":     total,
		})
	} else {
		// Keep dual-token window open
		writeJSON(w, http.StatusOK, map[string]any{
			"status":    "partial",
			"peers":     peerResults,
			"confirmed": confirmed,
			"total":     total,
			"message":   "some peers unreachable, old token still valid; use --force to override",
		})
	}
}

// peerTokenUpdate handles token distribution from a peer during rotation.
// Only accessible via mTLS (requires verified client cert).
func (s *Server) peerTokenUpdate(w http.ResponseWriter, r *http.Request) {
	// Require mTLS (cert identity, not CLI bearer token)
	peer := PeerIdentity(r.Context())
	if peer == "" || peer == "cli" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "peer token update requires mTLS authentication",
		})
		return
	}

	var req struct {
		Node  string `json:"node"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.Node == "" || req.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "node and token required"})
		return
	}

	// Update config file on disk
	cfgPath := s.configPath
	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}
	if err := config.UpdateNodeToken(cfgPath, req.Node, req.Token); err != nil {
		s.logger.Error("failed to update peer token", "peer", peer, "node", req.Node, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": errorMessage("failed to update token", err, r),
		})
		return
	}

	s.logger.Info("peer token updated", "from_peer", peer, "for_node", req.Node)
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
