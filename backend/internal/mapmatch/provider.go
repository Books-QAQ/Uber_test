package mapmatch

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

type Matcher interface {
	Match(ctx context.Context, points []model.DriverLocation) ([]model.DriverLocation, error)
}

type MatcherConfig struct {
	OSRMBaseURL    string
	RequestTimeout time.Duration
}

type OSRMMatcher struct {
	client      *http.Client
	osrmBaseURL string
}

func NewOSRMMatcher(cfg MatcherConfig) *OSRMMatcher {
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	return &OSRMMatcher{
		client: &http.Client{
			Timeout: timeout,
		},
		osrmBaseURL: strings.TrimRight(strings.TrimSpace(cfg.OSRMBaseURL), "/"),
	}
}

func (m *OSRMMatcher) Match(ctx context.Context, points []model.DriverLocation) ([]model.DriverLocation, error) {
	if m == nil || m.osrmBaseURL == "" {
		return nil, errors.New("osrm match disabled")
	}
	if len(points) < 2 {
		return nil, errors.New("osrm match requires at least 2 points")
	}

	segments := make([]string, 0, len(points))
	timestamps := make([]string, 0, len(points))
	radiuses := make([]string, 0, len(points))
	lastTimestamp := int64(0)
	for _, point := range points {
		latWGS, lngWGS := gcj02ToWGS84(point.Lat, point.Lng)
		segments = append(segments, fmt.Sprintf("%.6f,%.6f", lngWGS, latWGS))

		timestamp := point.Timestamp.Unix()
		if timestamp <= 0 {
			timestamp = time.Now().UTC().Unix()
		}
		if len(timestamps) > 0 && timestamp <= lastTimestamp {
			timestamp = lastTimestamp + 1
		}
		lastTimestamp = timestamp
		timestamps = append(timestamps, strconv.FormatInt(timestamp, 10))

		radius := point.AccuracyM
		if radius <= 0 {
			radius = 15
		}
		radiuses = append(radiuses, strconv.FormatFloat(radius, 'f', 1, 64))
	}

	query := url.Values{}
	query.Set("geometries", "geojson")
	query.Set("overview", "full")
	query.Set("tidy", "true")
	query.Set("gaps", "ignore")
	query.Set("timestamps", strings.Join(timestamps, ";"))
	query.Set("radiuses", strings.Join(radiuses, ";"))

	endpoint := fmt.Sprintf("%s/match/v1/driving/%s?%s", m.osrmBaseURL, strings.Join(segments, ";"), query.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("osrm match status=%d body=%s", resp.StatusCode, string(body))
	}

	var result struct {
		Code        string `json:"code"`
		Tracepoints []struct {
			Location []float64 `json:"location"`
		} `json:"tracepoints"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode osrm match response: %w", err)
	}
	if result.Code != "Ok" {
		return nil, fmt.Errorf("osrm match api failed: %s", result.Code)
	}
	if len(result.Tracepoints) == 0 {
		return nil, errors.New("osrm match api returned no tracepoints")
	}

	matched := make([]model.DriverLocation, 0, len(points))
	for i, point := range points {
		next := point
		if i < len(result.Tracepoints) && len(result.Tracepoints[i].Location) >= 2 {
			latGCJ, lngGCJ := wgs84ToGCJ02(result.Tracepoints[i].Location[1], result.Tracepoints[i].Location[0])
			next.Lat = latGCJ
			next.Lng = lngGCJ
		}
		matched = append(matched, next)
	}

	return matched, nil
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
