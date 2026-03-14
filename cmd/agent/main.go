package main

// atlax-agent is the tunnel agent that runs on customer nodes, establishes
// outbound TLS connections to the relay, and forwards local service traffic.
//
// Intended imports (uncomment during implementation):
// "github.com/atlasshare/atlax/internal/config"
// "github.com/atlasshare/atlax/pkg/auth"
// "github.com/atlasshare/atlax/pkg/agent"

func main() {
	// TODO: implement agent startup sequence
	//
	// 1. Parse command-line flags and load configuration
	// 2. Initialize structured logger (slog)
	// 3. Load TLS certificates and create mTLS configuration
	// 4. Create tunnel client
	// 5. Connect to relay with exponential backoff and jitter
	// 6. Register signal handlers (SIGINT, SIGTERM)
	// 7. On shutdown signal: close streams gracefully
	// 8. Disconnect from relay
}
