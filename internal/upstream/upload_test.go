package upstream

import (
	"context"
	"net/http"
	"testing"
)

func TestUploadFileUsesPanTokenAndMultipart(t *testing.T) {
	c := academicClient(t, nil, roundtrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/apis/login/userLogin4Uname.do":
			return response(r, `{"status":true,"msg":{"name":"Synthetic User","uid":"10001","puid":"10001","fid":"20002"}}`), nil
		case "/api/token/uservalid":
			return response(r, `{"result":true,"_token":"pan-token"}`), nil
		case "/upload":
			if r.URL.Query().Get("_from") != "mobilelearn" || r.URL.Query().Get("_token") != "pan-token" {
				t.Fatal(r.URL.RawQuery)
			}
			if err := r.ParseMultipartForm(1024 * 1024); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("puid") != "10001" {
				t.Fatal("missing puid")
			}
			file, header, err := r.FormFile("file")
			if err != nil || header.Filename != "photo.jpg" {
				t.Fatalf("file=%v err=%v", header, err)
			}
			defer file.Close()
			return response(r, `{"objectid":"object-1"}`), nil
		default:
			t.Fatalf("unexpected request %s", r.URL)
			return nil, nil
		}
	}))
	result, err := c.UploadFile(context.Background(), "photo.jpg", []byte("synthetic-image"))
	if err != nil {
		t.Fatal(err)
	}
	if result.ObjectID != "object-1" || result.Filename != "photo.jpg" {
		t.Fatalf("%+v", result)
	}
}

func TestEnrollFaceUsesLowercaseObjectID(t *testing.T) {
	c := academicClient(t, nil, roundtrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/apis/login/userLogin4Uname.do":
			return response(r, `{"status":true,"msg":{"name":"Synthetic User","uid":"10001","puid":"10001","fid":"20002"}}`), nil
		case "/api/changefaceid":
			if r.URL.RawQuery != "objectid=object-1&uid=10001&fid=20002" {
				t.Fatal(r.URL.RawQuery)
			}
			return response(r, `{"result":true}`), nil
		default:
			t.Fatalf("unexpected request %s", r.URL)
			return nil, nil
		}
	}))
	result, err := c.EnrollFace(context.Background(), "object-1")
	if err != nil || !result.Enrolled || result.ObjectID != "object-1" {
		t.Fatalf("%+v err=%v", result, err)
	}
}
