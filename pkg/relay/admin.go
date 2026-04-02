package relay

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// AdminServer serves health check and Prometheus metrics endpoints.
type AdminServer struct {
	registry AgentRegistry
	logger   *slog.Logger
	server   *http.Server
}

// HealthResponse is the JSON body returned by /healthz.
type HealthResponse struct {
	Status  string `json:"status"`
	Agents  int    `json:"agents"`
	Streams int    `json:"streams"`
}

// NewAdminServer creates an admin HTTP server on the given address.
func NewAdminServer(addr string, registry AgentRegistry, logger *slog.Logger) *AdminServer {
	a := &AdminServer{
		registry: registry,
		logger:   logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", a.handleHealth)
	mux.Handle("/metrics", promhttp.Handler())

	a.server = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return a
}

// Start begins serving. Blocks until ctx is canceled.
func (a *AdminServer) Start(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		a.server.Close()
	}()

	a.logger.Info("relay: admin server started", "addr", a.server.Addr)
	if err := a.server.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

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

	resp := HealthResponse{
		Status:  "ok",
		Agents:  len(agents),
		Streams: totalStreams,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck // best-effort response
}
