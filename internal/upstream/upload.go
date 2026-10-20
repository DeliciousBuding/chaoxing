package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

const (
	panTokenAPI  = "https://pan-yz.chaoxing.com/api/token/uservalid"
	panUploadAPI = "https://pan-yz.chaoxing.com/upload"
)

type UploadResult struct {
	ObjectID  string    `json:"object_id"`
	Filename  string    `json:"filename"`
	FetchedAt time.Time `json:"fetched_at"`
}

func (c *Client) UploadFile(ctx context.Context, filename string, content []byte) (UploadResult, error) {
	filename = filepath.Base(strings.TrimSpace(filename))
	if filename == "" || filename == "." || filename == ".." {
		return UploadResult{}, fault("INVALID_ARGUMENT", "upload filename is required")
	}
	if len(content) == 0 || len(content) > 5*1024*1024 {
		return UploadResult{}, fault("INVALID_ARGUMENT", "upload file must be between 1 byte and 5 MiB")
	}
	if err := c.lock(ctx); err != nil {
		return UploadResult{}, err
	}
	defer c.queries.Release()
	identity, err := c.fetchSignIdentity(ctx)
	if err != nil {
		return UploadResult{}, err
	}
	b, err := c.get(ctx, panTokenAPI)
	if err != nil {
		return UploadResult{}, err
	}
	var tokenPayload struct {
		Result bool   `json:"result"`
		Token  string `json:"_token"`
	}
	if json.Unmarshal(b, &tokenPayload) != nil || !tokenPayload.Result || tokenPayload.Token == "" {
		return UploadResult{}, fault("UPSTREAM_SCHEMA", "pan upload token not recognized")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("puid", identity.UID); err != nil {
		return UploadResult{}, fault("INTERNAL", "upload form failed")
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return UploadResult{}, fault("INTERNAL", "upload form failed")
	}
	if _, err = part.Write(content); err != nil {
		return UploadResult{}, fault("INTERNAL", "upload form failed")
	}
	if err = writer.Close(); err != nil {
		return UploadResult{}, fault("INTERNAL", "upload form failed")
	}
	uploadURL := panUploadAPI + "?" + url.Values{"_from": {"mobilelearn"}, "_token": {tokenPayload.Token}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &body)
	if err != nil {
		return UploadResult{}, fault("INVALID_URL", "upload endpoint not permitted")
	}
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return UploadResult{}, fault("UPSTREAM_UNAVAILABLE", "upload transport failed")
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if err != nil || len(payload) > 1024*1024 {
		return UploadResult{}, fault("UPSTREAM_UNAVAILABLE", "upload response read failed")
	}
	if err = c.save(resp); err != nil {
		return UploadResult{}, fault("STATE_WRITE_FAILED", "cannot persist session")
	}
	if resp.StatusCode != http.StatusOK {
		return UploadResult{}, fault("UPSTREAM_HTTP", fmt.Sprintf("upload HTTP %d", resp.StatusCode))
	}
	var uploaded struct {
		ObjectID  string `json:"objectid"`
		ObjectID2 string `json:"objectId"`
	}
	if json.Unmarshal(payload, &uploaded) != nil {
		return UploadResult{}, fault("UPSTREAM_SCHEMA", "upload response not recognized")
	}
	objectID := firstNonBlank(uploaded.ObjectID, uploaded.ObjectID2)
	if objectID == "" {
		return UploadResult{}, fault("UPLOAD_REJECTED", "upload did not return an object id")
	}
	return UploadResult{ObjectID: objectID, Filename: filename, FetchedAt: time.Now().UTC()}, nil
}
