package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/atlasshare/atlax/pkg/audit"
)

// AdminServer serves the admin API: health check, Prometheus metrics,
// and CRUD operations for ports, agents, and stats.
type AdminServer struct {
	registry       AgentRegistry
	router         *PortRouter
	clientListener *ClientListener
	emitter        audit.Emitter
	store          *SidecarStore
	logger         *slog.Logger
	server         *http.Server
	socketPath     string
	startTime      time.Time
	ctx            context.Context // lifecycle context for spawned port listeners
}

// AdminConfig holds settings for the admin server.
type AdminConfig struct {
	// Addr is the TCP address (e.g., "127.0.0.1:9090"). If empty and
	// SocketPath is set, only the unix socket is used.
	Addr string

	// SocketPath is the unix domain socket path (e.g., "/var/run/atlax.sock").
	// If empty, only TCP is used.
	SocketPath string

	Registry       AgentRegistry
	Router         *PortRouter
	ClientListener *ClientListener
	Logger         *slog.Logger
	// Emitter receives audit events for mutating admin API operations.
	// If nil, mutations are logged but not audited.
	Emitter audit.Emitter
	// Store persists runtime port mutations to the sidecar JSON file.
	// If nil, mutations are not persisted (relay.yaml remains authoritative).
	Store *SidecarStore
}

// HealthResponse is the JSON body returned by /healthz.
type HealthResponse struct {
	Status  string `json:"status"`
	Agents  int    `json:"agents"`
	Streams int    `json:"streams"`
}

// StatsResponse is the JSON body returned by /stats.
type StatsResponse struct {
	Status        string  `json:"status"`
	Uptime        string  `json:"uptime"`
	UptimeSeconds float64 `json:"uptime_seconds"`
	Agents        int     `json:"agents"`
	Streams       int     `json:"streams"`
}

// PortResponse represents a single port mapping in API responses.
type PortResponse struct {
	Port       int    `json:"port"`
	CustomerID string `json:"customer_id"`
	Service    string `json:"service"`
	ListenAddr string `json:"listen_addr"`
	MaxStreams int    `json:"max_streams"`
}

// AgentResponse represents a connected agent in API responses.
type AgentResponse struct {
	CustomerID  string `json:"customer_id"`
	RemoteAddr  string `json:"remote_addr"`
	ConnectedAt string `json:"connected_at"`
	LastSeen    string `json:"last_seen"`
	StreamCount int    `json:"stream_count"`
}

// PortCreateRequest is the JSON body for POST /ports.
type PortCreateRequest struct {
	Port       int    `json:"port"`
	CustomerID string `json:"customer_id"`
	Service    string `json:"service"`
	MaxStreams int    `json:"max_streams"`
	ListenAddr string `json:"listen_addr"`
}

// NewAdminServer creates an admin server with the full API.
func NewAdminServer(cfg *AdminConfig) *AdminServer {
	a := &AdminServer{
		registry:       cfg.Registry,
		router:         cfg.Router,
		clientListener: cfg.ClientListener,
		emitter:        cfg.Emitter,
		store:          cfg.Store,
		logger:         cfg.Logger,
		socketPath:     cfg.SocketPath,
		startTime:      time.Now(),
	}

	mux := http.NewServeMux()

	// Liveness + readiness + metrics
	mux.HandleFunc("/healthz", a.handleHealth)
	mux.HandleFunc("/readyz", a.handleReady)
	mux.Handle("/metrics", promhttp.Handler())

	// CRUD endpoints
	mux.HandleFunc("/ports", a.handlePorts)
	mux.HandleFunc("/ports/", a.handlePortByID)
	mux.HandleFunc("/agents", a.handleAgents)
	mux.HandleFunc("/agents/", a.handleAgentByID)
	mux.HandleFunc("/stats", a.handleStats)

	a.server = &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return a
}

// Start begins serving on TCP and/or unix socket. Blocks until ctx is canceled.
func (a *AdminServer) Start(ctx context.Context) error {
	a.ctx = ctx

	go func() {
		<-ctx.Done()
		a.server.Close()
		if a.socketPath != "" {
			os.Remove(a.socketPath)
		}
	}()

	// Unix socket listener
	if a.socketPath != "" {
		if removeErr := os.Remove(a.socketPath); removeErr != nil && !os.IsNotExist(removeErr) {
			a.logger.Warn("admin: could not remove stale socket",
				"path", a.socketPath, "error", removeErr)
		}
		unixLn, err := net.Listen("unix", a.socketPath)
		if err != nil {
			// Socket-only mode (no TCP): fail because there is no fallback.
			if a.server.Addr == "" {
				return fmt.Errorf("admin: unix socket: %w", err)
			}
			// Dual mode: warn and continue with TCP only.
			a.logger.Warn("admin: unix socket failed, continuing with TCP only",
				"path", a.socketPath, "error", err)
		} else {
			os.Chmod(a.socketPath, 0o660) //nolint:errcheck // best-effort permissions
			a.logger.Info("relay: admin socket started", "path", a.socketPath)

			if a.server.Addr == "" {
				return a.serve(unixLn)
			}

			go func() {
				if serveErr := a.serve(unixLn); serveErr != nil {
					a.logger.Error("admin: unix socket error", "error", serveErr)
				}
			}()
		}
	}

	// TCP listener
	if a.server.Addr != "" {
		a.logger.Info("relay: admin server started", "addr", a.server.Addr)
		if err := a.server.ListenAndServe(); err != http.ErrServerClosed {
			return err
		}
	}
	return nil
}

func (a *AdminServer) serve(ln net.Listener) error {
	if err := a.server.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// --- Health ---

func (a *AdminServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	agents, err := a.registry.ListConnectedAgents(r.Context())
	if err != nil {
		http.Error(w, `{"status":"error"}`, http.StatusInternalServerError)
		return
	}

	totalStreams := 0
	for _, agent := range agents {
		totalStreams += agent.StreamCount
	}

	writeJSON(w, HealthResponse{
		Status:  "ok",
		Agents:  len(agents),
		Streams: totalStreams,
	})
}

// --- Readiness ---

// handleReady returns 200 when the registry is reachable and the admin
// server is serving requests (which it must be, to handle this call).
// Distinct from /healthz which counts active agents: /readyz is a
// liveness+registry probe suitable for ALB target group health checks.
func (a *AdminServer) handleReady(w http.ResponseWriter, r *http.Request) {
	if _, err := a.registry.ListConnectedAgents(r.Context()); err != nil {
		http.Error(w, `{"status":"not ready"}`, http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, map[string]string{"status": "ready"})
}

// --- Stats ---

func (a *AdminServer) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	agents, err := a.registry.ListConnectedAgents(r.Context())
	if err != nil {
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}

	totalStreams := 0
	for _, agent := range agents {
		totalStreams += agent.StreamCount
	}

	uptime := time.Since(a.startTime)
	writeJSON(w, StatsResponse{
		Status:        "ok",
		Uptime:        uptime.Round(time.Second).String(),
		UptimeSeconds: uptime.Seconds(),
		Agents:        len(agents),
		Streams:       totalStreams,
	})
}

// --- Ports ---

func (a *AdminServer) handlePorts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.listPorts(w, r)
	case http.MethodPost:
		a.createPort(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (a *AdminServer) handlePortByID(w http.ResponseWriter, r *http.Request) {
	portStr := strings.TrimPrefix(r.URL.Path, "/ports/")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		http.Error(w, `{"error":"invalid port number"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.getPort(w, port)
	case http.MethodDelete:
		a.deletePort(w, r, port)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (a *AdminServer) listPorts(w http.ResponseWriter, _ *http.Request) {
	infos := a.router.ListPorts()
	ports := make([]PortResponse, 0, len(infos))
	for _, info := range infos {
		ports = append(ports, portInfoToResponse(info))
	}
	writeJSON(w, ports)
}

func (a *AdminServer) getPort(w http.ResponseWriter, port int) {
	info, ok := a.router.GetPort(port)
	if !ok {
		http.Error(w, `{"error":"port not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, portInfoToResponse(info))
}

func portInfoToResponse(info PortInfo) PortResponse {
	return PortResponse{
		Port:       info.Port,
		CustomerID: info.CustomerID,
		Service:    info.Service,
		ListenAddr: info.ListenAddr,
		MaxStreams: info.MaxStreams,
	}
}

func (a *AdminServer) createPort(w http.ResponseWriter, r *http.Request) {
	var req PortCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.Port <= 0 || req.CustomerID == "" || req.Service == "" {
		http.Error(w, `{"error":"port, customer_id, and service are required"}`, http.StatusBadRequest)
		return
	}

	// Default listen address.
	listenAddr := req.ListenAddr
	if listenAddr == "" {
		listenAddr = "0.0.0.0"
	}

	if err := a.router.AddPortMapping(req.CustomerID, req.Port, req.Service, listenAddr, req.MaxStreams); err != nil {
		writeError(w, err.Error(), http.StatusConflict)
		return
	}

	addr := fmt.Sprintf("%s:%d", listenAddr, req.Port)

	listenerCtx := a.ctx
	if listenerCtx == nil {
		listenerCtx = context.Background()
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.clientListener.StartPort(listenerCtx, addr, req.Port)
	}()

	// Brief wait to detect immediate bind failure (port in use, permission denied).
	select {
	case err := <-errCh:
		// StartPort returned immediately -- bind failed.
		a.router.RemovePortMapping(req.CustomerID, req.Port) //nolint:errcheck // rollback best-effort
		writeError(w, fmt.Sprintf("listen %s: %v", addr, err), http.StatusConflict)
		return
	case <-time.After(50 * time.Millisecond):
		// Listener started successfully (blocking in accept loop).
	}

	// Persist the new mapping to the sidecar so it survives restart.
	// If no store is configured, warn that the change will be lost.
	if a.store != nil {
		if saveErr := a.store.SaveCurrentState(a.router.ListPorts()); saveErr != nil {
			a.logger.Warn("admin: failed to persist port mapping to sidecar",
				"port", req.Port, "error", saveErr)
		}
	} else {
		a.logger.Warn("admin: port mapping added at runtime is not persisted; add to relay.yaml to survive restart",
			"port", req.Port,
			"customer_id", req.CustomerID,
			"service", req.Service,
			"listen_addr", addr)
	}

	a.emitAudit(r.Context(), audit.ActionAdminPortAdded,
		fmt.Sprintf("%d", req.Port), req.CustomerID,
		map[string]string{"service": req.Service, "listen_addr": addr})

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, PortResponse{
		Port:       req.Port,
		CustomerID: req.CustomerID,
		Service:    req.Service,
		ListenAddr: listenAddr,
		MaxStreams: req.MaxStreams,
	})
}

func (a *AdminServer) deletePort(w http.ResponseWriter, r *http.Request, port int) {
	customerID, _, ok := a.router.LookupPort(port)
	if !ok {
		http.Error(w, `{"error":"port not found"}`, http.StatusNotFound)
		return
	}

	if err := a.router.RemovePortMapping(customerID, port); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Stop the TCP listener for this port. Both config-started and
	// admin-started listeners are registered in ClientListener.listeners,
	// so StopPort handles both. If it errors here the listener simply
	// was not known (unusual; log but do not fail the request).
	if err := a.clientListener.StopPort(port); err != nil {
		a.logger.Warn("admin: stop listener failed",
			"port", port, "error", err)
	}

	// Persist the updated mapping set to the sidecar. If the deleted port
	// is also defined in relay.yaml, warn that relay.yaml must be updated
	// separately to prevent the mapping reappearing on next restart.
	if a.store != nil {
		if saveErr := a.store.SaveCurrentState(a.router.ListPorts()); saveErr != nil {
			a.logger.Warn("admin: failed to persist port removal to sidecar",
				"port", port, "error", saveErr)
		}
	}
	a.logger.Warn("admin: port mapping removed at runtime; also remove from relay.yaml to prevent reappearance on restart",
		"port", port,
		"customer_id", customerID)

	a.emitAudit(r.Context(), audit.ActionAdminPortRemoved,
		fmt.Sprintf("%d", port), customerID, nil)

	w.WriteHeader(http.StatusNoContent)
}

// --- Agents ---

func (a *AdminServer) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	agents, err := a.registry.ListConnectedAgents(r.Context())
	if err != nil {
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}

	resp := make([]AgentResponse, len(agents))
	for i, ag := range agents {
		resp[i] = AgentResponse{
			CustomerID:  ag.CustomerID,
			RemoteAddr:  ag.RemoteAddr,
			ConnectedAt: ag.ConnectedAt.Format(time.RFC3339),
			LastSeen:    ag.LastSeen.Format(time.RFC3339),
			StreamCount: ag.StreamCount,
		}
	}
	writeJSON(w, resp)
}

func (a *AdminServer) handleAgentByID(w http.ResponseWriter, r *http.Request) {
	customerID := strings.TrimPrefix(r.URL.Path, "/agents/")
	if customerID == "" {
		http.Error(w, `{"error":"customer_id required"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.getAgent(w, r, customerID)
	case http.MethodDelete:
		a.deleteAgent(w, r, customerID)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (a *AdminServer) getAgent(w http.ResponseWriter, r *http.Request, customerID string) {
	agents, err := a.registry.ListConnectedAgents(r.Context())
	if err != nil {
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	for _, ag := range agents {
		if ag.CustomerID == customerID {
			writeJSON(w, AgentResponse{
				CustomerID:  ag.CustomerID,
				RemoteAddr:  ag.RemoteAddr,
				ConnectedAt: ag.ConnectedAt.Format(time.RFC3339),
				LastSeen:    ag.LastSeen.Format(time.RFC3339),
				StreamCount: ag.StreamCount,
			})
			return
		}
	}
	http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
}

func (a *AdminServer) deleteAgent(w http.ResponseWriter, r *http.Request, customerID string) {
	if err := a.registry.Unregister(r.Context(), customerID); err != nil {
		writeError(w, err.Error(), http.StatusNotFound)
		return
	}

	a.logger.Info("admin: agent disconnected",
		"customer_id", customerID)

	a.emitAudit(r.Context(), audit.ActionAdminAgentDisconnected,
		customerID, customerID, nil)

	w.WriteHeader(http.StatusNoContent)
}

// --- Helpers ---

// emitAudit emits a best-effort audit event for a mutating admin operation.
// target is the resource being acted on (port number or customer ID).
// No-op if the emitter was not configured.
func (a *AdminServer) emitAudit(ctx context.Context, action audit.Action, target, customerID string, metadata map[string]string) {
	if a.emitter == nil {
		return
	}
	//nolint:errcheck // best-effort audit
	a.emitter.Emit(ctx, audit.Event{
		Action:     action,
		Actor:      "admin-api",
		Target:     target,
		Timestamp:  time.Now(),
		CustomerID: customerID,
		Metadata:   metadata,
	})
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck // best-effort
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck // best-effort response
}
