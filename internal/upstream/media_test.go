package upstream

import (
	"context"
	"net/http"
	"testing"
)

func TestMediaURLAllowlistAndHTTPSRewrite(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		host string
		want string
		ok   bool
	}{
		{
			name: "course http is upgraded",
			raw:  "http://p.ananas.chaoxing.com/star3/270_160c/cover.jpg",
			host: courseImageHost,
			want: "https://p.ananas.chaoxing.com/star3/270_160c/cover.jpg",
			ok:   true,
		},
		{
			name: "protocol relative course",
			raw:  "//p.ananas.chaoxing.com/star3/cover.jpg",
			host: courseImageHost,
			want: "https://p.ananas.chaoxing.com/star3/cover.jpg",
			ok:   true,
		},
		{
			name: "avatar host",
			raw:  "https://photo.chaoxing.com/p/123_120?flag=1&psize=120_120c",
			host: avatarHost,
			want: "https://photo.chaoxing.com/p/123_120?flag=1&psize=120_120c",
			ok:   true,
		},
		{name: "external host", raw: "https://evil.example/cover.jpg", host: courseImageHost},
		{name: "userinfo", raw: "https://user@p.ananas.chaoxing.com/cover.jpg", host: courseImageHost},
		{name: "custom port", raw: "https://p.ananas.chaoxing.com:8443/cover.jpg", host: courseImageHost},
		{name: "path traversal", raw: "https://p.ananas.chaoxing.com/star3/../cover.jpg", host: courseImageHost},
		{name: "fragment", raw: "https://p.ananas.chaoxing.com/cover.jpg#x", host: courseImageHost},
		{name: "other scheme", raw: "ftp://p.ananas.chaoxing.com/cover.jpg", host: courseImageHost},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := mediaURL(test.raw, test.host)
			if ok != test.ok {
				t.Fatalf("ok=%t want=%t", ok, test.ok)
			}
			if ok && got.String() != test.want {
				t.Fatalf("got %q want %q", got.String(), test.want)
			}
		})
	}
}

func TestFetchCoursesParsesCoverMetadata(t *testing.T) {
	c := testClient(t)
	c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
		return response(r, `{"result":1,"channelList":[{"cpi":"cpi-1","content":{"id":200,"name":"计科2405","isretire":0,"course":{"data":[{"id":100,"name":"操作系统","teacherfactor":"周军海","imageurl":"http://p.ananas.chaoxing.com/star3/270_160c/cover.jpg","schools":"湖南大学"}]}}}]}`), nil
	})
	value, err := c.Call(context.Background(), "list_courses", nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, ok := value.(map[string]any)["result"].([]Course)
	if !ok || len(rows) != 1 {
		t.Fatalf("unexpected result: %#v", value)
	}
	if !rows[0].CoverAvailable || rows[0].Schools != "湖南大学" {
		t.Fatalf("cover metadata missing: %+v", rows[0])
	}
	if rows[0].ImageURL != "https://p.ananas.chaoxing.com/star3/270_160c/cover.jpg" {
		t.Fatalf("cover url was not upgraded: %q", rows[0].ImageURL)
	}
}
