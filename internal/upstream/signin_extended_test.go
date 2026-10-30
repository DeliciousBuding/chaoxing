package upstream

import (
	"context"
	"net/http"
	"testing"
)

func TestPhotoSignSubmitsPanObject(t *testing.T) {
	c := photoFaceClient(t, 0, 0)
	v, err := c.Call(context.Background(), "sign_activity", map[string]any{
		"course_id": "course-1", "class_id": "class-1", "activity_id": "9001", "photo_object_id": "photo-object",
	})
	if err != nil {
		t.Fatal(err)
	}
	result := v.(map[string]any)["result"].(SignResult)
	if result.Method != "photo" || !result.Verified {
		t.Fatalf("%+v", result)
	}
}

func TestFaceSignUsesConstructedLivenessToken(t *testing.T) {
	c := photoFaceClient(t, 4, 1)
	v, err := c.Call(context.Background(), "sign_activity_with_face", map[string]any{
		"course_id": "course-1", "class_id": "class-1", "activity_id": "9001", "face_object_id": "face-object",
	})
	if err != nil {
		t.Fatal(err)
	}
	result := v.(map[string]any)["result"].(SignResult)
	if result.Method != "face" || !result.Verified {
		t.Fatalf("%+v", result)
	}
}

func photoFaceClient(t *testing.T, otherID, faceFlag int) *Client {
	t.Helper()
	var c *Client
	c = academicClient(t, []Course{{ID: "course-1", ClassID: "class-1", Name: "Synthetic"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v2/apis/active/student/activelist":
			return response(r, signActivityResponse(otherID, "")), nil
		case "/v2/apis/active/getPPTActiveInfo":
			return response(r, `{"result":1,"data":{"otherId":`+itoa(otherID)+`,"ifphoto":1,"ifopenAddress":1,"ifNeedVCode":0,"openCheckFaceFlag":`+itoa(faceFlag)+`,"locationLatitude":31,"locationLongitude":121,"locationRange":500}}`), nil
		case "/newsign/preSign":
			return response(r, `<html>ready</html>`), nil
		case "/pptSign/analysis":
			return response(r, `code='+'a1b2c3'`), nil
		case "/pptSign/analysis2":
			return response(r, ``), nil
		case "/pptSign/check-face-result":
			if r.URL.Query().Get("DB_STRATEGY") != "PRIMARY_KEY" || r.URL.Query().Get("faceResult") == "" {
				t.Fatal(r.URL.RawQuery)
			}
			return response(r, `{"status":1,"enc":"face-enc","code":"face-code"}`), nil
		case "/pptSign/stuSignajax":
			q := r.URL.Query()
			if q.Get("deviceCode") != c.device.DeviceCode {
				t.Fatal("device code missing")
			}
			if q.Get("faceEnc") != "" && (q.Get("faceEnc") != "face-enc" || q.Get("currentFaceId") != "face-object" || q.Get("ifCFP") != "0" || q.Get("faceCode") != "") {
				t.Fatal(r.URL.RawQuery)
			}
			if q.Get("objectId") != "" && q.Get("objectId") != "photo-object" {
				t.Fatal(r.URL.RawQuery)
			}
			return response(r, "success"), nil
		case "/v2/apis/sign/getAttendInfo":
			return response(r, `{"result":1,"data":{"status":1}}`), nil
		default:
			t.Fatalf("unexpected request %s", r.URL)
			return nil, nil
		}
	}))
	c.identity = &signIdentity{UID: "10001", FID: "20002", Name: "Synthetic User", CXCID: "cx-cid", SC: "device-secret"}
	return c
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	return string(rune('0' + v))
}
