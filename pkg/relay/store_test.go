package relay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSidecarStore_LoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	s := NewSidecarStore(filepath.Join(dir, "sidecar.json"))

	data, err := s.Load()

	require.NoError(t, err)
	assert.Equal(t, sidecarVersion, data.Version)
	assert.Empty(t, data.Ports)
}

func TestSidecarStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewSidecarStore(filepath.Join(dir, "sidecar.json"))

	want := &SidecarData{
		Version: sidecarVersion,
		Ports: []SidecarPort{
			{Port: 8080, CustomerID: "cust-a", Service: "http", ListenAddr: "0.0.0.0", MaxStreams: 10},
			{Port: 4433, CustomerID: "cust-b", Service: "smb", ListenAddr: "127.0.0.1", MaxStreams: 5},
		},
	}

	require.NoError(t, s.Save(want))

	got, err := s.Load()
	require.NoError(t, err)
	assert.Equal(t, want.Version, got.Version)
	assert.Equal(t, len(want.Ports), len(got.Ports))

	// Build a map for order-independent comparison.
	byPort := make(map[int]SidecarPort, len(got.Ports))
	for _, sp := range got.Ports {
		byPort[sp.Port] = sp
	}
	for _, sp := range want.Ports {
		assert.Equal(t, sp, byPort[sp.Port])
	}
}

func TestSidecarStore_AtomicWrite_NoTmpFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar.json")
	s := NewSidecarStore(path)

	data := &SidecarData{
		Version: sidecarVersion,
		Ports:   []SidecarPort{{Port: 9000, CustomerID: "cust-c", Service: "api", ListenAddr: "0.0.0.0"}},
	}

	require.NoError(t, s.Save(data))

	_, err := os.Stat(path + ".tmp")
	assert.True(t, os.IsNotExist(err), "temp file must not exist after successful Save")
}

func TestSidecarStore_SaveCurrentState(t *testing.T) {
	dir := t.TempDir()
	s := NewSidecarStore(filepath.Join(dir, "sidecar.json"))

	ports := []PortInfo{
		{Port: 7070, CustomerID: "cust-d", Service: "dash", ListenAddr: "0.0.0.0", MaxStreams: 20},
		{Port: 7071, CustomerID: "cust-d", Service: "dash-api", ListenAddr: "0.0.0.0", MaxStreams: 20},
	}

	require.NoError(t, s.SaveCurrentState(ports))

	got, err := s.Load()
	require.NoError(t, err)
	assert.Equal(t, sidecarVersion, got.Version)
	assert.Len(t, got.Ports, 2)

	byPort := make(map[int]SidecarPort, len(got.Ports))
	for _, sp := range got.Ports {
		byPort[sp.Port] = sp
	}

	assert.Equal(t, "cust-d", byPort[7070].CustomerID)
	assert.Equal(t, "dash", byPort[7070].Service)
	assert.Equal(t, 20, byPort[7070].MaxStreams)
	assert.Equal(t, "dash-api", byPort[7071].Service)
}

func TestSidecarStore_SaveCurrentState_Empty(t *testing.T) {
	dir := t.TempDir()
	s := NewSidecarStore(filepath.Join(dir, "sidecar.json"))

	require.NoError(t, s.SaveCurrentState(nil))

	got, err := s.Load()
	require.NoError(t, err)
	assert.Empty(t, got.Ports)
}

func TestSidecarStore_LoadCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar.json")
	require.NoError(t, os.WriteFile(path, []byte("not valid json {{{"), 0o600))

	s := NewSidecarStore(path)
	_, err := s.Load()
	assert.Error(t, err)
}

func TestSidecarStore_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar.json")
	s := NewSidecarStore(path)

	require.NoError(t, s.Save(&SidecarData{Version: sidecarVersion, Ports: []SidecarPort{}}))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestSidecarStore_SaveCurrentState_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewSidecarStore(filepath.Join(dir, "sidecar.json"))

	// Simulate create then delete cycle.
	ports := []PortInfo{
		{Port: 5000, CustomerID: "cust-e", Service: "svc1", ListenAddr: "0.0.0.0", MaxStreams: 0},
	}
	require.NoError(t, s.SaveCurrentState(ports))

	// Simulate delete: save empty state.
	require.NoError(t, s.SaveCurrentState([]PortInfo{}))

	got, err := s.Load()
	require.NoError(t, err)
	assert.Empty(t, got.Ports)
}

// TestSidecarData_JSONSchema verifies the JSON field names match the spec.
func TestSidecarData_JSONSchema(t *testing.T) {
	data := SidecarData{
		Version: "1",
		Ports: []SidecarPort{
			{Port: 1234, CustomerID: "c1", Service: "s1", ListenAddr: "0.0.0.0", MaxStreams: 3},
		},
	}

	b, err := json.Marshal(data)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))

	assert.Contains(t, m, "version")
	assert.Contains(t, m, "ports")

	ports, ok := m["ports"].([]any)
	require.True(t, ok)
	require.Len(t, ports, 1)

	entry, ok := ports[0].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, entry, "port")
	assert.Contains(t, entry, "customer_id")
	assert.Contains(t, entry, "service")
	assert.Contains(t, entry, "listen_addr")
	assert.Contains(t, entry, "max_streams")
}
