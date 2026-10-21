package upstream

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/signaudit"
)

const egressProbeURL = "https://www.cloudflare.com/cdn-cgi/trace"

func (c *Client) recordSignAudit(req SignRequest, result *SignResult, signErr error, trace *signAuditTrace) {
	if c.config.SignAudit == nil {
		return
	}
	method := result.Method
	if method == "" {
		method = requestedSignMethod(req)
	}
	state := result.State
	code := ""
	if signErr != nil {
		state, code = "failed", SafeFault(signErr).Code
	} else if state == "" {
		state = "unknown"
	}
	address := result.Address
	latitude, longitude := result.Latitude, result.Longitude
	if trace != nil {
		if address == "" {
			address = trace.Address
		}
		if latitude == nil {
			latitude, longitude = trace.Latitude, trace.Longitude
		}
	}
	if address == "" {
		address = strings.TrimSpace(req.Address)
	}
	if latitude == nil && req.Latitude != nil {
		latitude, longitude = req.Latitude, req.Longitude
	}
	result.Address, result.Latitude, result.Longitude = address, latitude, longitude
	egressIP, egressSource := result.EgressIP, result.EgressSource
	if egressIP == "" {
		egressIP, egressSource = c.probeEgressIP()
		result.EgressIP, result.EgressSource = egressIP, egressSource
	}
	record := signaudit.Record{
		AccountKey: c.config.EgressAccountKey, CourseID: req.CourseID, ClassID: req.ClassID,
		ActivityID: req.ActivityID, Method: method, State: state, Code: code,
		Verified: result.Verified, EgressIP: egressIP, EgressSource: egressSource,
		Address: address, Latitude: latitude, Longitude: longitude, CreatedAt: time.Now().UTC(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.config.SignAudit.SaveSignAudit(ctx, record); err != nil {
		log.Printf("sign audit write failed: %s", SafeFault(err).Code)
	}
}

func requestedSignMethod(req SignRequest) string {
	switch {
	case req.FaceObjectID != "":
		return "face"
	case req.PhotoObjectID != "":
		return "photo"
	case req.QRPayload != "":
		return "qrcode"
	case req.SignCode != "":
		return "code"
	case req.Latitude != nil:
		return "location"
	default:
		return "normal"
	}
}

// probeEgressIP asks a neutral endpoint what public address the same sticky
// egress path presents. It is best-effort: a probe failure never fails or
// delays the sign-in decision.
func (c *Client) probeEgressIP() (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c.requestContext(ctx), http.MethodGet, egressProbeURL, nil)
	if err != nil {
		return "", "unavailable"
	}
	req.Header.Set("User-Agent", "chaoxing-egress-probe/1")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", "unavailable"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "unavailable"
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return "", "unavailable"
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ip=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "ip="))
		if net.ParseIP(value) != nil {
			return value, "cloudflare-trace"
		}
	}
	return "", "unavailable"
}
