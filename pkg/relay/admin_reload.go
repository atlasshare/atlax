package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/atlasshare/atlax/pkg/audit"
	"github.com/atlasshare/atlax/pkg/config"
)

// ReloadSummary is the JSON body returned by POST /reload and produced
// by AdminServer.Reload. Counts are per-operation totals against the
// previously-applied config.
type ReloadSummary struct {
	PortsAdded        int      `json:"ports_added"`
	PortsRemoved      int      `json:"ports_removed"`
	PortsUpdated      int      `json:"ports_updated"`
	PortsRejected     int      `json:"ports_rejected"`
	RateLimitsChanged int      `json:"rate_limits_changed"`
	RestartRequired   []string `json:"restart_required"`
}

// handleConfig serves the currently-applied relay configuration. The
// response is a defensive JSON copy of AdminServer.currentCfg; callers
// cannot mutate the server's view by editing the response body.
//
// No redaction is performed: RelayConfig contains paths, ports,
// customer IDs, and rate limits -- operational data, never key
// material. If secret-bearing fields are added in future they must
// be scrubbed here explicitly.
func (a *AdminServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	a.cfgMu.RLock()
	cfg := a.currentCfg
	a.cfgMu.RUnlock()

	if cfg == nil {
		// No initial config was seeded. This only happens when the admin
		// server was constructed without InitialConfig; expose a clean
		// empty object rather than "null" to avoid surprises.
		writeJSON(w, config.RelayConfig{})
		return
	}

	// Defensive deep copy: marshal a fresh value derived from the snapshot
	// taken under the read lock. We intentionally do NOT hand json.Encoder
	// the shared pointer so that a subsequent Reload() cannot observe or
	// race with the encoding goroutine.
	snapshot := *cfg
	writeJSON(w, snapshot)
}

// handleReload is the POST /reload entry point. Thin wrapper over
// AdminServer.Reload so operators see a consistent 200/422 behavior
// and SIGHUP callers can use the same engine.
func (a *AdminServer) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	summary, err := a.Reload(r.Context())
	if err != nil {
		writeError(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, summary)
}

// Reload re-reads the config from a.configPath and reconciles the
// in-memory router/listener/rate-limit state against it. Mutating a
// port's customer_id via reload is rejected: the tenant boundary
// established at port creation is immutable, and cross-tenant routing
// must never change via a config file edit.
//
// Reload serializes itself under a.reloadMu so concurrent SIGHUPs and
// POST /reload callers see a consistent summary. The method never
// mutates a.currentCfg on parse or validate errors -- the router and
// listener state is left untouched, and the caller receives the
// loader error for operator diagnostics.
func (a *AdminServer) Reload(ctx context.Context) (ReloadSummary, error) {
	// Serialize reloads. SIGHUP floods and concurrent admin calls must
	// not observe a partially-applied state.
	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()

	if a.configPath == "" {
		return ReloadSummary{}, fmt.Errorf("reload: config_path not configured; reload unavailable")
	}

	loader := config.NewFileLoader()
	newCfg, err := loader.LoadRelayConfig(a.configPath)
	if err != nil {
		// Parse or validate failure. State is unchanged by contract.
		a.logger.Error("admin: reload failed; current config retained",
			"path", a.configPath, "error", err)
		return ReloadSummary{}, err
	}

	a.cfgMu.Lock()
	oldCfg := a.currentCfg
	a.cfgMu.Unlock()

	summary := a.applyReload(ctx, oldCfg, newCfg)

	// Commit the new config under the write lock. GET /config
	// henceforth reflects this snapshot.
	a.cfgMu.Lock()
	a.currentCfg = newCfg
	a.cfgMu.Unlock()

	// Persist the router's port snapshot so the sidecar stays consistent
	// with the live state. Failure is logged, not fatal: the reload has
	// already taken effect in memory.
	if a.store != nil {
		if saveErr := a.store.SaveCurrentState(a.router.ListPorts()); saveErr != nil {
			a.logger.Warn("admin: reload: sidecar save failed",
				"path", a.configPath, "error", saveErr)
		}
	}

	a.emitReloadAudit(ctx, summary)

	a.logger.Info("admin: reload applied",
		"path", a.configPath,
		"ports_added", summary.PortsAdded,
		"ports_removed", summary.PortsRemoved,
		"ports_updated", summary.PortsUpdated,
		"ports_rejected", summary.PortsRejected,
		"rate_limits_changed", summary.RateLimitsChanged,
		"restart_required", summary.RestartRequired)

	return summary, nil
}

// applyReload performs the actual diff between oldCfg and newCfg and
// reconciles router/listener/rate-limiter state. It does NOT take
// a.cfgMu; the caller is expected to hold a.reloadMu for the duration
// so concurrent reloads serialize on a single diff at a time.
func (a *AdminServer) applyReload(ctx context.Context, oldCfg, newCfg *config.RelayConfig) ReloadSummary {
	oldIdx := indexPorts(oldCfg)
	newIdx := indexPorts(newCfg)

	summary := ReloadSummary{RestartRequired: []string{}}

	// Process each port in the new config.
	for port, newEntry := range newIdx {
		oldEntry, existed := oldIdx[port]
		switch {
		case !existed:
			a.addPort(ctx, port, newEntry, &summary)
		case oldEntry.CustomerID != newEntry.CustomerID:
			a.rejectCustomerChange(port, oldEntry, newEntry, &summary)
		case portMutableFieldsDiffer(oldEntry, newEntry):
			a.updatePortReload(port, newEntry, &summary)
		}
	}

	// Process removals: ports present in old, gone from new.
	for port, oldEntry := range oldIdx {
		if _, stillPresent := newIdx[port]; !stillPresent {
			a.removePort(port, oldEntry, &summary)
		}
	}

	// Rate limit reconciliation.
	summary.RateLimitsChanged = a.applyRateLimitChanges(oldCfg, newCfg)

	// Restart-required fields (TLS paths, server listen addr, agent listen addr).
	summary.RestartRequired = diffRestartRequired(oldCfg, newCfg)
	if len(summary.RestartRequired) > 0 {
		a.logger.Warn("admin: reload: restart_required fields changed; new values ignored until process restart",
			"fields", summary.RestartRequired)
	}

	return summary
}

// addPort starts a listener and registers a new port mapping during
// reload. Errors are logged but never propagated up; one bad port in a
// large config must not stop the whole reload.
func (a *AdminServer) addPort(ctx context.Context, port int, entry config.PortIndexEntry, summary *ReloadSummary) {
	if err := a.router.AddPortMapping(entry.CustomerID, port, entry.Service, entry.ListenAddr, entry.MaxStreams); err != nil {
		a.logger.Error("admin: reload: add port mapping failed",
			"port", port, "customer_id", entry.CustomerID, "error", err)
		return
	}

	addr := fmt.Sprintf("%s:%d", entry.ListenAddr, port)
	listenerCtx := a.ctx
	if listenerCtx == nil {
		listenerCtx = ctx
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.clientListener.StartPort(listenerCtx, addr, port)
	}()

	// Brief wait to detect immediate bind failure.
	select {
	case err := <-errCh:
		a.router.RemovePortMapping(entry.CustomerID, port) //nolint:errcheck // rollback best-effort
		a.logger.Error("admin: reload: listener start failed; rolling back mapping",
			"port", port, "addr", addr, "error", err)
		return
	case <-time.After(50 * time.Millisecond):
	}

	summary.PortsAdded++
	a.logger.Info("admin: reload: port added",
		"port", port, "customer_id", entry.CustomerID, "service", entry.Service)
}

// removePort stops the listener and drops the port mapping during
// reload. Errors are logged but do not halt the reload.
func (a *AdminServer) removePort(port int, entry config.PortIndexEntry, summary *ReloadSummary) {
	if err := a.router.RemovePortMapping(entry.CustomerID, port); err != nil {
		a.logger.Error("admin: reload: remove port mapping failed",
			"port", port, "customer_id", entry.CustomerID, "error", err)
		return
	}
	if err := a.clientListener.StopPort(port); err != nil {
		a.logger.Warn("admin: reload: stop port listener failed",
			"port", port, "error", err)
	}
	summary.PortsRemoved++
	a.logger.Info("admin: reload: port removed",
		"port", port, "customer_id", entry.CustomerID)
}

// updatePortReload applies mutable-field changes (service, listen_addr,
// max_streams) to an existing port. customerID is NEVER passed; the
// router's UpdatePortMapping preserves it verbatim.
func (a *AdminServer) updatePortReload(port int, entry config.PortIndexEntry, summary *ReloadSummary) {
	if err := a.router.UpdatePortMapping(port, entry.Service, entry.ListenAddr, entry.MaxStreams); err != nil {
		a.logger.Error("admin: reload: update port mapping failed",
			"port", port, "error", err)
		return
	}
	summary.PortsUpdated++
	a.logger.Info("admin: reload: port updated",
		"port", port, "service", entry.Service,
		"listen_addr", entry.ListenAddr, "max_streams", entry.MaxStreams)
}

// rejectCustomerChange refuses a reload attempt that would move a port
// from one customer to another. This is the security invariant that
// makes reload safe to expose over the admin API: operators cannot
// redraw the tenant boundary by editing YAML.
func (a *AdminServer) rejectCustomerChange(port int, oldEntry, newEntry config.PortIndexEntry, summary *ReloadSummary) {
	summary.PortsRejected++
	a.logger.Error("admin: reload: customer_id_immutable; port change rejected",
		"port", port,
		"current_customer_id", oldEntry.CustomerID,
		"attempted_customer_id", newEntry.CustomerID,
		"reason", "tenant binding is immutable via reload; cross-tenant routing cannot change this way")
}

// applyRateLimitChanges compares per-customer rate_limit blocks between
// old and new configs. Returns the number of customers whose limiter
// was reconfigured. A customer whose rate_limit was previously 0 and
// is now >0 counts as changed; the reverse is a no-op at the moment
// because SetRateLimiter only accepts positive rps.
func (a *AdminServer) applyRateLimitChanges(oldCfg, newCfg *config.RelayConfig) int {
	oldLimits := rateLimits(oldCfg)
	newLimits := rateLimits(newCfg)

	changed := 0
	for customerID, newLim := range newLimits {
		oldLim, hadLimit := oldLimits[customerID]
		if hadLimit && rateLimitEqual(oldLim, newLim) {
			continue
		}
		if newLim.RequestsPerSecond > 0 {
			a.clientListener.SetRateLimiter(customerID, newLim.RequestsPerSecond, newLim.Burst)
			changed++
			a.logger.Info("admin: reload: rate limit updated",
				"customer_id", customerID,
				"requests_per_second", newLim.RequestsPerSecond,
				"burst", newLim.Burst)
		}
	}
	return changed
}

// emitReloadAudit records the reload event with enough detail for a
// SIEM pipeline to reconstruct the diff. Summary counts go in the
// metadata. No PII; customer IDs are already considered non-secret in
// this codebase (logged on every port operation).
func (a *AdminServer) emitReloadAudit(ctx context.Context, summary ReloadSummary) {
	if a.emitter == nil {
		return
	}
	restartJoined := ""
	if len(summary.RestartRequired) > 0 {
		restartJoined = joinSortedStrings(summary.RestartRequired)
	}
	a.emitter.Emit(ctx, audit.Event{ //nolint:errcheck // best-effort audit
		Action:    audit.ActionAdminReload,
		Actor:     "admin-api",
		Target:    a.configPath,
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"ports_added":         fmt.Sprintf("%d", summary.PortsAdded),
			"ports_removed":       fmt.Sprintf("%d", summary.PortsRemoved),
			"ports_updated":       fmt.Sprintf("%d", summary.PortsUpdated),
			"ports_rejected":      fmt.Sprintf("%d", summary.PortsRejected),
			"rate_limits_changed": fmt.Sprintf("%d", summary.RateLimitsChanged),
			"restart_required":    restartJoined,
		},
	})
}

// --- Helpers ---

// indexPorts flattens a RelayConfig's customer/ports tree into a
// port->entry map suitable for diffing. nil cfg maps to an empty
// index so the caller can use the same code paths for initial boot.
func indexPorts(cfg *config.RelayConfig) map[int]config.PortIndexEntry {
	out := make(map[int]config.PortIndexEntry)
	if cfg == nil {
		return out
	}
	for _, c := range cfg.Customers {
		for _, p := range c.Ports {
			listenAddr := p.ListenAddr
			if listenAddr == "" {
				listenAddr = "0.0.0.0"
			}
			out[p.Port] = config.PortIndexEntry{
				CustomerID: c.ID,
				Service:    p.Service,
				MaxStreams: c.MaxStreams,
				ListenAddr: listenAddr,
				RateLimit:  c.RateLimit,
			}
		}
	}
	return out
}

func portMutableFieldsDiffer(a, b config.PortIndexEntry) bool {
	return a.Service != b.Service || a.ListenAddr != b.ListenAddr || a.MaxStreams != b.MaxStreams
}

func rateLimitEqual(a, b config.RateLimitConfig) bool {
	return a.RequestsPerSecond == b.RequestsPerSecond && a.Burst == b.Burst
}

// rateLimits extracts the per-customer rate limit map from a config
// snapshot. Customers without a rate_limit block are not in the
// result; the caller must treat "absent" as "no limit".
func rateLimits(cfg *config.RelayConfig) map[string]config.RateLimitConfig {
	out := make(map[string]config.RateLimitConfig)
	if cfg == nil {
		return out
	}
	for _, c := range cfg.Customers {
		out[c.ID] = c.RateLimit
	}
	return out
}

// diffRestartRequired reports which fields changed between old and
// new config but cannot be applied without a process restart. The
// result is sorted for deterministic JSON output.
func diffRestartRequired(oldCfg, newCfg *config.RelayConfig) []string {
	if oldCfg == nil || newCfg == nil {
		return []string{}
	}
	out := []string{}
	if oldCfg.TLS.CertFile != newCfg.TLS.CertFile {
		out = append(out, "tls.cert_file")
	}
	if oldCfg.TLS.KeyFile != newCfg.TLS.KeyFile {
		out = append(out, "tls.key_file")
	}
	if oldCfg.TLS.CAFile != newCfg.TLS.CAFile {
		out = append(out, "tls.ca_file")
	}
	if oldCfg.TLS.ClientCAFile != newCfg.TLS.ClientCAFile {
		out = append(out, "tls.client_ca_file")
	}
	if oldCfg.Server.ListenAddr != newCfg.Server.ListenAddr {
		out = append(out, "server.listen_addr")
	}
	if oldCfg.Server.AgentListenAddr != newCfg.Server.AgentListenAddr {
		out = append(out, "server.agent_listen_addr")
	}
	sort.Strings(out)
	return out
}

// joinSortedStrings deterministically joins a slice of strings with ","
// so the audit metadata is stable across runs. Marshaling a sorted
// []string cannot fail, so the error is intentionally discarded; if it
// ever does, an empty string is the safe fallback.
func joinSortedStrings(s []string) string {
	cp := make([]string, len(s))
	copy(cp, s)
	sort.Strings(cp)
	b, err := json.Marshal(cp)
	if err != nil {
		return ""
	}
	return string(b)
}
