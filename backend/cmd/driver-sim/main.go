package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	locationpb "uber-test/backend/internal/gen/location/v1"
	"uber-test/backend/internal/model"

	"google.golang.org/protobuf/proto"
)

const (
	earthRadius = 6378245.0
	ee          = 0.00669342162296594323
)

type registerRequest struct {
	Phone       string `json:"phone"`
	Password    string `json:"password"`
	Role        string `json:"role"`
	DisplayName string `json:"display_name"`
}

type loginRequest struct {
	Phone    string `json:"phone"`
	Password string `json:"password"`
}

type loginResponse struct {
	Item struct {
		Token string     `json:"token"`
		User  model.User `json:"user"`
	} `json:"item"`
}

type orderResponse struct {
	Item model.Order `json:"item"`
}

type dispatchListResponse struct {
	Items []model.DispatchAssignment `json:"items"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type routePoint struct {
	Lat float64
	Lng float64
}

type driverAgent struct {
	apiBase       string
	udpAddr       string
	httpClient    *http.Client
	displayName   string
	phone         string
	password      string
	driverID      string
	token         string
	baseLat       float64
	baseLng       float64
	radiusMeters  float64
	speedStep     float64
	autoAccept    bool
	autoProgress  bool
	arriveDelay   time.Duration
	startDelay    time.Duration
	completeDelay time.Duration
	idleStepM     float64
	activeStepM   float64
	arriveWithinM float64

	mu           sync.Mutex
	current      *trackedOrder
	startedAt    time.Time
	locationSeq  int
	currentLat   float64
	currentLng   float64
	idlePhase    float64
	routeMode    string
	routeOrderID string
	routePath    []routePoint
}

type trackedOrder struct {
	Order       model.Order
	StatusSince time.Time
}

func main() {
	var (
		apiBase       = flag.String("api-base", "http://127.0.0.1:8080", "HTTP API base URL")
		udpAddr       = flag.String("udp-addr", "127.0.0.1:9000", "UDP location ingress address")
		driverCount   = flag.Int("drivers", 2, "number of simulated drivers")
		phonePrefix   = flag.String("phone-prefix", "1390000", "phone prefix used to generate test accounts")
		password      = flag.String("password", "pass123456", "password for simulated drivers")
		centerLat     = flag.Float64("lat", 31.2304, "base latitude")
		centerLng     = flag.Float64("lng", 121.4737, "base longitude")
		radiusMeters  = flag.Float64("radius-m", 700, "orbit radius in meters")
		speedStep     = flag.Float64("speed-step", 0.15, "orbit speed multiplier")
		autoAccept    = flag.Bool("auto-accept", true, "automatically accept incoming dispatches")
		autoProgress  = flag.Bool("auto-progress", true, "automatically move accepted orders to completed")
		arriveDelay   = flag.Duration("arrive-delay", 8*time.Second, "delay from accepted to driver_arrived")
		startDelay    = flag.Duration("start-delay", 2*time.Second, "delay from arrived to in_trip")
		completeDelay = flag.Duration("complete-delay", 16*time.Second, "delay from in_trip to completed")
		idleStepM     = flag.Float64("idle-step-m", 12, "meters a free driver moves on each location tick")
		activeStepM   = flag.Float64("active-step-m", 35, "meters an active driver moves on each location tick")
		arriveWithinM = flag.Float64("arrive-within-m", 45, "distance threshold treated as arrived/completed")
	)
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agents := make([]*driverAgent, 0, max(1, *driverCount))
	for i := 0; i < max(1, *driverCount); i++ {
		offset := float64(i) * (360.0 / float64(max(1, *driverCount)))
		agents = append(agents, &driverAgent{
			apiBase:       strings.TrimRight(*apiBase, "/"),
			udpAddr:       *udpAddr,
			httpClient:    &http.Client{Timeout: 10 * time.Second},
			displayName:   fmt.Sprintf("Sim Driver %d", i+1),
			phone:         fmt.Sprintf("%s%04d", *phonePrefix, i+1),
			password:      *password,
			baseLat:       *centerLat,
			baseLng:       *centerLng,
			radiusMeters:  *radiusMeters,
			speedStep:     *speedStep,
			autoAccept:    *autoAccept,
			autoProgress:  *autoProgress,
			arriveDelay:   *arriveDelay,
			startDelay:    *startDelay,
			completeDelay: *completeDelay,
			idleStepM:     *idleStepM,
			activeStepM:   *activeStepM,
			arriveWithinM: *arriveWithinM,
			startedAt:     time.Now().UTC().Add(time.Duration(offset) * time.Millisecond),
			idlePhase:     offset * math.Pi / 180,
		})
	}

	for _, agent := range agents {
		if err := agent.bootstrap(ctx); err != nil {
			log.Fatalf("bootstrap %s failed: %v", agent.displayName, err)
		}
		log.Printf("%s ready phone=%s driver_id=%s", agent.displayName, agent.phone, agent.driverID)
	}

	var wg sync.WaitGroup
	for _, agent := range agents {
		wg.Add(1)
		go func(agent *driverAgent) {
			defer wg.Done()
			agent.run(ctx)
		}(agent)
	}

	log.Printf("driver simulator running with %d drivers, api=%s, udp=%s", len(agents), *apiBase, *udpAddr)
	select {}
}

func (a *driverAgent) bootstrap(ctx context.Context) error {
	registerBody := registerRequest{
		Phone:       a.phone,
		Password:    a.password,
		Role:        model.RoleDriver,
		DisplayName: a.displayName,
	}
	status, body, err := a.doJSON(ctx, http.MethodPost, "/api/v1/auth/register", "", registerBody)
	if err != nil {
		return err
	}
	if status != http.StatusCreated && status != http.StatusConflict {
		return fmt.Errorf("register driver: status=%d body=%s", status, string(body))
	}

	var loginResult loginResponse
	if err := a.decodeJSON(ctx, http.MethodPost, "/api/v1/auth/login", "", loginRequest{
		Phone:    a.phone,
		Password: a.password,
	}, &loginResult); err != nil {
		return err
	}

	a.token = loginResult.Item.Token
	a.driverID = loginResult.Item.User.DriverID
	if a.driverID == "" {
		return errors.New("login result missing driver_id")
	}

	var onlineResult struct {
		Item model.DriverStatus `json:"item"`
	}
	if err := a.decodeJSON(ctx, http.MethodPost, fmt.Sprintf("/api/v1/drivers/%s/status", a.driverID), a.token, map[string]string{
		"status": model.DriverStatusOnline,
	}, &onlineResult); err != nil {
		return err
	}

	if err := a.recoverActiveOrder(ctx); err != nil {
		return err
	}

	return nil
}

func (a *driverAgent) recoverActiveOrder(ctx context.Context) error {
	current, err := a.getCurrentOrder(ctx)
	if err != nil {
		return err
	}
	if current == nil {
		return nil
	}

	log.Printf("%s recovering stale active order %s status=%s", a.displayName, current.ID, current.Status)

	orderItem := *current
	switch orderItem.Status {
	case model.OrderStatusAccepted:
		orderItem, err = a.updateOrderStatus(ctx, orderItem.ID, model.UpdateOrderStatusInput{Status: model.OrderStatusDriverArrived})
		if err != nil {
			return fmt.Errorf("recover accepted order to arrived: %w", err)
		}
		fallthrough
	case model.OrderStatusDriverArrived:
		orderItem, err = a.updateOrderStatus(ctx, orderItem.ID, model.UpdateOrderStatusInput{Status: model.OrderStatusInTrip})
		if err != nil {
			return fmt.Errorf("recover arrived order to in_trip: %w", err)
		}
		fallthrough
	case model.OrderStatusInTrip:
		orderItem, err = a.updateOrderStatus(ctx, orderItem.ID, model.UpdateOrderStatusInput{
			Status:           model.OrderStatusCompleted,
			ActualDistanceM:  6200,
			ActualDurationS:  1080,
			WaitingDurationS: 120,
		})
		if err != nil {
			return fmt.Errorf("recover in_trip order to completed: %w", err)
		}
		log.Printf("%s recovered order %s => %s", a.displayName, orderItem.ID, orderItem.Status)
	default:
		log.Printf("%s found active order in unsupported recovery status %s", a.displayName, orderItem.Status)
	}

	a.current = nil
	return nil
}

func (a *driverAgent) run(ctx context.Context) {
	locationTicker := time.NewTicker(500 * time.Millisecond)
	dispatchTicker := time.NewTicker(2 * time.Second)
	defer locationTicker.Stop()
	defer dispatchTicker.Stop()

	_ = a.sendLocationUpdate(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-locationTicker.C:
			if err := a.sendLocationUpdate(ctx); err != nil {
				log.Printf("%s location update failed: %v", a.displayName, err)
			}
		case <-dispatchTicker.C:
			if err := a.syncAndDriveOrder(ctx); err != nil {
				log.Printf("%s dispatch loop failed: %v", a.displayName, err)
			}
		}
	}
}

func (a *driverAgent) syncAndDriveOrder(ctx context.Context) error {
	current, err := a.getCurrentOrder(ctx)
	if err != nil {
		return err
	}

	a.mu.Lock()
	shouldClearRoute := false
	if current == nil {
		shouldClearRoute = a.current != nil || a.routeOrderID != ""
		a.current = nil
	} else if a.current == nil || a.current.Order.ID != current.ID || a.current.Order.Status != current.Status {
		a.current = &trackedOrder{Order: *current, StatusSince: time.Now().UTC()}
	}
	tracked := a.current
	a.mu.Unlock()

	if shouldClearRoute {
		a.clearRouteState()
	}

	if current == nil {
		if !a.autoAccept {
			return nil
		}
		dispatches, err := a.listDispatches(ctx)
		if err != nil {
			return err
		}
		if len(dispatches) == 0 {
			return nil
		}

		orderItem, err := a.updateOrderStatus(ctx, dispatches[0].Order.ID, model.UpdateOrderStatusInput{
			Status:   model.OrderStatusAccepted,
			DriverID: a.driverID,
		})
		if err != nil {
			return err
		}

		a.mu.Lock()
		a.current = &trackedOrder{Order: orderItem, StatusSince: time.Now().UTC()}
		a.mu.Unlock()
		if err := a.refreshRouteFromBackend(ctx, orderItem); err != nil {
			log.Printf("%s initial route fetch failed: %v", a.displayName, err)
		}
		log.Printf("%s accepted order %s", a.displayName, orderItem.ID)
		return nil
	}

	if !a.autoProgress || tracked == nil {
		if tracked != nil {
			if err := a.refreshRouteFromBackend(ctx, tracked.Order); err != nil {
				log.Printf("%s backend route sync failed: %v", a.displayName, err)
			}
		}
		return nil
	}

	if err := a.refreshRouteFromBackend(ctx, tracked.Order); err != nil {
		log.Printf("%s backend route sync failed: %v", a.displayName, err)
	}

	now := time.Now().UTC()
	switch tracked.Order.Status {
	case model.OrderStatusAccepted:
		if a.distanceTo(tracked.Order.PickupLat, tracked.Order.PickupLng) <= a.arriveWithinM {
			return a.progressOrder(ctx, tracked.Order.ID, model.UpdateOrderStatusInput{Status: model.OrderStatusDriverArrived}, "arrived")
		}
	case model.OrderStatusDriverArrived:
		if now.Sub(tracked.StatusSince) >= a.startDelay {
			return a.progressOrder(ctx, tracked.Order.ID, model.UpdateOrderStatusInput{Status: model.OrderStatusInTrip}, "started")
		}
	case model.OrderStatusInTrip:
		if a.distanceTo(tracked.Order.DestinationLat, tracked.Order.DestinationLng) <= a.arriveWithinM {
			return a.progressOrder(ctx, tracked.Order.ID, model.UpdateOrderStatusInput{
				Status:           model.OrderStatusCompleted,
				ActualDistanceM:  6200,
				ActualDurationS:  1080,
				WaitingDurationS: 120,
			}, "completed")
		}
	}

	return nil
}

func (a *driverAgent) progressOrder(ctx context.Context, orderID string, input model.UpdateOrderStatusInput, action string) error {
	orderItem, err := a.updateOrderStatus(ctx, orderID, input)
	if err != nil {
		return err
	}

	a.mu.Lock()
	a.current = &trackedOrder{Order: orderItem, StatusSince: time.Now().UTC()}
	a.mu.Unlock()
	if err := a.refreshRouteFromBackend(ctx, orderItem); err != nil {
		log.Printf("%s route refresh after %s failed: %v", a.displayName, action, err)
	}
	log.Printf("%s %s order %s => %s", a.displayName, action, orderID, orderItem.Status)
	return nil
}

func (a *driverAgent) getCurrentOrder(ctx context.Context) (*model.Order, error) {
	status, body, err := a.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/v1/drivers/%s/current-order", a.driverID), a.token, nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("get current order: status=%d body=%s", status, string(body))
	}

	var result orderResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode current order: %w", err)
	}
	return &result.Item, nil
}

func (a *driverAgent) listDispatches(ctx context.Context) ([]model.DispatchAssignment, error) {
	status, body, err := a.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/v1/drivers/%s/dispatches", a.driverID), a.token, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("list dispatches: status=%d body=%s", status, string(body))
	}

	var result dispatchListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode dispatches: %w", err)
	}
	return result.Items, nil
}

func (a *driverAgent) refreshRouteFromBackend(ctx context.Context, orderItem model.Order) error {
	if orderItem.ID == "" {
		a.clearRouteState()
		return nil
	}
	if orderItem.Status != model.OrderStatusAccepted && orderItem.Status != model.OrderStatusDriverArrived && orderItem.Status != model.OrderStatusInTrip {
		a.clearRouteState()
		return nil
	}

	status, body, err := a.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/v1/orders/%s/route", orderItem.ID), a.token, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		a.clearRouteState()
		return nil
	}
	if status != http.StatusOK {
		return fmt.Errorf("get backend route: status=%d body=%s", status, string(body))
	}

	var result struct {
		Item model.DriverRoute `json:"item"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decode backend route: %w", err)
	}

	nextPath := make([]routePoint, 0, len(result.Item.Points))
	for _, point := range result.Item.Points {
		nextPath = append(nextPath, routePoint{Lat: point.Lat, Lng: point.Lng})
	}

	a.mu.Lock()
	a.routeMode = result.Item.Mode
	a.routeOrderID = orderItem.ID
	a.routePath = nextPath
	a.mu.Unlock()
	return nil
}

func (a *driverAgent) updateOrderStatus(ctx context.Context, orderID string, input model.UpdateOrderStatusInput) (model.Order, error) {
	var result orderResponse
	if err := a.decodeJSON(ctx, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/status", orderID), a.token, input, &result); err != nil {
		return model.Order{}, err
	}
	return result.Item, nil
}

func (a *driverAgent) sendLocationUpdate(ctx context.Context) error {
	orderID := ""
	lat, lng, heading, speedKPH := a.nextPosition()
	a.mu.Lock()
	if a.current != nil {
		orderID = a.current.Order.ID
	}
	a.mu.Unlock()

	payload, err := proto.Marshal(&locationpb.LocationIngressPacket{
		Payload: &locationpb.LocationIngressPacket_LocationUpdate{
			LocationUpdate: &locationpb.DriverLocationUpdate{
				DriverId:         a.driverID,
				OrderId:          orderID,
				Lat:              lat,
				Lng:              lng,
				SpeedKph:         speedKPH,
				Heading:          heading,
				AccuracyM:        6,
				ReportedAtUnixMs: time.Now().UTC().UnixMilli(),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal location protobuf: %w", err)
	}

	conn, err := net.Dial("udp", a.udpAddr)
	if err != nil {
		return fmt.Errorf("dial udp: %w", err)
	}
	defer conn.Close()

	if deadlineErr := conn.SetDeadline(time.Now().Add(3 * time.Second)); deadlineErr == nil {
		_, err = conn.Write(payload)
	} else {
		_, err = conn.Write(payload)
	}
	if err != nil {
		return fmt.Errorf("write udp payload: %w", err)
	}
	return nil
}

func (a *driverAgent) nextPosition() (float64, float64, float64, float64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.currentLat == 0 && a.currentLng == 0 {
		a.currentLat = a.baseLat + metersToLat(a.radiusMeters*math.Cos(a.idlePhase))
		a.currentLng = a.baseLng + metersToLng(a.radiusMeters*math.Sin(a.idlePhase), a.baseLat)
	}

	prevLat := a.currentLat
	prevLng := a.currentLng
	targetLat := a.currentLat
	targetLng := a.currentLng
	stepM := a.idleStepM
	speedKPH := 28.0
	mode := "idle"

	switch {
	case a.current == nil:
		a.locationSeq++
		a.idlePhase += a.speedStep
		targetLat = a.baseLat + metersToLat(a.radiusMeters*math.Cos(a.idlePhase))
		targetLng = a.baseLng + metersToLng(a.radiusMeters*math.Sin(a.idlePhase), a.baseLat)
	case a.current.Order.Status == model.OrderStatusAccepted || a.current.Order.Status == model.OrderStatusDriverArrived:
		targetLat = a.current.Order.PickupLat
		targetLng = a.current.Order.PickupLng
		stepM = a.activeStepM
		speedKPH = 42
		mode = "pickup"
	case a.current.Order.Status == model.OrderStatusInTrip:
		targetLat = a.current.Order.DestinationLat
		targetLng = a.current.Order.DestinationLng
		stepM = a.activeStepM
		speedKPH = 48
		mode = "trip"
	default:
		a.locationSeq++
		a.idlePhase += a.speedStep
		targetLat = a.baseLat + metersToLat(a.radiusMeters*math.Cos(a.idlePhase))
		targetLng = a.baseLng + metersToLng(a.radiusMeters*math.Sin(a.idlePhase), a.baseLat)
	}

	if mode == "idle" {
		a.routeMode = ""
		a.routePath = nil
		a.currentLat, a.currentLng = moveTowards(a.currentLat, a.currentLng, targetLat, targetLng, stepM)
	} else {
		if linearDistanceMeters(a.currentLat, a.currentLng, targetLat, targetLng) <= a.arriveWithinM {
			a.routeMode = mode
			a.routePath = nil
			a.currentLat = targetLat
			a.currentLng = targetLng
		} else if a.ensureRouteLocked(mode) {
			a.currentLat, a.currentLng = followRoute(a.currentLat, a.currentLng, &a.routePath, stepM)
		} else {
			a.currentLat, a.currentLng = moveTowards(a.currentLat, a.currentLng, targetLat, targetLng, stepM)
		}
	}

	heading := bearingDegrees(prevLat, prevLng, a.currentLat, a.currentLng)
	return a.currentLat, a.currentLng, heading, speedKPH
}

func (a *driverAgent) ensureRouteLocked(mode string) bool {
	if a.current == nil {
		return false
	}
	return a.routeMode == mode && a.routeOrderID == a.current.Order.ID && len(a.routePath) > 0
}

func (a *driverAgent) clearRouteState() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.routeMode = ""
	a.routeOrderID = ""
	a.routePath = nil
}

func (a *driverAgent) distanceTo(lat, lng float64) float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentLat == 0 && a.currentLng == 0 {
		return linearDistanceMeters(a.baseLat, a.baseLng, lat, lng)
	}
	return linearDistanceMeters(a.currentLat, a.currentLng, lat, lng)
}

func (a *driverAgent) decodeJSON(ctx context.Context, method, path, token string, payload any, out any) error {
	status, body, err := a.doJSON(ctx, method, path, token, payload)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("%s %s failed: status=%d body=%s", method, path, status, string(body))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return nil
}

func (a *driverAgent) doJSON(ctx context.Context, method, path, token string, payload any) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, a.apiBase+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

func metersToLat(meters float64) float64 {
	return meters / 111320.0
}

func metersToLng(meters float64, lat float64) float64 {
	return meters / (111320.0 * math.Cos(lat*math.Pi/180))
}

func moveTowards(fromLat, fromLng, toLat, toLng, stepM float64) (float64, float64) {
	distance := linearDistanceMeters(fromLat, fromLng, toLat, toLng)
	if distance == 0 || distance <= stepM {
		return toLat, toLng
	}

	ratio := stepM / distance
	return fromLat + (toLat-fromLat)*ratio, fromLng + (toLng-fromLng)*ratio
}

func linearDistanceMeters(fromLat, fromLng, toLat, toLng float64) float64 {
	latMeters := (toLat - fromLat) * 111320.0
	midLatRad := ((fromLat + toLat) / 2) * math.Pi / 180
	lngMeters := (toLng - fromLng) * 111320.0 * math.Cos(midLatRad)
	return math.Hypot(latMeters, lngMeters)
}

func bearingDegrees(fromLat, fromLng, toLat, toLng float64) float64 {
	latMeters := (toLat - fromLat) * 111320.0
	midLatRad := ((fromLat + toLat) / 2) * math.Pi / 180
	lngMeters := (toLng - fromLng) * 111320.0 * math.Cos(midLatRad)
	if latMeters == 0 && lngMeters == 0 {
		return 0
	}
	heading := math.Atan2(lngMeters, latMeters) * 180 / math.Pi
	if heading < 0 {
		heading += 360
	}
	return heading
}

func followRoute(fromLat, fromLng float64, path *[]routePoint, stepM float64) (float64, float64) {
	if len(*path) == 0 {
		return fromLat, fromLng
	}

	currentLat := fromLat
	currentLng := fromLng
	remaining := stepM

	for remaining > 0 && len(*path) > 0 {
		next := (*path)[0]
		distance := linearDistanceMeters(currentLat, currentLng, next.Lat, next.Lng)
		if distance == 0 {
			*path = (*path)[1:]
			continue
		}
		if distance <= remaining {
			currentLat = next.Lat
			currentLng = next.Lng
			remaining -= distance
			*path = (*path)[1:]
			continue
		}

		currentLat, currentLng = moveTowards(currentLat, currentLng, next.Lat, next.Lng, remaining)
		remaining = 0
	}

	return currentLat, currentLng
}

func fetchAMapDrivingRoute(client *http.Client, key string, originLat, originLng, destinationLat, destinationLng float64) ([]routePoint, error) {
	query := url.Values{}
	query.Set("key", key)
	query.Set("origin", fmt.Sprintf("%.6f,%.6f", originLng, originLat))
	query.Set("destination", fmt.Sprintf("%.6f,%.6f", destinationLng, destinationLat))
	query.Set("extensions", "base")
	query.Set("output", "JSON")
	query.Set("strategy", "0")

	req, err := http.NewRequest(http.MethodGet, "https://restapi.amap.com/v3/direction/driving?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status=%d body=%s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Info   string `json:"info"`
		Route  struct {
			Paths []struct {
				Steps []struct {
					Polyline string `json:"polyline"`
				} `json:"steps"`
			} `json:"paths"`
		} `json:"route"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode route response: %w", err)
	}
	if result.Status != "1" {
		return nil, fmt.Errorf("route api failed: %s", result.Info)
	}
	if len(result.Route.Paths) == 0 {
		return nil, errors.New("route api returned no paths")
	}

	points := make([]routePoint, 0, 128)
	for _, step := range result.Route.Paths[0].Steps {
		points = append(points, parsePolyline(step.Polyline)...)
	}
	points = dedupeRoutePoints(points)
	if len(points) == 0 {
		return nil, errors.New("route api returned empty polyline")
	}
	return points, nil
}

func fetchOSRMRoute(client *http.Client, baseURL string, originLat, originLng, destinationLat, destinationLng float64) ([]routePoint, error) {
	originLatWGS, originLngWGS := gcj02ToWGS84(originLat, originLng)
	destinationLatWGS, destinationLngWGS := gcj02ToWGS84(destinationLat, destinationLng)
	endpoint := fmt.Sprintf(
		"%s/route/v1/driving/%.6f,%.6f;%.6f,%.6f?overview=full&geometries=geojson",
		strings.TrimRight(baseURL, "/"),
		originLngWGS,
		originLatWGS,
		destinationLngWGS,
		destinationLatWGS,
	)

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("osrm status=%d body=%s", resp.StatusCode, string(body))
	}

	var result struct {
		Code   string `json:"code"`
		Routes []struct {
			Geometry struct {
				Coordinates [][]float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode osrm route response: %w", err)
	}
	if result.Code != "Ok" {
		return nil, fmt.Errorf("osrm route api failed: %s", result.Code)
	}
	if len(result.Routes) == 0 {
		return nil, errors.New("osrm route api returned no routes")
	}

	points := make([]routePoint, 0, len(result.Routes[0].Geometry.Coordinates))
	for _, coordinate := range result.Routes[0].Geometry.Coordinates {
		if len(coordinate) < 2 {
			continue
		}
		latGCJ, lngGCJ := wgs84ToGCJ02(coordinate[1], coordinate[0])
		points = append(points, routePoint{
			Lat: latGCJ,
			Lng: lngGCJ,
		})
	}
	points = dedupeRoutePoints(points)
	if len(points) == 0 {
		return nil, errors.New("osrm route api returned empty geometry")
	}
	return points, nil
}

func outOfChina(lat, lng float64) bool {
	return lng < 72.004 || lng > 137.8347 || lat < 0.8293 || lat > 55.8271
}

func transformLat(x, y float64) float64 {
	ret := -100.0 + 2.0*x + 3.0*y + 0.2*y*y + 0.1*x*y + 0.2*math.Sqrt(math.Abs(x))
	ret += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	ret += (20.0*math.Sin(y*math.Pi) + 40.0*math.Sin(y/3.0*math.Pi)) * 2.0 / 3.0
	ret += (160.0*math.Sin(y/12.0*math.Pi) + 320.0*math.Sin(y*math.Pi/30.0)) * 2.0 / 3.0
	return ret
}

func transformLng(x, y float64) float64 {
	ret := 300.0 + x + 2.0*y + 0.1*x*x + 0.1*x*y + 0.1*math.Sqrt(math.Abs(x))
	ret += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	ret += (20.0*math.Sin(x*math.Pi) + 40.0*math.Sin(x/3.0*math.Pi)) * 2.0 / 3.0
	ret += (150.0*math.Sin(x/12.0*math.Pi) + 300.0*math.Sin(x/30.0*math.Pi)) * 2.0 / 3.0
	return ret
}

func deltaGCJ02(lat, lng float64) (float64, float64) {
	dLat := transformLat(lng-105.0, lat-35.0)
	dLng := transformLng(lng-105.0, lat-35.0)
	radLat := lat / 180.0 * math.Pi
	magic := math.Sin(radLat)
	magic = 1 - ee*magic*magic
	sqrtMagic := math.Sqrt(magic)
	dLat = (dLat * 180.0) / ((earthRadius * (1 - ee)) / (magic * sqrtMagic) * math.Pi)
	dLng = (dLng * 180.0) / (earthRadius / sqrtMagic * math.Cos(radLat) * math.Pi)
	return dLat, dLng
}

func wgs84ToGCJ02(lat, lng float64) (float64, float64) {
	if outOfChina(lat, lng) {
		return lat, lng
	}
	dLat, dLng := deltaGCJ02(lat, lng)
	return lat + dLat, lng + dLng
}

func gcj02ToWGS84(lat, lng float64) (float64, float64) {
	if outOfChina(lat, lng) {
		return lat, lng
	}
	dLat, dLng := deltaGCJ02(lat, lng)
	return lat - dLat, lng - dLng
}

func parsePolyline(polyline string) []routePoint {
	if polyline == "" {
		return nil
	}

	chunks := strings.Split(polyline, ";")
	points := make([]routePoint, 0, len(chunks))
	for _, chunk := range chunks {
		parts := strings.Split(strings.TrimSpace(chunk), ",")
		if len(parts) != 2 {
			continue
		}

		var lng, lat float64
		if _, err := fmt.Sscanf(parts[0], "%f", &lng); err != nil {
			continue
		}
		if _, err := fmt.Sscanf(parts[1], "%f", &lat); err != nil {
			continue
		}
		points = append(points, routePoint{Lat: lat, Lng: lng})
	}
	return points
}

func dedupeRoutePoints(points []routePoint) []routePoint {
	if len(points) == 0 {
		return nil
	}

	result := make([]routePoint, 0, len(points))
	for _, point := range points {
		if len(result) == 0 || linearDistanceMeters(result[len(result)-1].Lat, result[len(result)-1].Lng, point.Lat, point.Lng) > 1 {
			result = append(result, point)
		}
	}
	return result
}

func resolveAmapWebKey(explicit string) string {
	if explicit != "" {
		return explicit
	}

	if env := strings.TrimSpace(os.Getenv("AMAP_WEB_SERVICE_KEY")); env != "" {
		return env
	}

	for _, candidate := range []string{
		filepath.Join("..", "frontend-passenger", ".env.local"),
		filepath.Join("..", "frontend-passenger", ".env.example"),
	} {
		for _, envKey := range []string{"VITE_AMAP_WEB_SERVICE_KEY", "VITE_AMAP_KEY"} {
			if key := readKeyFromEnvFile(candidate, envKey); key != "" {
				return key
			}
		}
	}

	return ""
}

func readKeyFromEnvFile(path string, key string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	prefix := key + "="
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		value = strings.Trim(value, `"'`)
		if value != "" {
			return value
		}
	}

	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
