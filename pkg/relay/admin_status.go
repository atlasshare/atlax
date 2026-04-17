package relay

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"time"
)

// StatusResponse is the JSON body returned by GET /status. It
// summarizes relay health, runtime counts, and cert expiry for
// operator dashboards and the `ats` CLI.
type StatusResponse struct {
	Status          string       `json:"status"`
	Uptime          string       `json:"uptime"`
	UptimeSeconds   float64      `json:"uptime_seconds"`
	AgentsConnected int          `json:"agents_connected"`
	StreamsActive   int          `json:"streams_active"`
	PortsActive     int          `json:"ports_active"`
	ConfigVersion   string       `json:"config_version"`
	RelayCerts      []CertExpiry `json:"relay_certs"`
	AgentCerts      []CertExpiry `json:"agent_certs"`
}

// CertExpiry describes when a single relay-side certificate expires.
// ExpiresAt is RFC3339-formatted. DaysLeft may be zero or negative
// for expired certs so operators can alert on them.
type CertExpiry struct {
	Name      string `json:"name"`
	ExpiresAt string `json:"expires_at"`
	DaysLeft  int    `json:"days_left"`
}

// CertNamePath pairs a display name with a PEM file path. Admin
// server takes these at construction and re-reads the files on every
// /status call; there is no in-memory cache.
type CertNamePath struct {
	Name string
	Path string
}

// handleStatus returns a point-in-time snapshot of relay health,
// runtime counts, and cert expiry. GET only. Always returns 200
// with a "ok" status for now; readiness checks may downgrade to
// "degraded" in future revisions.
func (a *AdminServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agents, err := a.registry.ListConnectedAgents(r.Context())
	if err != nil {
		writeError(w, "internal", http.StatusInternalServerError)
		return
	}

	totalStreams := 0
	for i := range agents {
		totalStreams += agents[i].StreamCount
	}

	ports := a.router.ListPorts()
	uptime := time.Since(a.startTime)
	relayCerts := a.collectRelayCerts()
	agentCerts := collectAgentCerts(agents)

	writeJSON(w, StatusResponse{
		Status:          "ok",
		Uptime:          uptime.Round(time.Second).String(),
		UptimeSeconds:   uptime.Seconds(),
		AgentsConnected: len(agents),
		StreamsActive:   totalStreams,
		PortsActive:     len(ports),
		ConfigVersion:   a.configVersion,
		RelayCerts:      relayCerts,
		AgentCerts:      agentCerts,
	})
}

// collectRelayCerts reads each configured cert path, parses its
// NotAfter, and returns a freshly-allocated slice. Missing or
// malformed files are logged at warn level and omitted from the
// response so a misconfigured cert path cannot take the whole
// /status endpoint offline.
//
// The slice is always non-nil so json.Marshal emits "[]" rather
// than "null".
func (a *AdminServer) collectRelayCerts() []CertExpiry {
	out := make([]CertExpiry, 0, len(a.certPaths))
	for _, cp := range a.certPaths {
		entry, err := parseCertExpiry(cp.Name, cp.Path)
		if err != nil {
			a.logger.Warn("admin: skip cert expiry entry",
				"name", cp.Name, "path", cp.Path, "error", err)
			continue
		}
		out = append(out, entry)
	}
	return out
}

// collectAgentCerts builds a CertExpiry entry for each connected agent
// whose CertNotAfter is non-zero. The slice is always non-nil.
func collectAgentCerts(agents []AgentInfo) []CertExpiry {
	out := make([]CertExpiry, 0, len(agents))
	for i := range agents {
		ag := &agents[i]
		if ag.CertNotAfter.IsZero() {
			continue
		}
		now := time.Now()
		daysLeft := int(ag.CertNotAfter.Sub(now).Hours() / 24)
		out = append(out, CertExpiry{
			Name:      ag.CustomerID,
			ExpiresAt: ag.CertNotAfter.UTC().Format(time.RFC3339),
			DaysLeft:  daysLeft,
		})
	}
	return out
}

// parseCertExpiry reads a PEM file and extracts the NotAfter of the
// first CERTIFICATE block. Returns an error if the file cannot be
// read, does not contain a PEM CERTIFICATE block, or the block
// bytes do not parse as an x509 certificate. Callers are expected
// to tolerate these errors and skip the entry.
func parseCertExpiry(name, path string) (CertExpiry, error) {
	data, err := os.ReadFile(path) //nolint:gosec // trusted operator config, not user input
	if err != nil {
		return CertExpiry{}, fmt.Errorf("read cert %q: %w", path, err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return CertExpiry{}, fmt.Errorf("cert %q: no PEM block found", path)
	}
	if block.Type != "CERTIFICATE" {
		return CertExpiry{}, fmt.Errorf("cert %q: expected CERTIFICATE PEM type, got %q", path, block.Type)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return CertExpiry{}, fmt.Errorf("parse cert %q: %w", path, err)
	}

	now := time.Now()
	notAfter := cert.NotAfter
	// Truncate to whole days. For already-expired certs, this goes
	// negative -- intentional, so dashboards can surface them.
	daysLeft := int(notAfter.Sub(now).Hours() / 24)
	return CertExpiry{
		Name:      name,
		ExpiresAt: notAfter.UTC().Format(time.RFC3339),
		DaysLeft:  daysLeft,
	}, nil
}
