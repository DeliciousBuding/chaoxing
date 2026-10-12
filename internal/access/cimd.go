package access

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

func validRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.User != nil || u.Fragment != "" || u.Hostname() == "" || strings.ContainsAny(raw, "\r\n\t\\\"'<> ;") || strings.ContainsAny(u.Host, "*{}") {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1" || u.Hostname() == "localhost"))
}
func validCIMD(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= 2048 && u.Scheme == "https" && strings.EqualFold(u.Hostname(), "chatgpt.com") && (u.Port() == "" || u.Port() == "443") && u.User == nil && u.Fragment == ""
}
func publicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, raw := range []string{"100.64.0.0/10", "0.0.0.0/8", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "::/96", "::ffff:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/32"} {
		if netip.MustParsePrefix(raw).Contains(a) {
			return false
		}
	}
	return true
}

// fetchCIMD pins the checked IP at dial time; TLS still verifies chatgpt.com's
// hostname. No proxy, redirect, or second DNS lookup is permitted.
func fetchCIMD(ctx context.Context, id string) (client, error) {
	var c client
	if !validCIMD(id) {
		return c, errors.New("client metadata URL not allowed")
	}
	tr := &http.Transport{TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil || !strings.EqualFold(host, "chatgpt.com") || port != "443" {
				return nil, errors.New("metadata destination not allowed")
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil || len(ips) == 0 {
				return nil, errors.New("metadata DNS lookup failed")
			}
			for _, ip := range ips {
				if !publicIP(ip.IP) {
					return nil, errors.New("metadata resolved to a reserved address")
				}
			}
			d := net.Dialer{Timeout: 5 * time.Second}
			var last error
			for _, ip := range ips {
				conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				last = err
			}
			return nil, last
		},
	}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, "GET", id, nil)
	if err != nil {
		return c, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return c, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return c, errors.New("metadata HTTP failure")
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
	if err != nil || len(b) > 64<<10 {
		return c, errors.New("metadata body too large")
	}
	var doc struct {
		ID        string   `json:"client_id"`
		Name      string   `json:"client_name"`
		Redirects []string `json:"redirect_uris"`
	}
	if json.Unmarshal(b, &doc) != nil || doc.ID != id || len(doc.Redirects) == 0 || len(doc.Redirects) > 8 {
		return c, errors.New("invalid client metadata")
	}
	for _, raw := range doc.Redirects {
		if !validRedirect(raw) {
			return c, errors.New("invalid metadata redirect")
		}
	}
	if len(doc.Name) > 120 {
		doc.Name = doc.Name[:120]
	}
	return client{ID: id, Name: doc.Name, Redirects: doc.Redirects, Expires: time.Now().Add(10 * time.Minute).Unix()}, nil
}
