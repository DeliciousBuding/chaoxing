package upstream

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	captchaID           = "Qt9FIw9o4pwRjOyqM6yizZBh682qN2TU"
	captchaConfAPI      = "https://captcha.chaoxing.com/captcha/get/conf"
	captchaImageAPI     = "https://captcha.chaoxing.com/captcha/get/verification/image"
	captchaResultAPI    = "https://captcha.chaoxing.com/captcha/check/verification/result"
	captchaType         = "slide"
	captchaVersion      = "1.1.20"
	captchaChallengeTTL = 10 * time.Minute
)

type signCaptchaChallenge struct {
	ActivityID  string    `json:"activity_id"`
	CourseID    string    `json:"course_id"`
	ClassID     string    `json:"class_id"`
	Enc2        string    `json:"-"`
	CaptchaKey  string    `json:"-"`
	IV          string    `json:"-"`
	Token       string    `json:"-"`
	Validate    string    `json:"-"`
	ShadeImage  string    `json:"shade_image"`
	CutoutImage string    `json:"cutout_image"`
	CreatedAt   time.Time `json:"created_at"`
}

func (c *Client) storeCaptchaChallenge(activity Activity, enc2 string) {
	if c.captchaChallenges == nil {
		c.captchaChallenges = map[string]signCaptchaChallenge{}
	}
	if len(c.captchaChallenges) >= 32 {
		oldestKey, oldest := "", time.Now()
		for key, challenge := range c.captchaChallenges {
			if oldestKey == "" || challenge.CreatedAt.Before(oldest) {
				oldestKey, oldest = key, challenge.CreatedAt
			}
		}
		delete(c.captchaChallenges, oldestKey)
	}
	c.captchaChallenges[activity.ID] = signCaptchaChallenge{
		ActivityID: activity.ID, CourseID: activity.CourseID, ClassID: activity.ClassID,
		Enc2: enc2, CreatedAt: time.Now().UTC(),
	}
}

func (c *Client) getSignCaptcha(ctx context.Context, args map[string]any) (any, error) {
	activityID := str(args, "activity_id")
	challenge, ok := c.captchaChallenges[activityID]
	if !ok {
		return nil, fault("NOT_FOUND", "no captcha challenge; call sign_activity first")
	}
	if time.Since(challenge.CreatedAt) > captchaChallengeTTL {
		delete(c.captchaChallenges, activityID)
		return nil, fault("CAPTCHA_REQUIRED", "captcha challenge expired; call sign_activity again")
	}
	if challenge.ShadeImage != "" {
		return captchaEnvelope(challenge), nil
	}
	identity, err := c.fetchSignIdentity(ctx)
	if err != nil {
		return nil, err
	}
	confTime, err := c.captchaConfTime(ctx)
	if err != nil {
		return nil, err
	}
	captchaKey := md5Hex(fmt.Sprintf("%d%s", confTime, randomUUID()))
	iv := md5Hex(captchaID + captchaType + strconv.FormatInt(time.Now().UnixMilli(), 10) + randomUUID())
	token := md5Hex(fmt.Sprintf("%d%s%s%s", confTime, captchaID, captchaType, captchaKey)) + ":" + strconv.FormatInt(confTime+300000, 10)
	referer := preSignReferer(challenge, identity.UID)
	q := orderedQuery(
		"callback", "cx_captcha_function", "captchaId", captchaID, "type", captchaType,
		"version", captchaVersion, "captchaKey", captchaKey, "token", token,
		"referer", referer, "iv", iv, "_", strconv.FormatInt(time.Now().UnixMilli(), 10),
	)
	body, err := c.captchaGet(ctx, captchaImageAPI+"?"+q, referer)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Token string `json:"token"`
		Image struct {
			ShadeImage  string `json:"shadeImage"`
			CutoutImage string `json:"cutoutImage"`
		} `json:"imageVerificationVo"`
	}
	if err := decodeJSONP(body, &payload); err != nil || payload.Token == "" || payload.Image.ShadeImage == "" || payload.Image.CutoutImage == "" {
		return nil, fault("UPSTREAM_SCHEMA", "captcha image response not recognized")
	}
	challenge.CaptchaKey, challenge.IV, challenge.Token = captchaKey, iv, payload.Token
	challenge.ShadeImage, challenge.CutoutImage = payload.Image.ShadeImage, payload.Image.CutoutImage
	c.captchaChallenges[activityID] = challenge
	return captchaEnvelope(challenge), nil
}

func (c *Client) answerSignCaptcha(ctx context.Context, args map[string]any) (any, error) {
	activityID := str(args, "activity_id")
	challenge, ok := c.captchaChallenges[activityID]
	if !ok || challenge.ShadeImage == "" {
		return nil, fault("NOT_FOUND", "no image captcha challenge; call get_sign_captcha first")
	}
	if time.Since(challenge.CreatedAt) > captchaChallengeTTL {
		delete(c.captchaChallenges, activityID)
		return nil, fault("CAPTCHA_REQUIRED", "captcha challenge expired; call sign_activity again")
	}
	if challenge.Validate != "" {
		return map[string]any{"validated": true, "activity_id": activityID, "complete": true, "coverage": "captcha_validation", "fetched_at": time.Now().UTC()}, nil
	}
	identity, err := c.fetchSignIdentity(ctx)
	if err != nil {
		return nil, err
	}
	x, ok := floatArg(args, "x_position")
	if !ok || x < -8 || x > 280 {
		return nil, fault("INVALID_ARGUMENT", "x_position must be between -8 and 280")
	}
	textClickArr, _ := json.Marshal([]map[string]int{{"x": int(x)}})
	referer := preSignReferer(challenge, identity.UID)
	q := orderedQuery(
		"captchaId", captchaID, "type", captchaType, "token", challenge.Token,
		"textClickArr", string(textClickArr), "coordinate", "[]", "runEnv", "10",
		"version", captchaVersion, "t", "a", "iv", challenge.IV,
		"_", strconv.FormatInt(time.Now().UnixMilli(), 10),
	)
	body, err := c.captchaGet(ctx, captchaResultAPI+"?"+q, referer)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Error     int    `json:"error"`
		Result    bool   `json:"result"`
		ExtraData string `json:"extraData"`
	}
	if err := decodeJSONP(body, &payload); err != nil {
		return nil, fault("UPSTREAM_SCHEMA", "captcha result not recognized")
	}
	if payload.Error == 1 || !payload.Result {
		return nil, fault("SIGN_CAPTCHA_REJECTED", "captcha answer was not accepted")
	}
	var extra struct {
		Validate string `json:"validate"`
	}
	if json.Unmarshal([]byte(payload.ExtraData), &extra) != nil || extra.Validate == "" {
		return nil, fault("UPSTREAM_SCHEMA", "captcha validation token not recognized")
	}
	challenge.Validate = extra.Validate
	c.captchaChallenges[activityID] = challenge
	return map[string]any{
		"validated": true, "activity_id": activityID, "complete": true,
		"coverage": "captcha_validation", "fetched_at": time.Now().UTC(),
	}, nil
}

// solveSignCaptcha fetches the challenge image, asks the configured solver for
// one coordinate, submits it, and leaves the validated challenge in memory.
func (c *Client) solveSignCaptcha(ctx context.Context, req SignRequest) error {
	args := map[string]any{
		"course_id": req.CourseID, "class_id": req.ClassID, "activity_id": req.ActivityID,
	}
	if _, err := c.getSignCaptcha(ctx, args); err != nil {
		return err
	}
	challenge, ok := c.captchaChallenges[req.ActivityID]
	if !ok || challenge.ShadeImage == "" {
		return fault("CAPTCHA_SOLVER_FAILED", "captcha image is unavailable")
	}
	background, err := c.captchaGet(ctx, challenge.ShadeImage, preSignReferer(challenge, ""))
	if err != nil || len(background) == 0 {
		return fault("CAPTCHA_SOLVER_FAILED", "captcha background could not be downloaded")
	}
	cutout, err := c.captchaGet(ctx, challenge.CutoutImage, preSignReferer(challenge, ""))
	if err != nil || len(cutout) == 0 {
		return fault("CAPTCHA_SOLVER_FAILED", "captcha cutout could not be downloaded")
	}
	x, err := c.config.CaptchaSolver.Solve(ctx, background, cutout)
	if err != nil || x < -8 || x > 280 {
		return fault("CAPTCHA_SOLVER_FAILED", "captcha solver returned an invalid coordinate")
	}
	args["x_position"] = float64(x)
	if _, err = c.answerSignCaptcha(ctx, args); err != nil {
		return err
	}
	return nil
}
func (c *Client) captchaConfTime(ctx context.Context) (int64, error) {
	q := orderedQuery("callback", "cx_captcha_function", "captchaId", captchaID, "_", strconv.FormatInt(time.Now().UnixMilli(), 10))
	body, err := c.captchaGet(ctx, captchaConfAPI+"?"+q, "")
	if err != nil {
		return 0, err
	}
	var payload struct {
		Time int64 `json:"t"`
	}
	if err := decodeJSONP(body, &payload); err != nil || payload.Time == 0 {
		return 0, fault("UPSTREAM_SCHEMA", "captcha configuration not recognized")
	}
	return payload.Time, nil
}

func (c *Client) captchaGet(ctx context.Context, endpoint, referer string) ([]byte, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || !safeURL(parsed) {
		return nil, fault("INVALID_URL", "captcha endpoint not permitted")
	}
	req, err := http.NewRequestWithContext(c.requestContext(ctx), http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fault("INVALID_URL", "captcha endpoint not permitted")
	}
	req.Header.Set("User-Agent", c.userAgent())
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fault("UPSTREAM_UNAVAILABLE", "captcha transport failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if err != nil {
		return nil, fault("UPSTREAM_UNAVAILABLE", "captcha response read failed")
	}
	if len(body) > 1024*1024 {
		return nil, fault("RESPONSE_TOO_LARGE", "captcha response exceeds limit")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, httpStatusFault(resp.StatusCode, "captcha")
	}
	return body, nil
}

func captchaEnvelope(challenge signCaptchaChallenge) map[string]any {
	return map[string]any{"captcha": map[string]any{
		"activity_id": challenge.ActivityID, "course_id": challenge.CourseID,
		"class_id": challenge.ClassID, "type": captchaType, "version": captchaVersion,
		"shade_image": challenge.ShadeImage, "cutout_image": challenge.CutoutImage,
		"expires_at": challenge.CreatedAt.Add(captchaChallengeTTL), "complete": true,
		"coverage": "captcha_challenge", "fetched_at": time.Now().UTC(),
	}, "complete": true, "coverage": "captcha_challenge", "fetched_at": time.Now().UTC()}
}

func preSignReferer(challenge signCaptchaChallenge, uid string) string {
	if uid == "" {
		return "https://mobilelearn.chaoxing.com/newsign/preSign"
	}
	return preSignAPI + "?" + orderedQuery(
		"courseId", challenge.CourseID, "classId", challenge.ClassID,
		"activePrimaryId", challenge.ActivityID, "general", "1", "sys", "1",
		"ls", "1", "appType", "15", "uid", uid, "isTeacherViewOpen", "0",
	)
}

func decodeJSONP(body []byte, v any) error {
	value := strings.TrimSpace(string(body))
	if !strings.HasPrefix(value, "cx_captcha_function(") || !strings.HasSuffix(value, ")") {
		return fmt.Errorf("not captcha JSONP")
	}
	return json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(value, "cx_captcha_function("), ")")), v)
}

func md5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
