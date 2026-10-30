package upstream

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPreSignForbiddenRecoversSessionOnceAndOnlyThere(t *testing.T) {
	for _, tc := range []struct {
		name, path      string
		always, success bool
	}{
		{"expired pre-sign", "/newsign/preSign", false, true},
		{"still forbidden", "/newsign/preSign", true, false},
		{"unrelated forbidden", "/v2/apis/active/getPPTActiveInfo", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t)
			logins, requests := 0, 0
			client.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/fanyalogin" {
					logins++
					return response(r, `{"status":true}`), nil
				}
				requests++
				resp := response(r, "<html>ready</html>")
				if requests == 1 || tc.always {
					resp.StatusCode = http.StatusForbidden
				}
				return resp, nil
			})
			_, err := client.authenticatedRead(context.Background(), http.MethodPost, "https://mobilelearn.chaoxing.com"+tc.path, url.Values{"ext": {"{}"}})
			if (err == nil) != tc.success {
				t.Fatalf("err=%v", err)
			}
			if tc.path == "/newsign/preSign" && (logins != 1 || requests != 2) {
				t.Fatalf("logins=%d requests=%d", logins, requests)
			}
			if tc.path != "/newsign/preSign" && (logins != 0 || requests != 1) {
				t.Fatal("unrelated 403 triggered login")
			}
		})
	}
}

func TestQRActivityCannotOverrideSelectedActivity(t *testing.T) {
	client := testClient(t)
	requests := 0
	client.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) { requests++; return response(r, "unexpected"), nil })
	_, err := client.SignActivity(context.Background(), SignRequest{CourseID: "c", ClassID: "k", ActivityID: "9001", QRPayload: "https://example.com/?id=9002&enc=synthetic"})
	if SafeFault(err).Code != "INVALID_ARGUMENT" || requests != 0 {
		t.Fatalf("err=%v requests=%d", err, requests)
	}
	for _, payload := range []string{"https://example.com/?id=not-an-id&enc=x", "https://example.com/?id=-1&enc=x", "https://example.com/?id=0&enc=x", "ftp://example.com/?id=9001&enc=x"} {
		if _, err := parseQRCode(payload); err == nil {
			t.Fatalf("invalid QR accepted: %s", payload)
		}
	}
}

func TestReferenceOptionalLocationAndPersonIdentity(t *testing.T) {
	for _, other := range []int{0, 2, 3, 5, 4} {
		t.Run(strconv.Itoa(other), func(t *testing.T) {
			submitted := 0
			client := academicClient(t, []Course{{ID: "c", ClassID: "k", Name: "Synthetic"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/v2/apis/active/student/activelist":
					return response(r, signActivityResponse(other, "")), nil
				case "/v2/apis/active/getPPTActiveInfo":
					return response(r, fmt.Sprintf(`{"result":1,"data":{"otherId":%d,"ifopenAddress":1}}`, other)), nil
				case "/newsign/preSign":
					if r.URL.Query().Get("uid") != "222" {
						t.Fatal("preSign did not use person identity")
					}
					return response(r, `<html>ready</html>`), nil
				case "/pptSign/analysis":
					return response(r, `code='+'a1b2c3'`), nil
				case "/pptSign/analysis2":
					return response(r, ``), nil
				case "/widget/sign/pcStuSignController/checkSignCode":
					return response(r, `{"result":1}`), nil
				case "/newsign/signDetail":
					return response(r, `{"isOver":0,"signCode":"synthetic-code"}`), nil
				case "/pptSign/stuSignajax":
					submitted++
					q := r.URL.Query()
					if q.Get("uid") != "222" {
						t.Fatal("submission did not use person identity")
					}
					if other == 0 {
						if q.Get("latitude") != "-1" || q.Get("locationResult") != "" {
							t.Fatal("click sign invented location")
						}
						return response(r, "success"), nil
					}
					var result, position locationResultPayload
					if err := json.Unmarshal([]byte(q.Get("locationResult")), &result); err != nil {
						t.Fatal(err)
					}
					if result.MockData == "" || result.Address != "Synthetic room" {
						t.Fatal("missing location result")
					}
					if other != 4 {
						if err := json.Unmarshal([]byte(q.Get("location")), &position); err != nil {
							t.Fatal(err)
						}
						if position.Latitude != result.Latitude || position.Longitude != result.Longitude || position.MockData != "" {
							t.Fatal("location representations disagree")
						}
					}
					if other == 2 && (q.Get("latitude") != "-1" || len(q["latitude"]) != 1) {
						t.Fatal("QR latitude contract mismatch")
					}
					if other == 3 || other == 5 {
						lat, _ := strconv.ParseFloat(q.Get("latitude"), 64)
						if lat != result.Latitude {
							t.Fatal("code sign latitude disagrees with location result")
						}
					}
					return response(r, "success"), nil
				case "/v2/apis/sign/getAttendInfo":
					return response(r, `{"result":1,"data":{"status":1}}`), nil
				case "/cdn-cgi/trace":
					return response(r, "ip=203.0.113.7\n"), nil
				default:
					t.Fatalf("unexpected request %s", r.URL.Path)
					return nil, nil
				}
			}))
			client.identity = &signIdentity{UID: "111", PUID: "222", FID: "333", Name: "Synthetic"}
			store := &auditStore{}
			if other == 0 {
				client.config.SignAudit = store
			}
			lat, lng := 31.0, 121.0
			req := SignRequest{CourseID: "c", ClassID: "k", ActivityID: "9001", Latitude: &lat, Longitude: &lng, Address: "Synthetic room"}
			if other == 3 || other == 5 {
				req.SignCode = "1234"
			}
			if other == 2 {
				req.QRPayload = "https://example.com/?id=9001&c=synthetic-code&enc=synthetic"
			}
			got, err := client.SignActivity(context.Background(), req)
			if err != nil || !got.Verified || !got.Submitted || submitted != 1 {
				t.Fatalf("result=%+v err=%v submissions=%d", got, err, submitted)
			}
			if other == 0 && (got.Latitude != nil || got.Address != "" || len(store.records) != 1 || store.records[0].Latitude != nil || store.records[0].Method != "normal") {
				t.Fatal("unused preset was recorded as an actual submitted location")
			}
		})
	}
}

func TestMissingRequiredLocationNeverSubmits(t *testing.T) {
	client := academicClient(t, []Course{{ID: "c", ClassID: "k"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v2/apis/active/student/activelist":
			return response(r, signActivityResponse(5, "")), nil
		case "/v2/apis/active/getPPTActiveInfo":
			return response(r, `{"result":1,"data":{"otherId":5,"ifopenAddress":1}}`), nil
		default:
			t.Fatal("required-location validation made a preSign or submission request")
			return nil, nil
		}
	}))
	_, err := client.SignActivity(context.Background(), SignRequest{CourseID: "c", ClassID: "k", ActivityID: "9001", SignCode: "1234"})
	if SafeFault(err).Code != "INVALID_ARGUMENT" {
		t.Fatal(err)
	}
}

func TestDeviceIdentityRecoveryPreservesLeadingZeroAndChecksPadding(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"cid":"synthetic-cid","sc":"synthetic-sc"}`)
	block := make([]byte, key.Size())
	block[1] = 1
	start := len(block) - len(payload)
	for i := 2; i < start-1; i++ {
		block[i] = 0xff
	}
	copy(block[start:], payload)
	encode := func(b []byte) []byte {
		return new(big.Int).Exp(new(big.Int).SetBytes(b), key.D, key.N).FillBytes(make([]byte, key.Size()))
	}
	cid, sc, err := recoverDeviceIdentity(&key.PublicKey, encode(block))
	if err != nil || cid != "synthetic-cid" || sc != "synthetic-sc" {
		t.Fatalf("valid signed block rejected: %v", err)
	}
	block[3] = 2
	if _, _, err := recoverDeviceIdentity(&key.PublicKey, encode(block)); err == nil {
		t.Fatal("non-FF padding accepted")
	}
	if _, _, err := recoverDeviceIdentity(&key.PublicKey, []byte{1}); err == nil {
		t.Fatal("short ciphertext accepted")
	}
}

func TestExplicitFaceKeepsBaseVariantAndWireParameters(t *testing.T) {
	for _, other := range []int{0, 4} {
		t.Run(strconv.Itoa(other), func(t *testing.T) {
			client := academicClient(t, []Course{{ID: "c", ClassID: "k", Name: "Synthetic"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/v2/apis/active/student/activelist":
					return response(r, signActivityResponse(other, "")), nil
				case "/v2/apis/active/getPPTActiveInfo":
					return response(r, fmt.Sprintf(`{"result":1,"data":{"otherId":%d,"ifphoto":0,"openCheckFaceFlag":1,"locationLatitude":31,"locationLongitude":121}}`, other)), nil
				case "/newsign/preSign":
					return response(r, `<html>ready</html>`), nil
				case "/pptSign/analysis":
					return response(r, `code='+'a1b2c3'`), nil
				case "/pptSign/analysis2":
					return response(r, ``), nil
				case "/pptSign/check-face-result":
					var fields map[string]any
					if json.Unmarshal([]byte(r.URL.Query().Get("faceResult")), &fields) != nil || fields["collectStatus"] != float64(1) {
						t.Fatal("face protocol flags are not numeric")
					}
					return response(r, `{"enc":"synthetic-enc"}`), nil
				case "/pptSign/stuSignajax":
					q := r.URL.Query()
					if q.Get("ifCFP") != "0" || q.Get("currentFaceId") != "synthetic-object" || q.Get("faceEnc") != "synthetic-enc" || q.Get("faceCode") != "" {
						t.Fatal("face parameters were omitted or fabricated")
					}
					if _, ok := q["faceEncAid"]; !ok {
						t.Fatal("faceEncAid absent")
					}
					if other == 4 && (q.Get("locationResult") == "" || q.Get("vpProbability") != "-1") {
						t.Fatal("face variant lost location payload")
					}
					if other == 0 {
						if _, ok := q["useragent"]; !ok {
							t.Fatal("click variant lost base parameter")
						}
					}
					return response(r, "success"), nil
				case "/v2/apis/sign/getAttendInfo":
					return response(r, `{"result":1,"data":{"status":1}}`), nil
				default:
					t.Fatalf("unexpected request %s", r.URL.Path)
					return nil, nil
				}
			}))
			client.identity = &signIdentity{UID: "111", PUID: "222", FID: "333", Name: "Synthetic", CXCID: "synthetic-cid", SC: "synthetic-sc"}
			result, err := client.SignActivity(context.Background(), SignRequest{CourseID: "c", ClassID: "k", ActivityID: "9001", FaceObjectID: "synthetic-object"})
			if err != nil || result.Method != "face" || !result.Verified {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestStringEncodedGroupCardIDsAndTimestamp(t *testing.T) {
	card := `{"attachmentType":"15","att_chat_course":{"aid":"9001","atype":"2","atypeName":"位置签到","courseInfo":{"courseid":"123","classid":"456","coursename":"Synthetic"}}}`
	activity, ok := parseIMAttachment(card)
	if !ok || activity.ID != "9001" || activity.CourseID != "123" || activity.ClassID != "456" {
		t.Fatal("string-encoded protocol IDs were dropped")
	}
	attachment := append(lengthPrefixedField(1, []byte("attachment")), lengthPrefixedField(6, []byte(card))...)
	message := lengthPrefixedField(5, attachment)
	outer := lengthPrefixedField(6, message)
	stamp := uint64(1700000000000)
	var timestamp []byte
	for n := stamp; n > 0; n >>= 7 {
		b := byte(n & 0x7f)
		if n > 127 {
			b |= 0x80
		}
		timestamp = append(timestamp, b)
	}
	outer = append(append([]byte{4 << 3}, timestamp...), outer...)
	parsed, ok := parseEasemobMessage(outer)
	if !ok || parsed.Timestamp != int64(stamp) {
		t.Fatal("message timestamp discarded")
	}
}

func TestGroupSweepPrioritizesActiveClassesAndRotates(t *testing.T) {
	client := testClient(t)
	client.classActivities = map[string]cachedClassActivities{"7:70": {at: time.Now()}}
	groups := []imGroup{}
	for i := 0; i < 12; i++ {
		groups = append(groups, imGroup{ID: strconv.Itoa(i), CourseID: strconv.Itoa(i), ClassID: strconv.Itoa(i * 10)})
	}
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		selected := client.selectIMGroups(groups)
		if len(selected) != maxIMGroupsPerSweep || selected[0].ID != "7" {
			t.Fatalf("active class was not prioritized: %+v", selected)
		}
		for _, g := range selected {
			seen[g.ID] = true
		}
	}
	if len(seen) != len(groups) {
		t.Fatalf("fixed-prefix starvation: checked=%d groups=%d", len(seen), len(groups))
	}
}

func TestGroupRowsRespectExactClassSelection(t *testing.T) {
	client := academicClient(t, []Course{{ID: "c", ClassID: "k"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		t.Fatal("cached selection unexpectedly made a request")
		return nil, nil
	}))
	client.config.GroupActivities = true
	client.classActivities = map[string]cachedClassActivities{"c:k": {rows: []Activity{}, at: time.Now()}}
	client.imGroups = []imGroup{{ID: "group"}}
	client.imGroupsAt = time.Now()
	client.imActivities = []Activity{{ID: "1", CourseID: "c", ClassID: "k", Source: "group"}, {ID: "2", CourseID: "other", ClassID: "other", Source: "group"}}
	client.imActivitiesAt = time.Now()
	client.imActivitiesOK = true
	got, err := client.activities(context.Background(), map[string]any{"course_id": "c", "class_id": "k"})
	if err != nil {
		t.Fatal(err)
	}
	items := got.(map[string]any)["items"].([]Activity)
	if len(items) != 1 || items[0].ID != "1" {
		t.Fatalf("cross-class rows escaped selection: %+v", items)
	}
}

func TestCaptchaScopeAndJSONPTerminator(t *testing.T) {
	client := testClient(t)
	client.captchaChallenges = map[string]signCaptchaChallenge{"1": {CourseID: "c", ClassID: "k", CreatedAt: time.Now(), ShadeImage: "synthetic"}}
	args := map[string]any{"course_id": "other", "class_id": "k", "activity_id": "1"}
	if _, err := client.getSignCaptcha(context.Background(), args); SafeFault(err).Code != "INVALID_ARGUMENT" {
		t.Fatal(err)
	}
	if _, err := client.answerSignCaptcha(context.Background(), args); SafeFault(err).Code != "INVALID_ARGUMENT" {
		t.Fatal(err)
	}
	var value map[string]any
	if err := decodeJSONP([]byte(`cx_captcha_function({"result":true});`), &value); err != nil || value["result"] != true {
		t.Fatal("valid JSONP terminator rejected")
	}
	if err := decodeJSONP([]byte(`other_callback({"result":true});`), &value); err == nil {
		t.Fatal("unexpected callback accepted")
	}
	_, err := signResponseState(" 同一设备不允许重复签到 ")
	if SafeFault(err).Code != "SIGN_DEVICE_REJECTED" || SafeFault(err).Retryable {
		t.Fatal("device refusal must stay terminal")
	}
	if strings.Contains(SafeFault(err).Message, "synthetic") {
		t.Fatal("raw response leaked")
	}
}
