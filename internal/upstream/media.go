package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	maxMediaBytes    = 2 << 20
	maxMediaRedirect = 3
	avatarHost       = "photo.chaoxing.com"
	courseImageHost  = "p.ananas.chaoxing.com"
)

// Profile is the minimal identity projection used by the management UI.
// Credentials, UID/PUID and device identity never leave the upstream client.
type Profile struct {
	DisplayName     string `json:"display_name"`
	AvatarAvailable bool   `json:"avatar_available"`
}

// Profile reads the cached upstream identity under the same query admission
// gate as every other upstream read.
func (c *Client) Profile(ctx context.Context) (Profile, error) {
	if err := c.lock(ctx); err != nil {
		return Profile{}, err
	}
	defer c.queries.Release()
	identity, err := c.fetchSignIdentity(ctx)
	if err != nil {
		return Profile{}, err
	}
	return Profile{
		DisplayName:     strings.TrimSpace(identity.Name),
		AvatarAvailable: identity.PUID != "" || identity.UID != "",
	}, nil
}

// Avatar returns image bytes for the current account's Chaoxing avatar.
func (c *Client) Avatar(ctx context.Context) ([]byte, string, error) {
	if err := c.lock(ctx); err != nil {
		return nil, "", err
	}
	identity, err := c.fetchSignIdentity(ctx)
	c.queries.Release()
	if err != nil {
		return nil, "", err
	}
	puid := strings.TrimSpace(identity.PUID)
	if puid == "" {
		puid = strings.TrimSpace(identity.UID)
	}
	if puid == "" {
		return nil, "", fault("NOT_FOUND", "account avatar is unavailable")
	}
	avatarURL := "https://" + avatarHost + "/p/" + url.PathEscape(puid) + "_120?flag=1&psize=120_120c&ext=jpg&t=" +
		fmt.Sprintf("%d", time.Now().Unix())
	return c.fetchMedia(ctx, avatarURL)
}

// CourseCover resolves a cover URL from the verified course list and fetches
// it through the bounded media reader. Callers cannot provide arbitrary URLs.
func (c *Client) CourseCover(ctx context.Context, courseID, classID string) ([]byte, string, error) {
	if err := c.lock(ctx); err != nil {
		return nil, "", err
	}
	courses, err := c.fetchCourses(ctx)
	c.queries.Release()
	if err != nil {
		return nil, "", err
	}
	for _, course := range courses {
		if course.ID != courseID || course.ClassID != classID {
			continue
		}
		if course.ImageURL == "" {
			return nil, "", fault("NOT_FOUND", "course cover is unavailable")
		}
		return c.fetchMedia(ctx, course.ImageURL)
	}
	return nil, "", fault("NOT_FOUND", "course was not found in the verified course list")
}

func allowedCourseImageURL(raw string) string {
	parsed, ok := mediaURL(raw, courseImageHost)
	if !ok {
		return ""
	}
	return parsed.String()
}

func mediaURL(raw, host string) (*url.URL, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Fragment != "" {
		return nil, false
	}
	if parsed.Scheme == "http" {
		parsed.Scheme = "https"
	}
	if parsed.Scheme != "https" {
		return nil, false
	}
	if !strings.EqualFold(parsed.Hostname(), host) || parsed.Port() != "" {
		return nil, false
	}
	if path.Clean(parsed.Path) != parsed.Path || parsed.Path == "" || !strings.HasPrefix(parsed.Path, "/") {
		return nil, false
	}
	return parsed, true
}

func (c *Client) fetchMedia(ctx context.Context, raw string) ([]byte, string, error) {
	parsed, ok := mediaURL(raw, avatarHost)
	if !ok {
		parsed, ok = mediaURL(raw, courseImageHost)
	}
	if !ok {
		return nil, "", fault("INVALID_URL", "media host is not allowlisted")
	}
	client := &http.Client{
		Transport: c.http.Transport,
		Timeout:   12 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxMediaRedirect {
				return errors.New("media redirect limit")
			}
			if req.URL.Scheme != "https" {
				return errors.New("media redirect must use HTTPS")
			}
			if _, ok := mediaURL(req.URL.String(), avatarHost); !ok {
				if _, ok := mediaURL(req.URL.String(), courseImageHost); !ok {
					return errors.New("media redirect host is not allowlisted")
				}
			}
			return nil
		},
	}
	request, err := http.NewRequestWithContext(c.requestContext(ctx), http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", fault("UPSTREAM_UNAVAILABLE", "media request could not be created")
	}
	request.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg")
	request.Header.Set("User-Agent", c.userAgent())
	request.Header.Set("Referer", "https://mooc1.chaoxing.com/")
	response, err := client.Do(request)
	if err != nil {
		return nil, "", fault("UPSTREAM_UNAVAILABLE", "media request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", fault("UPSTREAM_HTTP", "media request was rejected")
	}
	if response.ContentLength > maxMediaBytes {
		return nil, "", fault("RESPONSE_TOO_LARGE", "media response exceeds the size limit")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxMediaBytes+1))
	if err != nil {
		return nil, "", fault("UPSTREAM_UNAVAILABLE", "media response could not be read")
	}
	if len(body) == 0 || len(body) > maxMediaBytes {
		return nil, "", fault("RESPONSE_TOO_LARGE", "media response exceeds the size limit")
	}
	declared := normalizeImageType(response.Header.Get("Content-Type"))
	detected := normalizeImageType(http.DetectContentType(body))
	if detected == "" || (declared != "" && declared != detected) {
		return nil, "", fault("UPSTREAM_SCHEMA", "media response is not a supported image")
	}
	return body, detected, nil
}

func normalizeImageType(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
	switch value {
	case "image/jpeg", "image/jpg":
		return "image/jpeg"
	case "image/png":
		return "image/png"
	case "image/webp":
		return "image/webp"
	default:
		return ""
	}
}
