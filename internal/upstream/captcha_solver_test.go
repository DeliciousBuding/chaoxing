package upstream

import (
	"context"
	"net/http"
	"testing"
)

type fakeCaptchaSolver struct {
	calls   int
	image   []byte
	cutout  []byte
	x       int
	failure error
}

func (f *fakeCaptchaSolver) Solve(_ context.Context, image, cutout []byte) (int, error) {
	f.calls++
	f.image, f.cutout = image, cutout
	return f.x, f.failure
}

func TestSignActivitySolvesDynamicallyRequestedSliderCaptcha(t *testing.T) {
	signCalls := 0
	solver := &fakeCaptchaSolver{x: 120}
	c := academicClient(t, []Course{{ID: "course-1", ClassID: "class-1", Name: "Synthetic"}}, roundtrip(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Host == "captcha.chaoxing.com" && r.URL.Path == "/captcha/get/conf":
			return response(r, `cx_captcha_function({"t":123456})`), nil
		case r.URL.Host == "captcha.chaoxing.com" && r.URL.Path == "/captcha/get/verification/image":
			return response(r, `cx_captcha_function({"token":"image-token","imageVerificationVo":{"shadeImage":"https://captcha.chaoxing.com/shade.jpg","cutoutImage":"https://captcha.chaoxing.com/cutout.jpg"}})`), nil
		case r.URL.Host == "captcha.chaoxing.com" && r.URL.Path == "/shade.jpg":
			return response(r, "synthetic-shade-image"), nil
		case r.URL.Host == "captcha.chaoxing.com" && r.URL.Path == "/cutout.jpg":
			return response(r, "synthetic-cutout-image"), nil
		case r.URL.Host == "captcha.chaoxing.com" && r.URL.Path == "/captcha/check/verification/result":
			if r.URL.Query().Get("textClickArr") != `[{"x":120}]` {
				t.Fatal(r.URL.RawQuery)
			}
			return response(r, `cx_captcha_function({"error":0,"result":true,"extraData":"{\"validate\":\"validated-token\"}"})`), nil
		}
		switch r.URL.Path {
		case "/v2/apis/active/student/activelist":
			return response(r, signActivityResponse(5, "")), nil
		case "/v2/apis/active/getPPTActiveInfo":
			return response(r, `{"result":1,"data":{"otherId":5,"ifNeedVCode":0,"openCheckFaceFlag":0}}`), nil
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
	c.config.CaptchaSolver = solver
	result, err := c.SignActivity(context.Background(), SignRequest{CourseID: "course-1", ClassID: "class-1", ActivityID: "9001", SignCode: "1234"})
	if err != nil || !result.Verified || result.Method != "password" || signCalls != 2 || solver.calls != 1 {
		t.Fatalf("result=%+v err=%v signCalls=%d solverCalls=%d", result, err, signCalls, solver.calls)
	}
	if string(solver.image) != "synthetic-shade-image" || string(solver.cutout) != "synthetic-cutout-image" {
		t.Fatalf("solver image=%q", solver.image)
	}
}
