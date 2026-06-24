package routeplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"uber-test/backend/internal/model"
)

type Planner interface {
	Plan(ctx context.Context, originLat, originLng, destinationLat, destinationLng float64) ([]model.RoutePoint, error)
}

type PlannerConfig struct {
	AMapWebKey     string
	OSRMBaseURL    string
	RequestTimeout time.Duration
}

type HTTPPlanner struct {
	client      *http.Client
	amapWebKey  string
	osrmBaseURL string
}

func NewHTTPPlanner(cfg PlannerConfig) *HTTPPlanner {
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	return &HTTPPlanner{
		client: &http.Client{
			Timeout: timeout,
		},
		amapWebKey:  strings.TrimSpace(cfg.AMapWebKey),
		osrmBaseURL: strings.TrimRight(strings.TrimSpace(cfg.OSRMBaseURL), "/"),
	}
}

func (p *HTTPPlanner) Plan(ctx context.Context, originLat, originLng, destinationLat, destinationLng float64) ([]model.RoutePoint, error) {
	if p == nil {
		return nil, errors.New("route planner is nil")
	}

	if p.amapWebKey != "" {
		path, err := fetchAMapDrivingRoute(ctx, p.client, p.amapWebKey, originLat, originLng, destinationLat, destinationLng)
		if err == nil && len(path) > 0 {
			return path, nil
		}
		if p.osrmBaseURL == "" {
			return nil, err
		}
	}

	if p.osrmBaseURL == "" {
		return nil, errors.New("no route provider configured")
	}
	return fetchOSRMRoute(ctx, p.client, p.osrmBaseURL, originLat, originLng, destinationLat, destinationLng)
}

func fetchAMapDrivingRoute(ctx context.Context, client *http.Client, key string, originLat, originLng, destinationLat, destinationLng float64) ([]model.RoutePoint, error) {
	query := url.Values{}
	query.Set("key", key)
	query.Set("origin", fmt.Sprintf("%.6f,%.6f", originLng, originLat))
	query.Set("destination", fmt.Sprintf("%.6f,%.6f", destinationLng, destinationLat))
	query.Set("extensions", "base")
	query.Set("output", "JSON")
	query.Set("strategy", "0")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://restapi.amap.com/v3/direction/driving?"+query.Encode(), nil)
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
		return nil, fmt.Errorf("amap status=%d body=%s", resp.StatusCode, string(body))
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
		return nil, fmt.Errorf("decode amap route response: %w", err)
	}
	if result.Status != "1" {
		return nil, fmt.Errorf("amap route api failed: %s", result.Info)
	}
	if len(result.Route.Paths) == 0 {
		return nil, errors.New("amap route api returned no paths")
	}

	points := make([]model.RoutePoint, 0, 128)
	for _, step := range result.Route.Paths[0].Steps {
		points = append(points, parsePolyline(step.Polyline)...)
	}
	points = dedupePoints(points)
	if len(points) == 0 {
		return nil, errors.New("amap route api returned empty polyline")
	}
	return points, nil
}

func fetchOSRMRoute(ctx context.Context, client *http.Client, baseURL string, originLat, originLng, destinationLat, destinationLng float64) ([]model.RoutePoint, error) {
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
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

	points := make([]model.RoutePoint, 0, len(result.Routes[0].Geometry.Coordinates))
	for _, coordinate := range result.Routes[0].Geometry.Coordinates {
		if len(coordinate) < 2 {
			continue
		}
		latGCJ, lngGCJ := wgs84ToGCJ02(coordinate[1], coordinate[0])
		points = append(points, model.RoutePoint{
			Lat: latGCJ,
			Lng: lngGCJ,
		})
	}
	points = dedupePoints(points)
	if len(points) == 0 {
		return nil, errors.New("osrm route api returned empty geometry")
	}
	return points, nil
}

func parsePolyline(polyline string) []model.RoutePoint {
	segments := strings.Split(strings.TrimSpace(polyline), ";")
	points := make([]model.RoutePoint, 0, len(segments))
	for _, segment := range segments {
		coords := strings.Split(strings.TrimSpace(segment), ",")
		if len(coords) != 2 {
			continue
		}

		lng, err := strconvParseFloat(coords[0])
		if err != nil {
			continue
		}
		lat, err := strconvParseFloat(coords[1])
		if err != nil {
			continue
		}
		points = append(points, model.RoutePoint{Lat: lat, Lng: lng})
	}
	return points
}

func dedupePoints(points []model.RoutePoint) []model.RoutePoint {
	deduped := make([]model.RoutePoint, 0, len(points))
	for _, point := range points {
		if len(deduped) == 0 || !samePoint(deduped[len(deduped)-1], point) {
			deduped = append(deduped, point)
		}
	}
	return deduped
}

func samePoint(a, b model.RoutePoint) bool {
	return math.Abs(a.Lat-b.Lat) < 0.000001 && math.Abs(a.Lng-b.Lng) < 0.000001
}

func strconvParseFloat(value string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(value), 64)
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
	magic = 1 - 0.00669342162296594323*magic*magic
	sqrtMagic := math.Sqrt(magic)
	dLat = (dLat * 180.0) / ((6335552.717000426 * magic) / (sqrtMagic * sqrtMagic) * math.Pi)
	dLng = (dLng * 180.0) / ((6378245.0 / sqrtMagic) * math.Cos(radLat) * math.Pi)
	return dLat, dLng
}

func gcj02ToWGS84(lat, lng float64) (float64, float64) {
	if outOfChina(lat, lng) {
		return lat, lng
	}
	dLat, dLng := deltaGCJ02(lat, lng)
	return lat - dLat, lng - dLng
}

func wgs84ToGCJ02(lat, lng float64) (float64, float64) {
	if outOfChina(lat, lng) {
		return lat, lng
	}
	dLat, dLng := deltaGCJ02(lat, lng)
	return lat + dLat, lng + dLng
}
