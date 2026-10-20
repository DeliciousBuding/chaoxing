// Package captchasolver adapts external slider-captcha recognition providers.
// The current provider is DamaGou; upstream does not know its protocol.
package captchasolver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const damaGouEndpoint = "http://api.damagou.top/apiv1/recognize.html"

// DamaGou is the operator-configured DamaGou client. The userkey is private
// configuration and is never exposed through tool results.
type DamaGou struct {
	UserKey  string
	Endpoint string
	Type     int
	Client   *http.Client
}

type damaGouResponse struct {
	Status string `json:"status"`
	Data   string `json:"data"`
	Msg    string `json:"msg"`
}

func NewDamaGou(userKey string) (*DamaGou, error) {
	if strings.TrimSpace(userKey) == "" {
		return nil, errors.New("damagou userkey is required")
	}
	return &DamaGou{
		UserKey:  strings.TrimSpace(userKey),
		Endpoint: damaGouEndpoint,
		Type:     1004,
		Client:   &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (d *DamaGou) Solve(ctx context.Context, background, _ []byte) (int, error) {
	image := background
	if len(image) == 0 || len(image) > 4*1024*1024 {
		return 0, errors.New("captcha image size is invalid")
	}
	form := url.Values{
		"userkey": {d.UserKey},
		"type":    {strconv.Itoa(d.Type)},
		"isJson":  {"2"},
		"image":   {base64.StdEncoding.EncodeToString(image)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, errors.New("damagou request could not be created")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := d.Client.Do(req)
	if err != nil {
		return 0, errors.New("damagou transport failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
	if err != nil || len(body) > 64*1024 {
		return 0, errors.New("damagou response read failed")
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("damagou HTTP %d", resp.StatusCode)
	}
	var payload damaGouResponse
	if err := json.Unmarshal(body, &payload); err != nil || payload.Status != "0" {
		return 0, errors.New("damagou rejected the captcha")
	}
	data := strings.TrimSpace(payload.Data)
	if data == "" {
		return 0, errors.New("damagou returned no coordinate")
	}
	if comma := strings.IndexByte(data, ','); comma >= 0 {
		data = data[:comma]
	}
	x, err := strconv.Atoi(data)
	if err != nil || x < -8 || x > 280 {
		return 0, errors.New("damagou coordinate is invalid")
	}
	return x, nil
}
