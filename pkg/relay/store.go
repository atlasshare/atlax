package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
func (s *SidecarStore) Load() (*SidecarData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.path)
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
	if data.Ports == nil {
		data.Ports = []SidecarPort{}
	}
	return &data, nil
}

// Save atomically writes data to the sidecar file. It writes to a
// temporary file first, then renames it into place.
func (s *SidecarStore) Save(data *SidecarData) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("sidecar: marshal: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("sidecar: write temp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp) //nolint:errcheck // best-effort cleanup on rename failure
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
