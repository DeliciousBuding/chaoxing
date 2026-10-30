package upstream

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	faceEnrollAPI = "https://passport2-api.chaoxing.com/api/changefaceid"
	faceCheckAPI  = "https://mobilelearn.chaoxing.com/pptSign/check-face-result"
)

type FaceEnrollResult struct {
	Enrolled  bool      `json:"enrolled"`
	ObjectID  string    `json:"object_id"`
	FetchedAt time.Time `json:"fetched_at"`
}

func (c *Client) EnrollFace(ctx context.Context, objectID string) (FaceEnrollResult, error) {
	if err := c.lock(ctx); err != nil {
		return FaceEnrollResult{}, err
	}
	defer c.queries.Release()
	return c.enrollFace(ctx, objectID)
}

func (c *Client) enrollFace(ctx context.Context, objectID string) (FaceEnrollResult, error) {
	objectID = strings.TrimSpace(objectID)
	if objectID == "" || len(objectID) > 128 {
		return FaceEnrollResult{}, fault("INVALID_ARGUMENT", "face object id is invalid")
	}
	identity, err := c.fetchSignIdentity(ctx)
	if err != nil {
		return FaceEnrollResult{}, err
	}
	q := orderedQuery("objectid", objectID, "uid", identity.signUID(), "fid", identity.FID)
	b, err := c.get(ctx, faceEnrollAPI+"?"+q)
	if err != nil {
		return FaceEnrollResult{}, err
	}
	var payload struct {
		Result bool `json:"result"`
		Status bool `json:"status"`
	}
	if json.Unmarshal(b, &payload) != nil || (!payload.Result && !payload.Status) {
		return FaceEnrollResult{}, fault("FACE_ENROLL_REJECTED", "face enrollment was not accepted")
	}
	return FaceEnrollResult{Enrolled: true, ObjectID: objectID, FetchedAt: time.Now().UTC()}, nil
}

func (c *Client) faceCheck(ctx context.Context, activity Activity, faceObjectID string, identity signIdentity) (string, string, error) {
	if identity.CXCID == "" || identity.SC == "" {
		return "", "", fault("UNSUPPORTED", "device identity did not supply face signing material")
	}
	cxtime := strconv.FormatInt(time.Now().UnixMilli(), 10)
	raw := "LiveDetectionStatus1collectStatus1currentFaceId" + faceObjectID + "cxcid" + identity.CXCID + "cxtime" + cxtime + identity.SC
	sum := md5.Sum([]byte(raw))
	faceResult, err := json.Marshal(map[string]any{
		"currentFaceId": faceObjectID, "collectStatus": 1, "LiveDetectionStatus": 1,
		"cxcid": identity.CXCID, "cxtime": cxtime, "signToken": hex.EncodeToString(sum[:]),
	})
	if err != nil {
		return "", "", fault("INTERNAL", "face result failed")
	}
	q := url.Values{
		"DB_STRATEGY": {"PRIMARY_KEY"}, "STRATEGY_PARA": {"activeId"},
		"activeId": {activity.ID}, "faceResult": {string(faceResult)},
	}
	b, err := c.get(ctx, faceCheckAPI+"?"+q.Encode())
	if err != nil {
		return "", "", err
	}
	var payload struct {
		Status flexInt `json:"status"`
		Enc    string  `json:"enc"`
		Code   string  `json:"code"`
	}
	if json.Unmarshal(b, &payload) != nil || (payload.Status.ok && payload.Status.value != 1) || payload.Enc == "" {
		return "", "", fault("FACE_CHECK_REJECTED", "face liveness check was not accepted")
	}
	return payload.Enc, payload.Code, nil
}
