package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

func TestActiveInfoCoordinateRepresentations(t *testing.T) {
	for _, tc := range []struct {
		name, lat, lng string
		hasCenter      bool
		reject         bool
	}{
		{"numbers", "31", "121", true, false},
		{"numeric strings", `"31"`, `"121"`, true, false},
		{"empty strings", `""`, `""`, false, false},
		{"null", "null", "null", false, false},
		{"invalid string", `"invalid"`, `"121"`, false, true},
		{"boolean", "true", "121", false, true},
		{"nonfinite string", `"NaN"`, `"121"`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var info activeInfo
			err := json.Unmarshal([]byte(`{"otherId":4,"locationLatitude":`+tc.lat+`,"locationLongitude":`+tc.lng+`,"locationRange":"500"}`), &info)
			if (err != nil) != tc.reject {
				t.Fatalf("decode error=%v reject=%t", err, tc.reject)
			}
			if tc.reject {
				return
			}
			lat, lng := 31.0, 121.0
			position, err := signPosition(Activity{}, info, SignRequest{Latitude: &lat, Longitude: &lng}, false, nil)
			if err != nil || position.Result != 1 {
				t.Fatalf("preset unusable: position=%+v err=%v", position, err)
			}
			if (info.LocationLat != nil && info.LocationLng != nil) != tc.hasCenter {
				t.Fatal("incorrect published center presence")
			}
		})
	}
}

func TestLocationSignAcceptsOptionalPublishedCenter(t *testing.T) {
	for _, center := range []string{
		`"locationLatitude":"","locationLongitude":""`,
		`"locationLatitude":"31","locationLongitude":"121"`,
	} {
		t.Run(center, func(t *testing.T) {
			submissions := 0
			c := academicClient(t, []Course{{ID: "course-1", ClassID: "class-1"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/v2/apis/active/student/activelist":
					return response(r, signActivityResponse(4, "")), nil
				case "/v2/apis/active/getPPTActiveInfo":
					return response(r, `{"result":1,"data":{"otherId":4,"status":1,`+center+`,"locationRange":"500"}}`), nil
				case "/apis/login/userLogin4Uname.do":
					return response(r, `{"status":true,"msg":{"name":"Synthetic","uid":"10001","puid":"10001","fid":"20002"}}`), nil
				case "/newsign/preSign":
					return response(r, `<html>ready</html>`), nil
				case "/pptSign/analysis":
					return response(r, `code='+'a1b2c3'`), nil
				case "/pptSign/analysis2":
					return response(r, ``), nil
				case "/pptSign/stuSignajax":
					submissions++
					lat, err := strconv.ParseFloat(r.URL.Query().Get("latitude"), 64)
					if err != nil || lat < 30.99994 || lat > 31.00006 {
						t.Fatal("configured preset was not used")
					}
					return response(r, "success"), nil
				case "/v2/apis/sign/getAttendInfo":
					return response(r, `{"result":1,"data":{"status":1}}`), nil
				default:
					t.Fatalf("unexpected request %s", r.URL.Path)
					return nil, nil
				}
			}))
			lat, lng := 31.0, 121.0
			result, err := c.SignActivity(context.Background(), SignRequest{
				CourseID: "course-1", ClassID: "class-1", ActivityID: "9001", Latitude: &lat, Longitude: &lng,
			})
			if err != nil || !result.Submitted || !result.Verified || submissions != 1 {
				t.Fatalf("result=%+v submissions=%d err=%v", result, submissions, err)
			}
		})
	}
}

func TestEndedActivityDetailRejectsGroupCandidateBeforeSubmission(t *testing.T) {
	c := academicClient(t, []Course{{ID: "course-1", ClassID: "class-1"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v2/apis/active/getPPTActiveInfo" {
			t.Fatalf("ended activity reached %s", r.URL.Path)
		}
		return response(r, `{"result":1,"data":{"otherId":4,"status":2,"locationLatitude":"","locationLongitude":""}}`), nil
	}))
	item := Activity{ID: "9001", CourseID: "course-1", ClassID: "class-1", Kind: "signin", State: "active", Signable: true, Source: "group"}
	ctx := context.WithValue(context.Background(), observedSignKey{}, item)
	_, err := c.SignActivity(ctx, SignRequest{CourseID: item.CourseID, ClassID: item.ClassID, ActivityID: item.ID})
	if err == nil || SafeFault(err).Code != "SIGN_EXPIRED" {
		t.Fatalf("ended activity error=%v", err)
	}
}
