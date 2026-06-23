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
frontend-passenger/
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

## Passenger Frontend

Passenger-side test frontend lives in `frontend-passenger/`.

Before running it with Gaode Map, create `frontend-passenger/.env.local`:

```powershell
VITE_AMAP_KEY=your_web_jsapi_key
VITE_AMAP_SECURITY_JS_CODE=your_security_js_code
VITE_AMAP_MAP_STYLE=amap://styles/normal
```

Run it with:

```powershell
cd frontend-passenger
npm install
npm run dev
```

Vite proxies `/api` and `/ws` to `http://127.0.0.1:8080`.

## Driver Simulator

Use the driver simulator instead of a driver frontend:

```powershell
cd backend
go run ./cmd/driver-sim -drivers 2
```

It will:

- auto-register/login test driver accounts
- set drivers online
- upload UDP locations periodically
- poll dispatches and auto-accept orders
- optionally auto-progress accepted orders
