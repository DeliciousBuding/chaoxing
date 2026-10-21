package location

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaiduGeocodeBase = "https://api.map.baidu.com"

// BaiduGeocoder adapts Baidu's geocoding endpoints without persisting the key.
type BaiduGeocoder struct {
	ak      string
	baseURL string
	client  *http.Client
}

func NewBaiduGeocoder(ak string, timeout time.Duration) *BaiduGeocoder {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &BaiduGeocoder{ak: strings.TrimSpace(ak), baseURL: defaultBaiduGeocodeBase, client: &http.Client{Timeout: timeout}}
}

func (g *BaiduGeocoder) Geocode(ctx context.Context, address string) (Location, error) {
	if g == nil || g.ak == "" {
		return Location{}, errors.New("baidu geocoder is not configured")
	}
	address = strings.TrimSpace(address)
	if address == "" || len(address) > 256 {
		return Location{}, errors.New("geocoding address is required and must be at most 256 bytes")
	}
	endpoint := g.baseURL + "/geocoding/v3/"
	query := url.Values{"address": {address}, "output": {"json"}, "ak": {g.ak}}
	var result struct {
		Status  int    `json:"status"`
		Message string `json:"message"`
		Result  struct {
			Location struct {
				Latitude  float64 `json:"lat"`
				Longitude float64 `json:"lng"`
			} `json:"location"`
			Level string `json:"level"`
		} `json:"result"`
	}
	if err := g.call(ctx, endpoint, query, &result); err != nil {
		return Location{}, err
	}
	if result.Status != 0 || !finiteCoordinate(result.Result.Location.Latitude, result.Result.Location.Longitude) {
		return Location{}, errors.New("baidu geocoding failed")
	}
	return Location{Latitude: result.Result.Location.Latitude, Longitude: result.Result.Location.Longitude, Address: address}, nil
}

func (g *BaiduGeocoder) ReverseGeocode(ctx context.Context, latitude, longitude float64) (string, error) {
	if g == nil || g.ak == "" {
		return "", errors.New("baidu geocoder is not configured")
	}
	if !finiteCoordinate(latitude, longitude) || latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
		return "", errors.New("reverse geocoding coordinates are outside range")
	}
	endpoint := g.baseURL + "/reverse_geocoding/v3/"
	query := url.Values{
		"location": {fmt.Sprintf("%.8f,%.8f", latitude, longitude)},
		"output":   {"json"},
		"ak":       {g.ak},
	}
	var result struct {
		Status  int    `json:"status"`
		Message string `json:"message"`
		Result  struct {
			FormattedAddress string `json:"formatted_address"`
		} `json:"result"`
	}
	if err := g.call(ctx, endpoint, query, &result); err != nil {
		return "", err
	}
	if result.Status != 0 || strings.TrimSpace(result.Result.FormattedAddress) == "" {
		return "", errors.New("baidu reverse geocoding failed")
	}
	return strings.TrimSpace(result.Result.FormattedAddress), nil
}

func (g *BaiduGeocoder) call(ctx context.Context, endpoint string, query url.Values, result any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return errors.New("baidu geocoding request is invalid")
	}
	request.Header.Set("Accept", "application/json")
	response, err := g.client.Do(request)
	if err != nil {
		return errors.New("baidu geocoding request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return errors.New("baidu geocoding request failed")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 128<<10))
	if err != nil {
		return errors.New("baidu geocoding response is unreadable")
	}
	if err := json.Unmarshal(body, result); err != nil {
		return errors.New("baidu geocoding response is invalid")
	}
	return nil
}
