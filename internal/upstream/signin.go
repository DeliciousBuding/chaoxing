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
	CourseID   string
	ClassID    string
	ActivityID string
	Latitude   *float64
	Longitude  *float64
	Address    string
	SignCode   string
}

type SignResult struct {
	ActivityID string    `json:"activity_id"`
	CourseID   string    `json:"course_id"`
	ClassID    string    `json:"class_id"`
	Method     string    `json:"method"`
	State      string    `json:"state"`
	Verified   bool      `json:"verified"`
	FetchedAt  time.Time `json:"fetched_at"`
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
	MockData  string  `json:"mockData"`
}

// SignActivity submits exactly one selected activity, supports only normal and
// location sign-in, and verifies the upstream attendance state after submit.
func (c *Client) SignActivity(ctx context.Context, req SignRequest) (SignResult, error) {
	if strings.TrimSpace(req.CourseID) == "" || strings.TrimSpace(req.ClassID) == "" || strings.TrimSpace(req.ActivityID) == "" {
		return SignResult{}, fault("INVALID_ARGUMENT", "course, class and activity IDs are required")
	}
	if (req.Latitude == nil) != (req.Longitude == nil) {
		return SignResult{}, fault("INVALID_ARGUMENT", "latitude and longitude must be supplied together")
	}
	if req.Latitude != nil && !validCoordinate(*req.Latitude, *req.Longitude) {
		return SignResult{}, fault("INVALID_ARGUMENT", "location is outside range")
	}
	courses, err := c.fetchCourses(ctx)
	if err != nil {
		return SignResult{}, err
	}
	course, ok := findCourseClass(courses, req.CourseID, req.ClassID)
	if !ok {
		return SignResult{}, fault("NOT_FOUND", "course/class is not in the accessible list")
	}
	rows, err := c.fetchActivities(ctx, course)
	if err != nil {
		return SignResult{}, err
	}
	activity, ok := findActivity(rows, req.ActivityID)
	if !ok || activity.Kind != "signin" {
		return SignResult{}, fault("NOT_FOUND", "activity is not in the current class feed")
	}
	if activity.UserState != "unsigned" {
		return SignResult{
			ActivityID: activity.ID, CourseID: activity.CourseID, ClassID: activity.ClassID,
			Method: "none", State: activity.UserState, Verified: true, FetchedAt: time.Now().UTC(),
		}, nil
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
	if info.IfNeedVCode.ok && info.IfNeedVCode.value == 1 || info.OpenCheckFace.ok && info.OpenCheckFace.value == 1 {
		return SignResult{}, fault("UNSUPPORTED", "activity requires manual verification")
	}
	var method string
	switch {
	case info.OtherID.value == 0 && (!info.IfPhoto.ok || info.IfPhoto.value == 0):
		method = "normal"
	case info.OtherID.value == 4 && info.IfOpenAddress.ok && info.IfOpenAddress.value == 1:
		method = "location"
	case info.OtherID.value == 5 && req.SignCode != "":
		method = "password"
	case info.OtherID.value == 3 && req.SignCode != "":
		method = "gesture"
	default:
		if info.OtherID.value == 3 || info.OtherID.value == 5 {
			return SignResult{}, fault("INVALID_ARGUMENT", "sign_code is required for gesture or password sign-in")
		}
		return SignResult{}, fault("UNSUPPORTED", "only normal, location, gesture and password sign-in are implemented")
	}
	state, err := c.signSelected(ctx, activity, info, req, method)
	if err != nil {
		return SignResult{}, err
	}
	verified, err := c.attendanceVerified(ctx, activity.ID)
	if err != nil {
		return SignResult{}, err
	}
	if !verified {
		return SignResult{}, fault("SIGN_REJECTED", "upstream did not confirm the sign-in")
	}
	return SignResult{
		ActivityID: activity.ID, CourseID: activity.CourseID, ClassID: activity.ClassID,
		Method: method, State: state, Verified: true, FetchedAt: time.Now().UTC(),
	}, nil
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
	return payload.Data.Status.value != 0, nil
}

func (c *Client) signSelected(ctx context.Context, activity Activity, info activeInfo, req SignRequest, method string) (string, error) {
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
	if method == "normal" {
		q := orderedQuery(
			"activeId", activity.ID, "courseId", activity.CourseID, "uid", identity.UID,
			"clientip", "", "useragent", "", "latitude", "-1", "longitude", "-1",
			"appType", "15", "fid", identity.FID, "name", identity.Name,
			"deviceCode", c.device.DeviceCode,
		)
		b, err := c.get(ctx, stuSignAPI+"?"+q)
		if err != nil {
			return "", err
		}
		return signResponseState(string(b))
	}
	if method == "password" || method == "gesture" {
		if err := c.checkSignCode(ctx, activity.ID, req.SignCode); err != nil {
			return "", err
		}
		q := orderedQuery(
			"activeId", activity.ID, "courseId", activity.CourseID, "uid", identity.UID,
			"clientip", "", "latitude", "-1", "longitude", "-1", "appType", "15",
			"fid", identity.FID, "name", identity.Name, "signCode", req.SignCode,
			"deviceCode", c.device.DeviceCode,
		)
		b, err := c.get(ctx, stuSignAPI+"?"+q)
		if err != nil {
			return "", err
		}
		return signResponseState(string(b))
	}

	state, err := c.signLocation(ctx, activity, info, req, identity, false)
	if err == nil {
		return state, nil
	}
	var f *Fault
	if !errors.As(err, &f) || f.Code != "SIGN_LOCATION_REJECTED" {
		return "", err
	}
	// The reference client tightens its random offset after errorLocation and
	// lets the operator retry once. Automatic mode performs that single retry.
	return c.signLocation(ctx, activity, info, req, identity, true)
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

func (c *Client) signLocation(ctx context.Context, activity Activity, info activeInfo, req SignRequest, identity signIdentity, tight bool) (string, error) {
	if info.LocationLat == nil || info.LocationLng == nil {
		return "", fault("UNSUPPORTED", "location activity has no published center")
	}
	lat, lng := *info.LocationLat, *info.LocationLng
	if req.Latitude != nil {
		lat, lng = *req.Latitude, *req.Longitude
		if info.LocationRange.ok && info.LocationRange.value > 0 && coordinateDistanceMeters(lat, lng, *info.LocationLat, *info.LocationLng) > float64(info.LocationRange.value) {
			return "", fault("INVALID_ARGUMENT", "preset location is outside the activity range")
		}
	}
	lat, lng = jitterLocation(lat, lng, tight)
	address := firstNonBlank(req.Address, activity.Course, "课堂位置")
	locationResult, err := json.Marshal(locationResultPayload{
		Result: 1, Latitude: lat, Longitude: lng, Address: address,
		MockData: `{"strategy":0,"probability":-1}`,
	})
	if err != nil {
		return "", fault("INTERNAL", "location payload failed")
	}
	q := orderedQuery(
		"name", identity.Name, "address", address, "activeId", activity.ID,
		"courseId", activity.CourseID, "uid", identity.UID, "clientip", "",
		"latitude", formatCoordinate(lat), "longitude", formatCoordinate(lng),
		"fid", identity.FID, "appType", "15", "ifTiJiao", "1", "deviceCode", c.device.DeviceCode,
		"vpProbability", "-1", "vpStrategy", "", "locationResult", string(locationResult),
	)
	b, err := c.get(ctx, stuSignAPI+"?"+q)
	if err != nil {
		return "", err
	}
	return signResponseState(string(b))
}

func (c *Client) preSign(ctx context.Context, activity Activity, identity signIdentity) error {
	q := orderedQuery(
		"courseId", activity.CourseID, "classId", activity.ClassID, "activePrimaryId", activity.ID,
		"general", "1", "sys", "1", "ls", "1", "appType", "15", "uid", identity.UID,
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
	for _, pattern := range []*regexp.Regexp{
		regexp.MustCompile(`"primaryAttend"\s*:\s*\{[^{}]*?"status"\s*:\s*(\d+)`),
		regexp.MustCompile(`signstatus\s*=\s*(\d+)`),
	} {
		if m := pattern.FindStringSubmatch(body); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n == 1 || n == 2 || n == 3 || n == 9
			}
		}
	}
	return false
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
	switch {
	case body == "success":
		return "signed", nil
	case body == "您已签到过了":
		return "signed", nil
	case body == "success2":
		return "", fault("SIGN_EXPIRED", "sign-in has ended")
	case strings.HasPrefix(body, "validate_"), strings.HasPrefix(body, "[face]"), strings.HasPrefix(body, "checkFace_"):
		return "", fault("UNSUPPORTED", "activity requires manual verification")
	case strings.HasPrefix(body, "errorLocation"):
		return "", fault("SIGN_LOCATION_REJECTED", "location was rejected")
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
