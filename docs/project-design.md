# Uber-like Taxi Project Design

## 1. Project Positioning

This project is a Go-based, front-end/back-end separated taxi dispatch system inspired by Uber's real-time map experience.

Core goal:

- Let passengers see nearby drivers and real-time vehicle movement on a map.
- Support the full basic business loop from login to order completion.
- Build a scalable real-time location capability for later dispatch and pricing improvements.

Recommended MVP boundary:

- Passenger app/web
- Driver app/web
- Admin backend
- Go backend APIs
- Real-time location service
- Order and trip management
- Basic pricing and payment status flow

Not in MVP:

- Full payment gateway settlement
- Coupon system
- Intelligent dispatch algorithm
- Dynamic surge pricing
- Large-scale message push platform
- Complex multi-city operations and RBAC

## 2. Business Roles

### 2.1 Passenger

- Register/login
- Set pickup and destination
- View nearby drivers on map
- Create ride order
- View driver assignment and trip status
- View trip trajectory and fee
- Complete payment
- View order history

### 2.2 Driver

- Register/login/certification
- Maintain online/offline status
- Upload real-time location
- Receive and accept/reject orders
- Start trip / arrive pickup / complete trip
- View current order and income

### 2.3 Admin

- Manage users/drivers/vehicles
- Review driver qualification
- View order and trip records
- View driver online status and location distribution
- Handle abnormal orders / complaints / manual closure

## 3. Core Business Chain

### 3.1 Passenger Ride Flow

1. Passenger logs in.
2. Front end gets current location and destination.
3. Front end requests nearby drivers and draws them on the map.
4. Passenger creates an order.
5. Backend creates the order in `pending_dispatch`.
6. Dispatch service selects candidate drivers.
7. Driver receives order and accepts it.
8. Order becomes `accepted`.
9. Driver moves toward pickup point and keeps reporting location.
10. Passenger sees driver moving in real time.
11. Driver arrives, then starts trip.
12. During trip, backend continuously records route points and estimated fee.
13. Driver completes trip.
14. Backend calculates final fee and marks order `to_be_paid` or `paid`.
15. Passenger views trip result and history.

### 3.2 Driver Location Flow

1. Driver goes online.
2. Driver client starts high-frequency location reporting, recommended every 1 to 3 seconds.
3. UDP ingress receives Protobuf-encoded location packets.
4. Real-time location service updates driver latest position in memory/Redis cache.
5. Spatial index is updated for nearby search.
6. Recent location points are appended to trip trajectory cache/store.
7. If driver is in an active trip, trip route and fee snapshot are updated.
8. If timeout is reached, driver is marked inactive/offline.

### 3.3 Dispatch and Map Flow

1. Passenger opens home page.
2. Front end pulls nearby drivers and order data from HTTP API.
3. Front end subscribes to order/driver real-time updates through WebSocket.
4. After order acceptance, front end receives driver movement updates.
5. Front end animates marker movement from backend-provided coordinates.

## 4. Functional List

## 4.1 MVP Functions

### User and Account

- Passenger registration/login
- Driver registration/login
- JWT-based authentication
- Driver profile and certification status
- Basic admin login

### Map and Location

- Passenger current location acquisition
- Driver high-frequency location upload
- Nearby drivers query
- Real-time driver movement playback
- Driver online/offline state management

### Order

- Create order
- Cancel order
- Dispatch order to nearby drivers
- Driver accept/reject order
- Order status transition management
- Order detail query
- Order list/history

### Trip

- Driver arrive pickup
- Start trip
- End trip
- Store trip trajectory
- Estimate and settle trip fee

### Driver

- Driver vehicle binding
- Driver work status management
- Current order dashboard
- Income summary

### Admin

- User management
- Driver management
- Vehicle management
- Order/trip monitoring
- Driver certification review

## 4.2 Phase 2 Enhancements

- Route correction based on map service
- Smarter dispatch scoring
- Heat map and supply-demand analysis
- Coupon and wallet system
- Rating system
- Push notifications
- Multi-city support
- Gray release and operational metrics dashboard

## 5. Recommended Front-end/Back-end Separation

## 5.1 Front-end Applications

### Passenger Front End

- Recommended: Vue 3 + TypeScript + Vite
- Main pages:
  - Login/Register
  - Home map page
  - Create order page
  - Waiting for driver page
  - In-trip page
  - Order history page

### Driver Front End

- Recommended: Vue 3 + TypeScript + Vite
- Main pages:
  - Login/Register
  - Online/offline dashboard
  - Order receive/accept page
  - Navigation/trip page
  - Income/history page

### Admin Front End

- Recommended: Vue 3 + TypeScript + Element Plus
- Main pages:
  - Dashboard
  - Driver review
  - User management
  - Vehicle management
  - Order monitoring

## 5.2 Back-end Services

- Recommended language: Go
- Suggested frameworks:
  - HTTP API: Gin or Fiber
  - ORM: GORM or sqlc + hand-written SQL
  - RPC: gRPC for internal services
  - Realtime push: WebSocket
  - Location uplink: UDP + Protobuf
  - Hot storage: in-memory cache + Redis
  - DB: MySQL 8.x

## 5.3 Communication Architecture

Use different protocols by business type instead of forcing every request through HTTP.

### HTTP/gRPC

Use for strong-business actions that require clear request/response semantics:

- login and authentication
- create order
- accept/reject order
- order status transitions
- order detail and list query
- admin operations

Recommended rule:

- external client-facing business APIs use HTTP
- internal service-to-service calls use gRPC

### UDP + Protobuf

Use for driver high-frequency location reporting:

- driver location uplink
- driver heartbeat with coordinates
- optional speed, heading, accuracy, timestamp

Recommended rule:

- only location stream uses UDP
- packet body uses Protobuf to reduce size
- packet loss is acceptable within a small range
- order, payment, and state transition data must not rely on UDP

### WebSocket

Use for front-end realtime push:

- driver dispatch notifications
- order status updates
- passenger map vehicle movement updates
- trip progress updates

Recommended rule:

- clients subscribe after login
- backend actively pushes changes instead of requiring frequent polling

## 6. Service Split

Recommended first iteration:

### 6.1 API Gateway Service

Responsibilities:

- Unified external API entry
- JWT authentication
- Rate limiting and request logging
- Route requests to internal modules/services

Main interfaces:

- `/api/passenger/*`
- `/api/driver/*`
- `/api/admin/*`
- `/ws/order`
- `/ws/location`
- `udp://location-ingress`

### 6.2 User Service

Responsibilities:

- Passenger account management
- Driver account management
- Admin account management
- Authentication and token issuance
- Driver certification state

### 6.3 Driver Service

Responsibilities:

- Driver profile
- Vehicle binding
- Driver work status
- Driver current order relationship

### 6.4 Order Service

Responsibilities:

- Create/cancel orders
- Order state machine
- Dispatch trigger
- Order query

Suggested status flow:

- `created`
- `pending_dispatch`
- `accepted`
- `driver_arrived`
- `in_trip`
- `completed`
- `cancelled`
- `to_be_paid`
- `paid`

### 6.5 Dispatch Service

Responsibilities:

- Find nearby available drivers
- Score and select candidate drivers
- Send order to candidate drivers
- Handle timeout and retry

Initial dispatch strategy:

- Radius-based nearby search
- Filter online + idle drivers
- Sort by distance and last active time

### 6.6 Realtime Location Service

Responsibilities:

- Receive driver location stream
- Maintain latest driver position
- Decode Protobuf packets
- Maintain nearby search index
- Push driver movement to passenger/driver clients
- Detect offline timeout

Recommended storage pattern:

- In-memory cache for very hot latest driver state
- Redis for latest location, online state, and shared hot data
- MySQL for persistent trip trajectory and business records

### 6.7 Trip Service

Responsibilities:

- Manage trip start/end
- Record route points
- Generate trip summary
- Estimate and settle fare

### 6.8 Pricing Service

Responsibilities:

- Base fare calculation
- Distance fee
- Duration fee
- Waiting fee
- Final fee snapshot output

MVP can be merged into Trip Service if team size is small.

### 6.9 Admin Service

Responsibilities:

- Driver certification review
- Order intervention
- Dashboard and reports

## 7. Monolith vs Microservice Suggestion

Recommended actual landing path:

### Phase 1

Use a modular monolith in Go:

- one repo
- one deployable backend app
- internal modules separated by domain

Reasons:

- Lower complexity
- Faster MVP delivery
- Easier local development
- Easier transaction handling

Suggested internal module structure:

- `cmd/`
- `internal/api/`
- `internal/auth/`
- `internal/user/`
- `internal/driver/`
- `internal/order/`
- `internal/dispatch/`
- `internal/location/`
- `internal/trip/`
- `internal/admin/`
- `pkg/`

### Phase 2

Split out heavy modules when pressure appears:

- realtime location
- dispatch
- pricing

## 8. Data Storage Design Draft

Recommended split:

- MySQL: core business data
- Redis: shared cache, online state, latest location, dispatch queue
- In-memory cache: process-local hot driver state and short-window trajectory

## 8.1 Storage Layering Strategy

### In-memory cache

Use for ultra-hot short-lived data:

- latest driver position
- latest driver heartbeat timestamp
- recent N location points of active drivers
- current trip realtime snapshot

### Redis

Use for shared hot data across instances:

- online/offline status
- latest driver location
- nearby driver geo index
- dispatch cache
- websocket session routing metadata

### MySQL

Use for durable business data:

- user and driver accounts
- vehicles
- orders
- trips
- persistent trip points
- payments

## 8.2 Core Tables

### `users`

Passenger and admin basic account table.

| Field | Type | Notes |
| --- | --- | --- |
| id | bigint PK | user id |
| role | varchar(20) | passenger/admin |
| phone | varchar(20) | unique |
| password_hash | varchar(255) | encrypted password |
| nickname | varchar(64) | display name |
| avatar | varchar(255) | avatar url |
| status | varchar(20) | active/disabled |
| created_at | datetime | created time |
| updated_at | datetime | updated time |

### `drivers`

Driver base information.

| Field | Type | Notes |
| --- | --- | --- |
| id | bigint PK | driver id |
| user_id | bigint | linked user account |
| real_name | varchar(64) | driver name |
| phone | varchar(20) | unique |
| license_no | varchar(64) | driver license id |
| id_card_no | varchar(64) | identity number |
| status | varchar(20) | pending/approved/rejected/disabled |
| work_status | varchar(20) | offline/idle/busy |
| current_vehicle_id | bigint | current vehicle |
| score | decimal(3,2) | rating score |
| created_at | datetime | created time |
| updated_at | datetime | updated time |

### `vehicles`

Vehicle information.

| Field | Type | Notes |
| --- | --- | --- |
| id | bigint PK | vehicle id |
| driver_id | bigint | owner or current driver |
| plate_no | varchar(32) | unique |
| brand | varchar(64) | brand |
| model | varchar(64) | model |
| color | varchar(32) | color |
| seat_count | int | seats |
| status | varchar(20) | active/inactive |
| created_at | datetime | created time |
| updated_at | datetime | updated time |

### `driver_sessions`

Driver online session table.

| Field | Type | Notes |
| --- | --- | --- |
| id | bigint PK | session id |
| driver_id | bigint | driver id |
| login_token | varchar(128) | optional session token |
| device_type | varchar(32) | ios/android/web |
| status | varchar(20) | online/offline/expired |
| online_at | datetime | online time |
| offline_at | datetime | offline time |
| last_heartbeat_at | datetime | last active time |
| created_at | datetime | created time |
| updated_at | datetime | updated time |

### `orders`

Ride order main table.

| Field | Type | Notes |
| --- | --- | --- |
| id | bigint PK | order id |
| order_no | varchar(64) | unique business number |
| passenger_id | bigint | passenger id |
| driver_id | bigint | nullable before acceptance |
| vehicle_id | bigint | nullable before acceptance |
| status | varchar(32) | order status |
| pickup_lat | decimal(10,7) | pickup latitude |
| pickup_lng | decimal(10,7) | pickup longitude |
| pickup_address | varchar(255) | pickup address |
| dest_lat | decimal(10,7) | destination latitude |
| dest_lng | decimal(10,7) | destination longitude |
| dest_address | varchar(255) | destination address |
| estimated_distance_m | int | estimated meters |
| estimated_duration_s | int | estimated seconds |
| estimated_price | decimal(10,2) | estimated fee |
| final_price | decimal(10,2) | final fee |
| cancel_reason | varchar(255) | nullable |
| created_at | datetime | created time |
| updated_at | datetime | updated time |

### `order_dispatch_records`

Dispatch detail table.

| Field | Type | Notes |
| --- | --- | --- |
| id | bigint PK | id |
| order_id | bigint | order id |
| driver_id | bigint | candidate driver |
| dispatch_round | int | retry round |
| distance_m | int | candidate distance |
| status | varchar(20) | pending/accepted/rejected/timeout |
| sent_at | datetime | dispatch time |
| responded_at | datetime | response time |
| created_at | datetime | created time |

### `trips`

Trip lifecycle table.

| Field | Type | Notes |
| --- | --- | --- |
| id | bigint PK | trip id |
| order_id | bigint | linked order |
| passenger_id | bigint | passenger |
| driver_id | bigint | driver |
| start_at | datetime | trip start |
| end_at | datetime | trip end |
| actual_distance_m | int | actual meters |
| actual_duration_s | int | actual duration |
| waiting_duration_s | int | waiting duration |
| fee_snapshot | json | pricing detail |
| route_summary | json | route summary |
| created_at | datetime | created time |
| updated_at | datetime | updated time |

### `trip_points`

Persistent trajectory points for active/completed trips.

| Field | Type | Notes |
| --- | --- | --- |
| id | bigint PK | point id |
| trip_id | bigint | trip id |
| driver_id | bigint | driver id |
| lat | decimal(10,7) | latitude |
| lng | decimal(10,7) | longitude |
| speed | decimal(8,2) | optional |
| direction | decimal(8,2) | optional |
| accuracy | decimal(8,2) | optional |
| recorded_at | datetime | point time |
| created_at | datetime | created time |

### `payments`

Payment status table.

| Field | Type | Notes |
| --- | --- | --- |
| id | bigint PK | payment id |
| order_id | bigint | order id |
| passenger_id | bigint | payer |
| amount | decimal(10,2) | payment amount |
| pay_method | varchar(20) | mock/wechat/alipay/cash |
| status | varchar(20) | unpaid/paid/refunded |
| paid_at | datetime | nullable |
| created_at | datetime | created time |
| updated_at | datetime | updated time |

## 8.3 Recommended Redis Keys

- `driver:location:{driverId}`: latest driver location
- `driver:status:{driverId}`: online/idle/busy
- `driver:session:{driverId}`: current session
- `geo:drivers:idle`: nearby query geo index
- `order:dispatch:{orderId}`: dispatch state cache
- `order:realtime:{orderId}`: current order realtime snapshot

## 9. State Design

### 9.1 Driver Work Status

- `offline`
- `idle`
- `to_pickup`
- `in_trip`

### 9.2 Order Status

- `created`
- `pending_dispatch`
- `accepted`
- `driver_arrived`
- `in_trip`
- `completed`
- `to_be_paid`
- `paid`
- `cancelled`

Important rule:

- Driver work status and order status must be changed atomically in the same transaction whenever possible.

## 10. Suggested API Groups

### Passenger APIs

- `POST /api/passenger/auth/register`
- `POST /api/passenger/auth/login`
- `GET /api/passenger/drivers/nearby`
- `POST /api/passenger/orders`
- `POST /api/passenger/orders/{id}/cancel`
- `GET /api/passenger/orders/{id}`
- `GET /api/passenger/orders`

### Driver APIs

- `POST /api/driver/auth/login`
- `POST /api/driver/status/online`
- `POST /api/driver/status/offline`
- `POST /api/driver/location/report`
- `POST /api/driver/orders/{id}/accept`
- `POST /api/driver/orders/{id}/reject`
- `POST /api/driver/orders/{id}/arrive`
- `POST /api/driver/orders/{id}/start`
- `POST /api/driver/orders/{id}/finish`

### Admin APIs

- `POST /api/admin/auth/login`
- `GET /api/admin/drivers`
- `POST /api/admin/drivers/{id}/approve`
- `POST /api/admin/drivers/{id}/reject`
- `GET /api/admin/orders`
- `GET /api/admin/trips`

### WebSocket Channels

- `/ws/order/{orderId}`
- `/ws/driver/{driverId}/dispatch`
- `/ws/passenger/{userId}/location`

## 11. Development Priority

Recommended build order:

1. User auth and role model
2. Driver online/offline and location reporting
3. Nearby driver query
4. Order creation and state machine
5. Driver accept/start/finish loop
6. Real-time order and map updates through WebSocket
7. Trip route persistence and fee calculation
8. Admin management pages

## 12. Recommended MVP Repo Layout

```text
Uber_test/
|-- backend/
|   |-- cmd/server/
|   |-- internal/
|   |   |-- api/
|   |   |-- auth/
|   |   |-- user/
|   |   |-- driver/
|   |   |-- order/
|   |   |-- dispatch/
|   |   |-- location/
|   |   |-- trip/
|   |   |-- payment/
|   |   `-- admin/
|   |-- migrations/
|   `-- configs/
|-- frontend-passenger/
|-- frontend-driver/
|-- frontend-admin/
`-- docs/
```

## 13. Practical Engineering Suggestions

- Start with a modular monolith instead of microservices.
- Use `UDP + Protobuf` only for driver location uplink.
- Use HTTP for external business APIs and gRPC for internal service calls.
- Use WebSocket for order and map realtime push.
- Use in-memory cache + Redis + MySQL as the default storage layering.
- Persist only key trip points if write pressure becomes high.
- Keep dispatch logic replaceable through an interface.
- Write the order state machine centrally to avoid status drift.

## 14. Summary

If we implement only the MVP, the project can be understood as three major systems:

- passenger ride system
- driver operation and location system
- backend order/dispatch/trip system

The best first landing path is:

- Go modular monolith backend
- three separated front ends
- in-memory cache + Redis + MySQL
- HTTP/gRPC + UDP + WebSocket

This balance is usually enough to demonstrate both business completeness and the technical highlight of Uber-like realtime map movement.
