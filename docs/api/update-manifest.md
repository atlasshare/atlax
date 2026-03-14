# Agent Update Manifest

The atlax agent supports self-updating: it periodically checks for new versions, downloads and verifies the update, replaces itself atomically, and rolls back if the new version crashes.

---

## VersionManifest JSON Schema

The relay (or a dedicated update server) hosts a signed JSON manifest describing the latest available version:

```json
{
  "version": "1.2.0",
  "release_date": "2026-03-14T00:00:00Z",
  "min_agent_version": "1.0.0",
  "changelog_url": "https://releases.atlasshare.io/atlax/v1.2.0/changelog.txt",
  "binaries": {
    "linux-amd64": {
      "url": "https://releases.atlasshare.io/atlax/v1.2.0/atlax-agent-linux-amd64",
      "sha256": "a1b2c3d4e5f6...64 hex characters...7890abcdef",
      "size": 15728640
    },
    "linux-arm64": {
      "url": "https://releases.atlasshare.io/atlax/v1.2.0/atlax-agent-linux-arm64",
      "sha256": "f0e1d2c3b4a5...64 hex characters...6789012345",
      "size": 14680064
    },
    "darwin-amd64": {
      "url": "https://releases.atlasshare.io/atlax/v1.2.0/atlax-agent-darwin-amd64",
      "sha256": "1234567890ab...64 hex characters...cdef012345",
      "size": 16252928
    },
    "darwin-arm64": {
      "url": "https://releases.atlasshare.io/atlax/v1.2.0/atlax-agent-darwin-arm64",
      "sha256": "abcdef012345...64 hex characters...6789abcdef",
      "size": 15204352
    }
  },
  "signature": "base64-encoded-ed25519-signature-of-the-manifest-without-this-field"
}
```

---

## Go Type Definitions

```go
// VersionManifest describes the latest available agent version and
// provides download URLs and integrity information for each platform.
type VersionManifest struct {
    // Version is the semantic version string (e.g., "1.2.0").
    Version string `json:"version"`

    // ReleaseDate is the ISO 8601 timestamp of the release.
    ReleaseDate string `json:"release_date"`

    // MinAgentVersion is the minimum agent version that can perform
    // an in-place update to this version. Agents older than this
    // must update through intermediate versions.
    MinAgentVersion string `json:"min_agent_version"`

    // ChangelogURL is a URL to the human-readable changelog.
    ChangelogURL string `json:"changelog_url"`

    // Binaries maps platform identifiers (e.g., "linux-amd64") to
    // their download information.
    Binaries map[string]BinaryInfo `json:"binaries"`

    // Signature is the base64-encoded ed25519 signature of the
    // manifest content (with the signature field itself set to an
    // empty string during signing).
    Signature string `json:"signature"`
}

// BinaryInfo describes a downloadable binary for a specific platform.
type BinaryInfo struct {
    // URL is the HTTPS download URL for the binary.
    URL string `json:"url"`

    // SHA256 is the hex-encoded SHA-256 hash of the binary file.
    SHA256 string `json:"sha256"`

    // Size is the expected file size in bytes.
    Size int64 `json:"size"`
}
```

---

## Update Check Flow

The agent checks for updates on a configurable interval (default: every 6 hours).

```
Agent                                 Update Server
  |                                        |
  |--- GET /v1/update/manifest ---------->|
  |                                        |
  |<-- 200 OK (VersionManifest JSON) -----|
  |                                        |
  | 1. Verify ed25519 signature            |
  | 2. Compare version to current          |
  | 3. If newer:                           |
  |    a. Select binary for GOOS/GOARCH    |
  |    b. Download binary via HTTPS        |
  |    c. Verify SHA-256 hash              |
  |    d. Verify file size                 |
  |    e. Replace binary atomically        |
  |    f. Restart                          |
  | 4. If same or older: no action         |
  |                                        |
```

### Step-by-Step Details

**Step 1: Fetch Manifest**

The agent sends an HTTPS GET request to the configured update endpoint. The request includes the agent's current version as a query parameter for server-side filtering (optional).

```
GET /v1/update/manifest?current=1.1.0&os=linux&arch=amd64
Host: releases.atlasshare.io
```

**Step 2: Verify Signature**

The agent verifies the manifest's ed25519 signature before trusting any fields:

1. Parse the JSON manifest.
2. Extract the `signature` field and set it to an empty string in the JSON.
3. Compute the ed25519 signature of the modified JSON using the public key embedded at compile time.
4. Compare the computed signature with the extracted signature.
5. If verification fails, log a warning and abort the update. Do not download anything.

**Step 3: Compare Versions**

Compare the manifest `version` with the agent's current version using semantic versioning:
- If the manifest version is newer, proceed with the update.
- If the manifest version is the same or older, no action.
- If the agent's current version is older than `min_agent_version`, log a warning (staged rollout may require intermediate updates).

**Step 4: Download Binary**

1. Look up the binary for the agent's `GOOS-GOARCH` in the `binaries` map.
2. Download the binary via HTTPS to a temporary file.
3. Verify the downloaded file's SHA-256 hash matches `BinaryInfo.SHA256`.
4. Verify the downloaded file's size matches `BinaryInfo.Size`.
5. If any verification fails, delete the temporary file and abort.

**Step 5: Atomic Replacement**

Replace the running binary without leaving a window where no binary exists:

1. Write the new binary to a temporary file in the same directory as the current binary.
2. Set executable permissions on the temporary file.
3. Rename the current binary to `atlax-agent.old` (backup).
4. Rename the temporary file to `atlax-agent` (atomic on POSIX via `rename(2)`).
5. Trigger restart (via `syscall.Exec` for self-exec, or exit and let systemd restart).

---

## Ed25519 Signature Verification

### Key Management

- The **signing private key** is stored in a secure environment (HSM, Vault, or encrypted storage) and is never present on the agent, relay, or build servers.
- The **verification public key** is embedded in the agent binary at compile time using `go:embed` or a build-time constant.

```go
// Embedded at compile time. Never changes without a new agent binary.
//
//go:embed update_pubkey.pem
var updatePublicKeyPEM []byte
```

### Signing Process (Release Pipeline)

1. Build the agent binary for all target platforms.
2. Compute SHA-256 hashes for each binary.
3. Construct the `VersionManifest` JSON with `signature` set to an empty string.
4. Sign the JSON bytes with the ed25519 private key.
5. Base64-encode the signature and insert it into the `signature` field.
6. Upload the manifest and binaries to the release server.

### Why Ed25519

| Property | Value |
|----------|-------|
| Key size | 32 bytes (public), 64 bytes (private) |
| Signature size | 64 bytes |
| Performance | Fast signing and verification |
| Security | 128-bit security level, immune to timing attacks |
| Simplicity | Single algorithm, no parameter choices |

---

## Download Security

### HTTPS Only

All binary downloads are over HTTPS. The agent rejects HTTP URLs in the manifest. The agent uses the system certificate store to verify the download server's TLS certificate.

### SHA-256 Verification

After downloading, the agent computes the SHA-256 hash of the file and compares it to the hash in the signed manifest. This provides integrity verification beyond the HTTPS transport:

- Protects against CDN or proxy corruption.
- Ensures the manifest and binary are consistent (the manifest was signed with the binary's hash).

### Size Verification

The agent verifies the downloaded file size matches `BinaryInfo.Size`. This is a quick sanity check before computing the SHA-256 hash, catching truncated downloads early.

---

## Atomic Replacement

The binary replacement must be atomic to prevent a state where no valid binary exists.

### POSIX (Linux, macOS)

```go
func atomicReplace(currentPath, newPath string) error {
    backupPath := currentPath + ".old"

    // Back up the current binary
    if err := os.Rename(currentPath, backupPath); err != nil {
        return fmt.Errorf("backup current binary: %w", err)
    }

    // Atomic rename of new binary into place
    if err := os.Rename(newPath, currentPath); err != nil {
        // Attempt to restore backup
        _ = os.Rename(backupPath, currentPath)
        return fmt.Errorf("replace binary: %w", err)
    }

    return nil
}
```

The `os.Rename` call on POSIX systems is atomic (it maps to `rename(2)`), so the binary path always points to a complete file.

---

## Rollback on Crash

If the agent crashes within 60 seconds of an update, it is considered a failed update and the previous version is restored.

### Mechanism

1. After replacing the binary and restarting, the agent records the update timestamp and previous binary path.
2. A watchdog timer starts counting from the restart.
3. If the agent process exits (crash, panic, or fatal error) within 60 seconds:
   - systemd detects the exit (via `Restart=on-failure`).
   - Before restarting, the agent's `ExecStartPre` script checks if a `.old` backup exists and if the last update was within 60 seconds.
   - If both conditions are met, the script restores the backup binary.
   - The restored (old) binary starts normally.
4. If the agent runs successfully for more than 60 seconds, the backup binary is deleted.

### systemd Integration

```ini
[Service]
ExecStartPre=/usr/local/bin/atlax-update-guard
ExecStart=/usr/local/bin/atlax-agent --config /etc/atlax/agent.yaml
Restart=on-failure
RestartSec=5
```

The `atlax-update-guard` script:

```bash
#!/bin/bash
BINARY="/usr/local/bin/atlax-agent"
BACKUP="${BINARY}.old"
TIMESTAMP_FILE="/var/lib/atlax/last-update-timestamp"

if [ -f "$BACKUP" ] && [ -f "$TIMESTAMP_FILE" ]; then
    last_update=$(cat "$TIMESTAMP_FILE")
    now=$(date +%s)
    elapsed=$((now - last_update))

    if [ "$elapsed" -lt 60 ]; then
        # Update was recent and agent crashed; roll back
        mv "$BACKUP" "$BINARY"
        rm -f "$TIMESTAMP_FILE"
        logger -t atlax "Rolled back agent update due to crash within 60 seconds"
    fi
fi
```

---

## Configuration

| Parameter | Default | Description |
|-----------|---------|-------------|
| `update_check_interval` | `6h` | How often to check for updates |
| `update_url` | `https://releases.atlasshare.io/v1/update/manifest` | Manifest endpoint |
| `update_enabled` | `true` | Enable/disable auto-updates |
| `rollback_window` | `60s` | Time window for crash-triggered rollback |

Auto-updates can be disabled for environments where updates are managed externally (e.g., configuration management tools, container image updates).
