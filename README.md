# Uber_test

A Go-based, front-end/back-end separated taxi dispatch system inspired by Uber's realtime map experience.

## Current Scope

- Go backend scaffold
- HTTP health and location query endpoints
- UDP location ingress
- WebSocket realtime push hub
- Project design documentation

## Structure

```text
docs/
backend/
```

## Quick Start

```powershell
cd backend
go run ./cmd/server
```

## Protobuf

The UDP location uplink uses protobuf.

Protocol file:

- `backend/proto/location/v1/location.proto`

Regenerate Go code:

```powershell
cd backend
.\scripts\generate-proto.ps1
```

Supported ingress packet types:

- `location_update`
- `heartbeat`
- `location_batch`

Each UDP payload should be encoded as `LocationIngressPacket`.

## Redis

Location storage supports a two-layer mode:

- in-memory hot cache
- optional Redis shared hot data store

Enable Redis with env vars in `backend/configs/config.example.env`.
