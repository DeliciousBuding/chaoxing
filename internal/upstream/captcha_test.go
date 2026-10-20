package upstream

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/operation"
)

func TestSignCaptchaChallengeAnswerAndRetry(t *testing.T) {
	signCalls := 0
	c := academicClient(t, []Course{{ID: "course-1", ClassID: "class-1", Name: "Synthetic"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Host == "captcha.chaoxing.com" && r.URL.Path == "/captcha/get/conf":
			return response(r, `cx_captcha_function({"t":123456})`), nil
		case r.URL.Host == "captcha.chaoxing.com" && r.URL.Path == "/captcha/get/verification/image":
			if r.URL.Query().Get("captchaId") != captchaID || r.URL.Query().Get("type") != "slide" {
				t.Fatal(r.URL.RawQuery)
			}
			return response(r, `cx_captcha_function({"token":"image-token","imageVerificationVo":{"shadeImage":"https://example.com/shade.jpg","cutoutImage":"https://example.com/cutout.jpg"}})`), nil
		case r.URL.Host == "captcha.chaoxing.com" && r.URL.Path == "/captcha/check/verification/result":
			if r.URL.Query().Get("token") != "image-token" || r.URL.Query().Get("textClickArr") != `[{"x":120}]` {
				t.Fatal(r.URL.RawQuery)
			}
			return response(r, `cx_captcha_function({"error":0,"result":true,"extraData":"{\"validate\":\"validated-token\"}"})`), nil
		}
		switch r.URL.Path {
		case "/v2/apis/active/student/activelist":
			return response(r, signActivityResponse(5, "")), nil
		case "/v2/apis/active/getPPTActiveInfo":
			return response(r, `{"result":1,"data":{"otherId":5,"ifNeedVCode":1,"openCheckFaceFlag":0}}`), nil
		case "/apis/login/userLogin4Uname.do":
			return response(r, `{"status":true,"msg":{"name":"Synthetic User","uid":"10001","puid":"10001","fid":"20002"}}`), nil
		case "/newsign/preSign":
			return response(r, `<html>ready</html>`), nil
		case "/pptSign/analysis":
			return response(r, `code='+'a1b2c3'`), nil
		case "/pptSign/analysis2":
			return response(r, ``), nil
		case "/widget/sign/pcStuSignController/checkSignCode":
			return response(r, `{"result":1}`), nil
		case "/pptSign/stuSignajax":
			signCalls++
			if signCalls == 1 {
				if strings.Contains(r.URL.RawQuery, "enc2=") || strings.Contains(r.URL.RawQuery, "validate=") {
					t.Fatal("first captcha probe must not contain retry parameters")
				}
				return response(r, "validate_enc2-token"), nil
			}
			if r.URL.Query().Get("enc2") != "enc2-token" || r.URL.Query().Get("validate") != "validated-token" {
				t.Fatal(r.URL.RawQuery)
			}
			return response(r, "success"), nil
		case "/v2/apis/sign/getAttendInfo":
			if signCalls != 2 {
				t.Fatal("attendance checked before captcha retry")
			}
			return response(r, `{"result":1,"data":{"status":1}}`), nil
		default:
			t.Fatalf("unexpected request %s", r.URL)
			return nil, nil
		}
	}))
	args := map[string]any{"course_id": "course-1", "class_id": "class-1", "activity_id": "9001", "sign_code": "1234"}
	_, err := c.Call(context.Background(), "sign_activity", args)
	if err == nil || SafeFault(err).Code != "CAPTCHA_REQUIRED" {
		t.Fatal(err)
	}
	challenge, err := operation.Execute(context.Background(), "get_sign_captcha", map[string]any{"course_id": "course-1", "class_id": "class-1", "activity_id": "9001"}, c.Call)
	if err != nil {
		t.Fatal(err)
	}
	if challenge.(map[string]any)["captcha"] == nil {
		t.Fatalf("normalized captcha envelope missing: %+v", challenge)
	}
	row := challenge.(map[string]any)["captcha"].(map[string]any)
	if row["shade_image"] != "https://example.com/shade.jpg" || row["cutout_image"] != "https://example.com/cutout.jpg" {
		t.Fatalf("%+v", row)
	}
	answer, err := operation.Execute(context.Background(), "answer_sign_captcha", map[string]any{"course_id": "course-1", "class_id": "class-1", "activity_id": "9001", "x_position": 120.0}, c.Call)
	if err != nil {
		t.Fatal(err)
	}
	if answer.(map[string]any)["validated"] != true {
		t.Fatalf("%+v", answer)
	}
	result, err := c.Call(context.Background(), "sign_activity", args)
	if err != nil {
		t.Fatal(err)
	}
	signResult := result.(map[string]any)["result"].(SignResult)
	if signResult.Method != "password" || !signResult.Verified || signCalls != 2 {
		t.Fatalf("%+v calls=%d", signResult, signCalls)
	}
}

func TestAnswerSignCaptchaRejectsWrongAnswer(t *testing.T) {
	c := testClient(t)
	c.captchaChallenges = map[string]signCaptchaChallenge{"9001": {
		ActivityID: "9001", Enc2: "enc2", Token: "image-token", IV: "iv",
		ShadeImage: "https://example.com/shade.jpg", CutoutImage: "https://example.com/cutout.jpg",
		CreatedAt: time.Now().UTC(),
	}}
	c.identity = &signIdentity{UID: "10001"}
	c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "captcha.chaoxing.com" || r.URL.Path != "/captcha/check/verification/result" {
			t.Fatalf("unexpected request %s", r.URL)
		}
		return response(r, `cx_captcha_function({"error":0,"result":false})`), nil
	})
	_, err := c.answerSignCaptcha(context.Background(), map[string]any{"activity_id": "9001", "x_position": 120.0})
	if err == nil || SafeFault(err).Code != "SIGN_CAPTCHA_REJECTED" {
		t.Fatal(err)
	}
}
