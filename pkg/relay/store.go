package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const sidecarVersion = "1"

// SidecarPort is a single port mapping persisted in the sidecar JSON file.
type SidecarPort struct {
	Port       int    `json:"port"`
	CustomerID string `json:"customer_id"`
	Service    string `json:"service"`
	ListenAddr string `json:"listen_addr"`
	MaxStreams  int    `json:"max_streams"`
}

// SidecarData is the JSON payload written to and read from the sidecar file.
type SidecarData struct {
	Version string        `json:"version"`
	Ports   []SidecarPort `json:"ports"`
}

// SidecarStore persists runtime port mutations to a JSON file.
// On relay startup the sidecar is merged into the port index so that
// ports added via the admin API survive process restarts without
// requiring relay.yaml to be modified.
type SidecarStore struct {
	path string
	mu   sync.Mutex
}

// NewSidecarStore returns a SidecarStore backed by the file at path.
func NewSidecarStore(path string) *SidecarStore {
	return &SidecarStore{path: path}
}

// Load reads and parses the sidecar file. If the file does not exist,
// an empty SidecarData is returned without error. Any other read or
// parse error is returned to the caller.
//
// The mutex is held only for the file read; unmarshaling happens outside
// the lock to keep the critical section short.
func (s *SidecarStore) Load() (*SidecarData, error) {
	s.mu.Lock()
	b, err := os.ReadFile(s.path)
	s.mu.Unlock()

	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &SidecarData{Version: sidecarVersion, Ports: []SidecarPort{}}, nil
		}
		return nil, err
	}

	var data SidecarData
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, fmt.Errorf("sidecar: parse %s: %w", s.path, err)
	}
	if data.Version != sidecarVersion {
		return nil, fmt.Errorf("sidecar: unsupported version %q (want %q)", data.Version, sidecarVersion)
	}
	if data.Ports == nil {
		data.Ports = []SidecarPort{}
	}
	return &data, nil
}

// Save atomically writes data to the sidecar file. It creates a temp file
// with a random suffix in the same directory (guaranteeing same-filesystem
// rename), then renames it into place.
func (s *SidecarStore) Save(data *SidecarData) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("sidecar: marshal: %w", err)
	}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".sidecar-*.tmp")
	if err != nil {
		return fmt.Errorf("sidecar: create temp: %w", err)
	}
	tmpName := tmp.Name()

	_, writeErr := tmp.Write(b)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(tmpName) //nolint:errcheck // best-effort cleanup
		if writeErr != nil {
			return fmt.Errorf("sidecar: write temp: %w", writeErr)
		}
		return fmt.Errorf("sidecar: close temp: %w", closeErr)
	}

	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName) //nolint:errcheck // best-effort cleanup on rename failure
		return fmt.Errorf("sidecar: rename: %w", err)
	}
	return nil
}

// SaveCurrentState converts a PortInfo snapshot to SidecarData and saves
// it atomically. Call after createPort or deletePort succeeds.
func (s *SidecarStore) SaveCurrentState(ports []PortInfo) error {
	sps := make([]SidecarPort, len(ports))
	for i, p := range ports {
		sps[i] = SidecarPort{
			Port:       p.Port,
			CustomerID: p.CustomerID,
			Service:    p.Service,
			ListenAddr: p.ListenAddr,
			MaxStreams:  p.MaxStreams,
		}
	}
	return s.Save(&SidecarData{Version: sidecarVersion, Ports: sps})
}
