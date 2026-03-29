# Prerequisite: Verify Dev Certificates

## Why

Phase 3 integration tests require a full mTLS handshake between relay and agent. The relay needs the relay cert (server-side) and customer CA (client verification). The agent needs the agent cert (client-side) and relay CA (server verification).

## Steps

```bash
cd ~/projects/atlax
ls certs/relay.crt certs/relay.key certs/customer-ca.crt certs/agent.crt certs/agent.key certs/relay-ca.crt
```

If any are missing: `make certs-dev`

Verify chains:
```bash
openssl verify -CAfile certs/root-ca.crt -untrusted certs/relay-ca.crt certs/relay.crt
openssl verify -CAfile certs/root-ca.crt -untrusted certs/customer-ca.crt certs/agent.crt
```

Both should output `OK`.

## Done When

- All cert files present
- Both verification chains pass
