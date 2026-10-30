package upstream

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	activeInfoAPI = "https://mobilelearn.chaoxing.com/v2/apis/active/getPPTActiveInfo"
	attendInfoAPI = "https://mobilelearn.chaoxing.com/v2/apis/sign/getAttendInfo"
	preSignAPI    = "https://mobilelearn.chaoxing.com/newsign/preSign"
	analysisAPI   = "https://mobilelearn.chaoxing.com/pptSign/analysis?vs=1&DB_STRATEGY=RANDOM"
	analysis2API  = "https://mobilelearn.chaoxing.com/pptSign/analysis2?DB_STRATEGY=RANDOM"
	stuSignAPI    = "https://mobilelearn.chaoxing.com/pptSign/stuSignajax"
	checkCodeAPI  = "https://mobilelearn.chaoxing.com/widget/sign/pcStuSignController/checkSignCode"
)

var analysisCodePattern = regexp.MustCompile(`code='\+'([a-f0-9]+)'`)

// SignRequest identifies one account-owned activity. A class-level preset can
// supply exact coordinates; the sign layer still applies the reference client's
// small random offset before submission.
type SignRequest struct {
	CourseID        string
	ClassID         string
	ActivityID      string
	Latitude        *float64
	Longitude       *float64
	Address         string
	SignCode        string
	SignEnc2        string
	CaptchaValidate string
	QRPayload       string
	PhotoObjectID   string
	FaceObjectID    string
	FaceEnc         string
}

type SignResult struct {
	ActivityID   string    `json:"activity_id"`
	CourseID     string    `json:"course_id"`
	ClassID      string    `json:"class_id"`
	Method       string    `json:"method"`
	State        string    `json:"state"`
	Verified     bool      `json:"verified"`
	Submitted    bool      `json:"submitted"`
	StateSource  string    `json:"state_source,omitempty"`
	Address      string    `json:"address,omitempty"`
	Latitude     *float64  `json:"latitude,omitempty"`
	Longitude    *float64  `json:"longitude,omitempty"`
	EgressIP     string    `json:"egress_ip,omitempty"`
	EgressSource string    `json:"egress_ip_source,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
}

type signAuditTrace struct {
	AttemptID  string
	ActivityID string
	Submitted  bool
	Method     string
	Address    string
	Latitude   *float64
	Longitude  *float64
}

type activeInfo struct {
	OtherID       flexInt  `json:"otherId"`
	IfPhoto       flexInt  `json:"ifphoto"`
	IfOpenAddress flexInt  `json:"ifopenAddress"`
	IfNeedVCode   flexInt  `json:"ifNeedVCode"`
	OpenCheckFace flexInt  `json:"openCheckFaceFlag"`
	LocationLat   *float64 `json:"locationLatitude"`
	LocationLng   *float64 `json:"locationLongitude"`
	LocationRange flexInt  `json:"locationRange"`
}

type locationResultPayload struct {
	Result    int     `json:"result"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Address   string  `json:"address"`
	MockData  string  `json:"mockData,omitempty"`
}

// SignActivity is the watcher-facing boundary. It uses the same account queue
// as MCP/CLI calls so sign state cannot race a concurrent query.
func (c *Client) SignActivity(ctx context.Context, req SignRequest) (SignResult, error) {
	if err := c.lock(ctx); err != nil {
		c.recordSignAudit(req, &SignResult{}, err, nil)
		return SignResult{}, err
	}
	trace := &signAuditTrace{}
	result, signErr := func() (SignResult, error) {
		defer c.queries.Release()
		return c.signActivityWithSolver(ctx, req, trace)
	}()
	c.recordSignAudit(req, &result, signErr, trace)
	return result, signErr
}

// signActivityLocked is used by Call, which already owns the account query
// slot. It keeps manual MCP/CLI sign-in on the same audit path as the watcher.
func (c *Client) signActivityLocked(ctx context.Context, req SignRequest) (SignResult, error) {
	trace := &signAuditTrace{}
	result, err := c.signActivityWithSolver(ctx, req, trace)
	c.recordSignAudit(req, &result, err, trace)
	return result, err
}

// signActivityWithSolver runs the sign state machine and, when a solver is
// configured, resolves one slider challenge before a single retry.
func (c *Client) signActivityWithSolver(ctx context.Context, req SignRequest, trace *signAuditTrace) (SignResult, error) {
	trace.AttemptID = strconv.FormatInt(time.Now().UnixNano(), 36)
	trace.ActivityID = req.ActivityID
	ctx = context.WithValue(ctx, signTraceKey{}, trace)
	result, err := c.signActivity(ctx, req, trace)
	if err == nil || c.config.CaptchaSolver == nil {
		return result, err
	}
	if SafeFault(err).Code != "CAPTCHA_REQUIRED" {
		return result, err
	}
	if solveErr := c.solveSignCaptcha(ctx, req); solveErr != nil {
		return SignResult{}, solveErr
	}
	return c.signActivity(ctx, req, trace)
}

// signActivity submits exactly one selected activity and verifies attendance.
// Callers must already hold the account query slot.
func (c *Client) signActivity(ctx context.Context, req SignRequest, trace *signAuditTrace) (SignResult, error) {
	if strings.TrimSpace(req.CourseID) == "" || strings.TrimSpace(req.ClassID) == "" || strings.TrimSpace(req.ActivityID) == "" {
		return SignResult{}, fault("INVALID_ARGUMENT", "course, class and activity IDs are required")
	}
	if (req.Latitude == nil) != (req.Longitude == nil) {
		return SignResult{}, fault("INVALID_ARGUMENT", "latitude and longitude must be supplied together")
	}
	if req.Latitude != nil && !validCoordinate(*req.Latitude, *req.Longitude) {
		return SignResult{}, fault("INVALID_ARGUMENT", "location is outside range")
	}
	if req.QRPayload != "" {
		qr, err := parseQRCode(req.QRPayload)
		if err != nil {
			return SignResult{}, err
		}
		if qr.AID != "" && qr.AID != req.ActivityID {
			return SignResult{}, fault("INVALID_ARGUMENT", "QR activity does not match the selected activity")
		}
	}
	courses, err := c.fetchCourses(ctx)
	if err != nil {
		return SignResult{}, err
	}
	course, ok := findCourseClass(courses, req.CourseID, req.ClassID)
	if !ok {
		return SignResult{}, fault("NOT_FOUND", "course/class is not in the accessible list")
	}
	activity, ok := c.recentSignActivity(course, req.ActivityID)
	if !ok {
		rows, err := c.fetchActivities(ctx, course)
		if err != nil {
			return SignResult{}, err
		}
		activity, ok = findActivity(rows, req.ActivityID)
	}
	if !ok || activity.Kind != "signin" {
		return SignResult{}, fault("NOT_FOUND", "activity is not in the current class feed")
	}
	if activity.State != "active" || !activity.Signable {
		return SignResult{}, fault("UNSUPPORTED", "activity is not currently signable")
	}
	info, err := c.fetchActiveInfo(ctx, activity.ID)
	if err != nil {
		return SignResult{}, err
	}
	if !info.OtherID.ok {
		return SignResult{}, fault("UPSTREAM_SCHEMA", "activity sign type is missing")
	}
	if info.OpenCheckFace.ok && info.OpenCheckFace.value == 1 && req.FaceObjectID == "" {
		return SignResult{}, fault("UNSUPPORTED", "activity requires an enrolled face object id")
	}
	// A challenge is authoritative even when the static activity flag is off.
	// Upstream can request captcha dynamically after the first submission.
	if challenge, ok := c.captchaChallenges[activity.ID]; ok {
		if challenge.CourseID != req.CourseID || challenge.ClassID != req.ClassID {
			return SignResult{}, fault("INVALID_ARGUMENT", "captcha class does not match")
		}
		if time.Since(challenge.CreatedAt) > 10*time.Minute {
			delete(c.captchaChallenges, activity.ID)
			return SignResult{}, fault("CAPTCHA_REQUIRED", "captcha challenge expired; call sign_activity again")
		}
		if challenge.Validate == "" {
			return SignResult{}, fault("CAPTCHA_REQUIRED", "answer the captcha challenge before retrying")
		}
		req.SignEnc2, req.CaptchaValidate = challenge.Enc2, challenge.Validate
	}
	// Without a stored challenge, submit once. The reference protocol returns
	// `validate_<enc2>` for a captcha-required activity; blind pre-rejection
	// would make that state unreachable.
	var method string
	switch {
	case info.OpenCheckFace.ok && info.OpenCheckFace.value == 1 && info.OtherID.value == 0 && info.IfPhoto.ok && info.IfPhoto.value == 1:
		return SignResult{}, fault("UNSUPPORTED", "combined photo and face requirements need manual handling")
	case req.FaceObjectID != "" && info.OpenCheckFace.ok && info.OpenCheckFace.value == 1 && (info.OtherID.value == 0 || info.OtherID.value == 4):
		method = "face"
	case info.OpenCheckFace.ok && info.OpenCheckFace.value == 1:
		return SignResult{}, fault("UNSUPPORTED", "face combination is not supported for this sign type")
	case info.OtherID.value == 0 && (!info.IfPhoto.ok || info.IfPhoto.value == 0):
		method = "normal"
	case info.OtherID.value == 0 && info.IfPhoto.ok && info.IfPhoto.value == 1 && req.PhotoObjectID != "":
		method = "photo"
	case info.OtherID.value == 4:
		method = "location"
	case info.OtherID.value == 5 && req.SignCode != "":
		method = "password"
	case info.OtherID.value == 3 && req.SignCode != "":
		method = "gesture"
	case info.OtherID.value == 2 && req.QRPayload != "":
		method = "qrcode"
	default:
		if info.OtherID.value == 3 || info.OtherID.value == 5 {
			return SignResult{}, fault("INVALID_ARGUMENT", "sign_code is required for gesture or password sign-in")
		}
		if info.OtherID.value == 2 {
			return SignResult{}, fault("INVALID_ARGUMENT", "qr_payload is required for QR sign-in")
		}
		return SignResult{}, fault("UNSUPPORTED", "only normal, location, gesture and password sign-in are implemented")
	}
	if (method == "password" || method == "gesture" || method == "qrcode") && info.IfOpenAddress.ok && info.IfOpenAddress.value == 1 && req.Latitude == nil {
		return SignResult{}, fault("INVALID_ARGUMENT", "this sign-in requires operator-supplied location")
	}
	state, err := c.signSelected(ctx, activity, info, req, method, trace)
	if err != nil {
		return SignResult{}, err
	}
	if strings.HasPrefix(state, "validate_") || strings.HasPrefix(state, "checkFace_") {
		enc2 := strings.TrimPrefix(strings.TrimPrefix(state, "validate_"), "checkFace_")
		if enc2 == "" {
			return SignResult{}, fault("UPSTREAM_SCHEMA", "captcha challenge token not recognized")
		}
		if strings.HasPrefix(state, "checkFace_") {
			return SignResult{}, fault("UNSUPPORTED", "activity requires face verification")
		}
		c.storeCaptchaChallenge(activity, enc2)
		return SignResult{}, fault("CAPTCHA_REQUIRED", "captcha challenge is ready")
	}
	delete(c.captchaChallenges, activity.ID)
	if !trace.Submitted {
		method = "none"
	}
	phaseSource := "submission"
	if !trace.Submitted {
		phaseSource = "pre_sign"
	}
	result := SignResult{
		ActivityID: activity.ID, CourseID: activity.CourseID, ClassID: activity.ClassID,
		Method: method, State: state, Submitted: trace.Submitted, StateSource: phaseSource, Address: trace.Address,
		Latitude: trace.Latitude, Longitude: trace.Longitude, FetchedAt: time.Now().UTC(),
	}
	verified, err := c.attendanceVerified(ctx, activity.ID)
	if err != nil {
		return result, &Fault{Code: "SIGN_VERIFICATION_PENDING", RetryAfterSeconds: SafeFault(err).RetryAfterSeconds}
	}
	if !verified {
		return result, fault("SIGN_VERIFICATION_PENDING", "attendance has not confirmed the accepted sign-in")
	}
	result.Verified, result.StateSource = true, "attendance"
	return result, nil
}

// Discovery and submission are separate operations. A recent owned card is
// enough to enter the authoritative preSign flow; signing must not request a
// throttled discovery endpoint again just to rediscover the same card.
func (c *Client) recentSignActivity(course Course, activityID string) (Activity, bool) {
	if cached, ok := c.classActivities[course.ID+":"+course.ClassID]; ok && time.Since(cached.at) < classActivityCacheTTL {
		if activity, found := findActivity(cached.rows, activityID); found {
			return activity, true
		}
	}
	if c.config.GroupActivities && time.Since(c.imActivitiesAt) < time.Minute {
		for _, activity := range c.imActivities {
			if activity.Source == "group" && activity.ID == activityID && activity.CourseID == course.ID && activity.ClassID == course.ClassID {
				return activity, true
			}
		}
	}
	return Activity{}, false
}

// VerifySignActivity confirms a previously accepted attempt without consulting
// a possibly ended/disappeared feed and without ever submitting again.
// The watcher owns the persistent attempt receipt; this boundary owns protocol
// reads and independently rechecks the course/class account scope.
func (c *Client) VerifySignActivity(ctx context.Context, req SignRequest) (SignResult, error) {
	if err := c.lock(ctx); err != nil {
		return SignResult{}, err
	}
	defer c.queries.Release()
	return c.verifySignActivityLocked(ctx, req)
}

func (c *Client) verifySignActivityLocked(ctx context.Context, req SignRequest) (result SignResult, verifyErr error) {
	defer func() { c.recordSignAudit(req, &result, verifyErr, nil) }()
	if req.CourseID == "" || req.ClassID == "" || req.ActivityID == "" {
		return SignResult{}, fault("INVALID_ARGUMENT", "course, class and activity IDs are required")
	}
	courses, err := c.fetchCourses(ctx)
	if err != nil {
		return SignResult{}, err
	}
	if _, ok := findCourseClass(courses, req.CourseID, req.ClassID); !ok {
		return SignResult{}, fault("NOT_FOUND", "course/class is not in the accessible list")
	}
	verified, err := c.attendanceVerified(ctx, req.ActivityID)
	result = SignResult{ActivityID: req.ActivityID, CourseID: req.CourseID, ClassID: req.ClassID, Method: "none", State: "signed", StateSource: "attendance", Verified: verified, FetchedAt: time.Now().UTC()}
	if err == nil && !verified {
		err = fault("SIGN_VERIFICATION_PENDING", "attendance has not confirmed the accepted sign-in")
	}
	return result, err
}

func findCourseClass(courses []Course, courseID, classID string) (Course, bool) {
	for _, course := range courses {
		if course.ID == courseID && course.ClassID == classID {
			return course, true
		}
	}
	return Course{}, false
}

// ListClassActivities is the watcher-facing read boundary: it resolves one
// exact enrolled class and returns its current activity feed.
func (c *Client) ListClassActivities(ctx context.Context, courseID, classID string) ([]Activity, error) {
	if err := c.lock(ctx); err != nil {
		return nil, err
	}
	defer c.queries.Release()
	return c.listClassActivities(ctx, courseID, classID)
}

func (c *Client) listClassActivities(ctx context.Context, courseID, classID string) ([]Activity, error) {
	if err := c.activityDeferred(ctx); err != nil {
		return nil, err
	}
	courses, err := c.fetchCourses(ctx)
	if err != nil {
		return nil, err
	}
	course, ok := findCourseClass(courses, courseID, classID)
	if !ok {
		return nil, fault("NOT_FOUND", "course/class is not in the accessible list")
	}
	return c.fetchActivities(ctx, course)
}

func findActivity(rows []Activity, activityID string) (Activity, bool) {
	for _, row := range rows {
		if row.ID == activityID {
			return row, true
		}
	}
	return Activity{}, false
}

func validCoordinate(latitude, longitude float64) bool {
	return latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180 &&
		!math.IsNaN(latitude) && !math.IsNaN(longitude) && !math.IsInf(latitude, 0) && !math.IsInf(longitude, 0)
}

func (c *Client) fetchActiveInfo(ctx context.Context, activityID string) (activeInfo, error) {
	b, err := c.get(ctx, activeInfoAPI+"?"+orderedQuery("activeId", activityID))
	if err != nil {
		return activeInfo{}, err
	}
	var payload struct {
		Result int        `json:"result"`
		Data   activeInfo `json:"data"`
	}
	if json.Unmarshal(b, &payload) != nil || payload.Result != 1 {
		return activeInfo{}, fault("UPSTREAM_SCHEMA", "activity info not recognized")
	}
	return payload.Data, nil
}

func (c *Client) attendanceVerified(ctx context.Context, activityID string) (bool, error) {
	b, err := c.get(ctx, attendInfoAPI+"?"+orderedQuery("activeId", activityID))
	if err != nil {
		return false, err
	}
	var payload struct {
		Result int `json:"result"`
		Data   struct {
			Status flexInt `json:"status"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &payload) != nil || payload.Result != 1 || !payload.Data.Status.ok {
		return false, fault("UPSTREAM_SCHEMA", "attendance state not recognized")
	}
	switch payload.Data.Status.value {
	case 1, 2, 3, 9:
		return true, nil
	case 0, 4, 5, 10, 11:
		return false, nil
	default:
		return false, fault("UPSTREAM_SCHEMA", "unknown attendance status")
	}
}

func (c *Client) signSelected(ctx context.Context, activity Activity, info activeInfo, req SignRequest, method string, trace *signAuditTrace) (string, error) {
	if trace != nil {
		trace.Method = method
	}
	identity, err := c.fetchSignIdentity(ctx)
	if err != nil {
		return "", err
	}
	if err = c.preSign(ctx, activity, identity); err != nil {
		var f *Fault
		if errors.As(err, &f) && f.Code == "SIGN_ALREADY" {
			return "signed", nil
		}
		return "", err
	}
	if err = c.postAnalysis(ctx, activity.ID); err != nil {
		return "", err
	}
	if method == "normal" || method == "photo" {
		return c.signNormal(ctx, activity, req, identity)
	}
	if method == "face" {
		faceEnc, _, err := c.faceCheck(ctx, activity, req.FaceObjectID, identity)
		if err != nil {
			return "", err
		}
		req.FaceEnc = faceEnc
		return c.signFace(ctx, activity, info, req, identity, trace)
	}
	if method == "password" || method == "gesture" {
		if err := c.checkSignCode(ctx, activity.ID, req.SignCode); err != nil {
			return "", err
		}
		lat, lng := "-1", "-1"
		var position *locationResultPayload
		if req.Latitude != nil {
			p, err := signPosition(activity, info, req, false, trace)
			if err != nil {
				return "", err
			}
			position = &p
			lat, lng = strconv.FormatFloat(p.Latitude, 'f', 6, 64), strconv.FormatFloat(p.Longitude, 'f', 6, 64)
		}
		pairs := []string{
			"activeId", activity.ID, "courseId", activity.CourseID, "uid", identity.signUID(),
			"clientip", "", "latitude", lat, "longitude", lng, "appType", "15",
			"fid", identity.FID, "name", identity.Name, "signCode", req.SignCode,
			"deviceCode", c.device.DeviceCode,
		}
		pairs = appendPosition(pairs, position, true)
		if req.SignEnc2 != "" {
			pairs = append(pairs, "enc2", req.SignEnc2)
		}
		if req.CaptchaValidate != "" {
			pairs = append(pairs, "validate", req.CaptchaValidate)
		}
		q := orderedQuery(pairs...)
		b, err := c.get(ctx, stuSignAPI+"?"+q)
		if err != nil {
			return "", err
		}
		return signResponseState(string(b))
	}
	if method == "qrcode" {
		qr, err := parseQRCode(req.QRPayload)
		if err != nil {
			return "", err
		}
		if err := c.verifyQRCode(ctx, activity.ID, qr); err != nil {
			return "", err
		}
		activeID := activity.ID
		var position *locationResultPayload
		if req.Latitude != nil {
			p, err := signPosition(activity, info, req, false, trace)
			if err != nil {
				return "", err
			}
			position = &p
		}
		pairs := []string{
			"enc", qr.Enc, "name", identity.Name, "activeId", activeID,
			"uid", identity.signUID(), "clientip", "",
		}
		pairs = appendPosition(pairs, position, false)
		pairs = append(pairs, "latitude", "-1", "longitude", "-1",
			"fid", identity.FID, "appType", "15", "deviceCode", c.device.DeviceCode,
			"vpProbability", "-1", "vpStrategy", "", "courseId", activity.CourseID,
		)
		if req.SignEnc2 != "" {
			pairs = append(pairs, "enc2", req.SignEnc2)
		}
		if req.CaptchaValidate != "" {
			pairs = append(pairs, "validate", req.CaptchaValidate)
		}
		pairs = appendPositionResult(pairs, position)
		b, err := c.get(ctx, stuSignAPI+"?"+orderedQuery(pairs...))
		if err != nil {
			return "", err
		}
		return signResponseState(string(b))
	}

	state, err := c.signLocation(ctx, activity, info, req, identity, false, trace)
	if err == nil {
		return state, nil
	}
	var f *Fault
	if !errors.As(err, &f) || f.Code != "SIGN_LOCATION_REJECTED" {
		return "", err
	}
	// The reference client tightens its random offset after errorLocation and
	// lets the operator retry once. Automatic mode performs that single retry.
	return c.signLocation(ctx, activity, info, req, identity, true, trace)
}

func (c *Client) checkSignCode(ctx context.Context, activityID, code string) error {
	b, err := c.get(ctx, checkCodeAPI+"?"+orderedQuery("activeId", activityID, "signCode", code))
	if err != nil {
		return err
	}
	var payload struct {
		Result int `json:"result"`
	}
	if json.Unmarshal(b, &payload) != nil || payload.Result != 1 {
		return fault("SIGN_CODE_REJECTED", "sign code was not accepted")
	}
	return nil
}

func (c *Client) signLocation(ctx context.Context, activity Activity, info activeInfo, req SignRequest, identity signIdentity, tight bool, trace *signAuditTrace) (string, error) {
	position, err := signPosition(activity, info, req, tight, trace)
	if err != nil {
		return "", err
	}
	lat, lng, address := position.Latitude, position.Longitude, position.Address
	pairs := []string{
		"name", identity.Name, "address", address, "activeId", activity.ID,
		"courseId", activity.CourseID, "uid", identity.signUID(), "clientip", "",
		"latitude", formatCoordinate(lat), "longitude", formatCoordinate(lng),
		"fid", identity.FID, "appType", "15", "ifTiJiao", "1",
	}
	if req.CaptchaValidate != "" {
		pairs = append(pairs, "validate", req.CaptchaValidate)
	}
	pairs = append(pairs,
		"deviceCode", c.device.DeviceCode, "vpProbability", "-1", "vpStrategy", "",
	)
	if req.SignEnc2 != "" {
		pairs = append(pairs, "enc2", req.SignEnc2)
	}
	pairs = appendPositionResult(pairs, &position)
	pairs = appendFaceParameters(pairs, req)
	q := orderedQuery(pairs...)
	b, err := c.get(ctx, stuSignAPI+"?"+q)
	if err != nil {
		return "", err
	}
	return signResponseState(string(b))
}

func (c *Client) signFace(ctx context.Context, activity Activity, info activeInfo, req SignRequest, identity signIdentity, trace *signAuditTrace) (string, error) {
	if req.FaceEnc == "" || req.FaceObjectID == "" {
		return "", fault("INVALID_ARGUMENT", "face sign requires explicit image and verification material")
	}
	if info.OtherID.value == 4 {
		return c.signLocation(ctx, activity, info, req, identity, false, trace)
	}
	if info.OtherID.value == 0 {
		return c.signNormal(ctx, activity, req, identity)
	}
	return "", fault("UNSUPPORTED", "face combination is not supported for this sign type")
}

func roundCoordinate(value float64, digits int) float64 {
	factor := math.Pow(10, float64(digits))
	return math.Round(value*factor) / factor
}

func (c *Client) preSign(ctx context.Context, activity Activity, identity signIdentity) error {
	q := orderedQuery(
		"courseId", activity.CourseID, "classId", activity.ClassID, "activePrimaryId", activity.ID,
		"general", "1", "sys", "1", "ls", "1", "appType", "15", "uid", identity.signUID(),
		"isTeacherViewOpen", "0",
	)
	form := url.Values{"ext": {firstNonBlank(activity.Ext, "{}")}}
	b, err := c.authenticatedRead(ctx, "POST", preSignAPI+"?"+q, form)
	if err != nil {
		return err
	}
	body := string(b)
	if strings.Contains(body, "校验失败，未查询到活动数据") {
		return fault("COURSE_ACCESS", "activity is not available for this account")
	}
	if strings.Contains(body, "下次早点哦") {
		return fault("SIGN_EXPIRED", "sign-in has ended")
	}
	if signedStatus(body) {
		return fault("SIGN_ALREADY", "activity is already signed")
	}
	return nil
}

func signedStatus(body string) bool {
	n, ok := primaryAttendanceStatus(body)
	return ok && (n == 1 || n == 2 || n == 3 || n == 9)
}

func (c *Client) postAnalysis(ctx context.Context, activityID string) error {
	b, err := c.get(ctx, analysisAPI+"&"+orderedQuery("aid", activityID))
	if err != nil {
		return err
	}
	m := analysisCodePattern.FindSubmatch(b)
	if m == nil {
		return fault("UPSTREAM_SCHEMA", "sign analysis code not recognized")
	}
	_, err = c.get(ctx, analysis2API+"&"+orderedQuery("code", string(m[1])))
	return err
}

func signResponseState(body string) (string, error) {
	body = strings.TrimSpace(body)
	switch {
	case body == "success":
		return "signed", nil
	case body == "您已签到过了":
		return "already_signed", nil
	case body == "success2":
		return "", fault("SIGN_EXPIRED", "sign-in has ended")
	case body == "签到失败，请重新扫描。":
		return "", fault("SIGN_QR_EXPIRED", "QR code is expired")
	case strings.HasPrefix(body, "validate_"), strings.HasPrefix(body, "[face]"), strings.HasPrefix(body, "checkFace_"):
		if strings.HasPrefix(body, "validate_") || strings.HasPrefix(body, "checkFace_") {
			return body, nil
		}
		return "", fault("UNSUPPORTED", "activity requires manual verification")
	case strings.HasPrefix(body, "errorLocation"):
		return "", fault("SIGN_LOCATION_REJECTED", "location was rejected")
	case strings.Contains(body, "同一设备不允许重复签到"):
		return "", fault("SIGN_DEVICE_REJECTED", "upstream rejected reuse of this device identity")
	default:
		return "", fault("SIGN_REJECTED", "sign-in was not accepted")
	}
}

func orderedQuery(pairs ...string) string {
	if len(pairs)%2 != 0 {
		return ""
	}
	values := make([]string, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		values = append(values, url.QueryEscape(pairs[i])+"="+url.QueryEscape(pairs[i+1]))
	}
	return strings.Join(values, "&")
}

func formatCoordinate(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// jitterLocation mirrors the reference implementation: independent uniform
// offsets of ±0.00005 degrees, tightened to ±0.00001 after errorLocation.
func jitterLocation(latitude, longitude float64, tight bool) (float64, float64) {
	delta := 0.00005
	if tight {
		delta = 0.00001
	}
	latitude += (randomUnit()*2 - 1) * delta
	longitude += (randomUnit()*2 - 1) * delta
	return clampCoordinate(latitude, -90, 90), clampCoordinate(longitude, -180, 180)
}

func randomUnit() float64 {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<53))
	if err != nil {
		return 0.5
	}
	return float64(n.Int64()) / float64(int64(1<<53))
}

func clampCoordinate(v, min, max float64) float64 {
	if math.IsNaN(v) {
		return min
	}
	return math.Min(max, math.Max(min, v))
}

func coordinateDistanceMeters(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadius = 6371000.0
	phi1 := lat1 * math.Pi / 180
	phi2 := lat2 * math.Pi / 180
	dPhi := (lat2 - lat1) * math.Pi / 180
	dLambda := (lng2 - lng1) * math.Pi / 180
	a := math.Sin(dPhi/2)*math.Sin(dPhi/2) + math.Cos(phi1)*math.Cos(phi2)*math.Sin(dLambda/2)*math.Sin(dLambda/2)
	return 2 * earthRadius * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
