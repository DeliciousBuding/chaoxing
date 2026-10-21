package upstream

import (
	"context"
	"crypto/des"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
)

func lengthPrefixedField(field int, value []byte) []byte {
	var length [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(length[:], uint64(len(value)))
	out := []byte{byte(uint64(field)<<3 | 2)}
	out = append(out, length[:n]...)
	return append(out, value...)
}

func TestParseEasemobMessageExtractsAttachment(t *testing.T) {
	attachment := `{"attachmentType":15,"att_chat_course":{"aid":9001,"atype":2}}`
	keyValue := append(lengthPrefixedField(1, []byte("attachment")), lengthPrefixedField(6, []byte(attachment))...)
	message := lengthPrefixedField(5, keyValue)
	outer := lengthPrefixedField(6, message)

	got, ok := parseEasemobMessage(outer)
	if !ok {
		t.Fatal("valid message rejected")
	}
	if len(got.Exts) != 1 || got.Exts[0].Key != "attachment" || got.Exts[0].StringValue != attachment {
		t.Fatalf("%+v", got.Exts)
	}
}

func TestParseEasemobMessageRejectsTruncatedAndIncompleteMessages(t *testing.T) {
	inputs := [][]byte{
		nil,
		{0x32},
		{0x32, 0x20, 0x01},
	}
	for i, input := range inputs {
		if _, ok := parseEasemobMessage(input); ok {
			t.Fatalf("input %d accepted", i)
		}
	}

	keyValue := lengthPrefixedField(6, []byte("synthetic"))
	message := lengthPrefixedField(5, keyValue)
	outer := lengthPrefixedField(6, message)
	if _, ok := parseEasemobMessage(outer); ok {
		t.Fatal("key-value without attachment key accepted")
	}
}

func TestParseIMAttachmentNormalizesSignCard(t *testing.T) {
	item, ok := parseIMAttachment(`{"attachmentType":15,"att_chat_course":{"aid":9001,"atype":2,"atypeName":"密码签到","title":"课堂签到","subTitle":"","courseInfo":{"courseid":123,"classid":456,"coursename":"Synthetic Course"}}}`)
	if !ok {
		t.Fatal("sign card rejected")
	}
	if item.ID != "9001" || item.CourseID != "123" || item.ClassID != "456" || item.Course != "Synthetic Course" {
		t.Fatalf("%+v", item)
	}
	if item.Kind != "signin" || !item.Signable || item.SignType != "password" || item.RawOtherID != 5 {
		t.Fatalf("%+v", item)
	}
}

func TestParseIMAttachmentRejectsNonCourseCard(t *testing.T) {
	if _, ok := parseIMAttachment(`{"attachmentType":14}`); ok {
		t.Fatal("non-course attachment accepted")
	}
	if _, ok := parseIMAttachment(`{"attachmentType":15,"att_chat_course":{"aid":0,"atype":2}}`); ok {
		t.Fatal("card without activity id accepted")
	}
}

func TestIMTokenUsesReferenceJSONFormEnvelope(t *testing.T) {
	c := testClient(t)
	c.identity = &signIdentity{UID: "10001", IMPassword: encryptIMPasswordForTest(t, "synthetic-im-password")}
	c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "a1-vip6.easemob.com" || r.URL.Path != "/cx-dev/cxstudy/token" {
			t.Fatalf("unexpected request %s", r.URL)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Fatal(r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"grant_type":"password","password":"synthetic-im-password","username":"10001"}` {
			t.Fatal(string(body))
		}
		return response(r, `{"access_token":"token","user":{"uuid":"uuid","username":"10001"}}`), nil
	})
	cfg, err := c.imConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccessToken != "token" || cfg.Username != "10001" || cfg.UUID != "uuid" {
		t.Fatalf("%+v", cfg)
	}
}

func TestIMGroupFallbackUsesCourseDescription(t *testing.T) {
	c := testClient(t)
	c.im = &imConfig{AccessToken: "token", Username: "10001"}
	c.http.Transport = roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "a1-vip6.easemob.com" || !strings.Contains(r.URL.Path, "/joined_chatgroups") {
			t.Fatalf("unexpected request %s", r.URL)
		}
		return response(r, `{"data":[{"id":"group-1","description":"{\"courseInfo\":{\"coursename\":\"Synthetic Course\"}}"}]}`), nil
	})
	groups, err := c.fetchIMGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Name != "Synthetic Course" || groups[0].ID != "group-1" {
		t.Fatalf("%+v", groups)
	}
}

func encryptIMPasswordForTest(t *testing.T, value string) string {
	t.Helper()
	block, err := des.NewCipher([]byte(imPasswordDESK))
	if err != nil {
		t.Fatal(err)
	}
	padding := des.BlockSize - len(value)%des.BlockSize
	raw := []byte(value + strings.Repeat(string([]byte{byte(padding)}), padding))
	out := make([]byte, len(raw))
	for start := 0; start < len(raw); start += des.BlockSize {
		block.Encrypt(out[start:start+des.BlockSize], raw[start:start+des.BlockSize])
	}
	return hex.EncodeToString(out)
}
