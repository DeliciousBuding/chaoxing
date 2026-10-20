package upstream

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestParseQRCodeSupportsReferenceFormats(t *testing.T) {
	raw := `SIGNIN:aid=9001&c=code1&enc=qr-enc-extra`
	payload, err := parseQRCode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if payload.AID != "9001" || payload.Code != "code1" || payload.Enc != "qr" {
		t.Fatalf("%+v", payload)
	}
	payload, err = parseQRCode(`https://example.com/sign?id=9002&c=code-2&enc=qr-enc-2`)
	if err != nil {
		t.Fatal(err)
	}
	if payload.AID != "9002" || payload.Code != "code-2" || payload.Enc != "qr-enc-2" {
		t.Fatalf("%+v", payload)
	}
	if _, err = parseQRCode(`https://example.com/sign?id=9002`); err == nil || SafeFault(err).Code != "INVALID_ARGUMENT" {
		t.Fatal(err)
	}
}

func TestQRSignVerifiesDetailAndUsesReferenceQuery(t *testing.T) {
	c := academicClient(t, []Course{{ID: "course-1", ClassID: "class-1", Name: "Synthetic"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v2/apis/active/student/activelist":
			return response(r, signActivityResponse(2, "")), nil
		case "/v2/apis/active/getPPTActiveInfo":
			return response(r, `{"result":1,"data":{"otherId":2,"ifopenAddress":0,"ifNeedVCode":0,"openCheckFaceFlag":0}}`), nil
		case "/apis/login/userLogin4Uname.do":
			return response(r, `{"status":true,"msg":{"name":"Synthetic User","uid":"10001","puid":"10001","fid":"20002"}}`), nil
		case "/newsign/preSign":
			return response(r, `<html>ready</html>`), nil
		case "/pptSign/analysis":
			return response(r, `code='+'a1b2c3'`), nil
		case "/pptSign/analysis2":
			return response(r, ``), nil
		case "/newsign/signDetail":
			if r.URL.RawQuery != "activePrimaryId=9001&type=1&msg=code-1" {
				t.Fatal(r.URL.RawQuery)
			}
			return response(r, `{"isOver":0,"signCode":"code-1"}`), nil
		case "/pptSign/stuSignajax":
			q := r.URL.Query()
			if q.Get("enc") != "qr-enc" || q.Get("activeId") != "9001" || q.Get("courseId") != "course-1" {
				t.Fatal(r.URL.RawQuery)
			}
			if strings.Contains(r.URL.RawQuery, "location=") || strings.Contains(r.URL.RawQuery, "enc2=") || strings.Contains(r.URL.RawQuery, "validate=") {
				t.Fatal(r.URL.RawQuery)
			}
			wantPrefix := "enc=qr-enc&name=Synthetic+User&activeId=9001&uid=10001&clientip=&latitude=-1&longitude=-1&fid=20002&appType=15&deviceCode="
			if !strings.HasPrefix(r.URL.RawQuery, wantPrefix) {
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
	v, err := c.Call(context.Background(), "sign_activity", map[string]any{
		"course_id": "course-1", "class_id": "class-1", "activity_id": "9001",
		"qr_payload": "https://example.com/sign?id=9001&c=code-1&enc=qr-enc",
	})
	if err != nil {
		t.Fatal(err)
	}
	result := v.(map[string]any)["result"].(SignResult)
	if result.Method != "qrcode" || !result.Verified {
		t.Fatalf("%+v", result)
	}
}
