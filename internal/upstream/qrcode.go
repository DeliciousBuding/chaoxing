package upstream

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

const signDetailAPI = "https://mobilelearn.chaoxing.com/newsign/signDetail"

type qrCodePayload struct {
	AID  string
	Code string
	Enc  string
}

func parseQRCode(rawValue string) (qrCodePayload, error) {
	rawValue = strings.TrimSpace(rawValue)
	if rawValue == "" {
		return qrCodePayload{}, fault("INVALID_ARGUMENT", "qr_payload is empty")
	}
	if strings.HasPrefix(rawValue, "SIGNIN:") {
		content := strings.SplitN(strings.TrimPrefix(rawValue, "SIGNIN:"), "-", 2)[0]
		enc := queryFragmentValue(content, "enc")
		if enc == "" {
			return qrCodePayload{}, fault("INVALID_ARGUMENT", "qr_payload has no enc parameter")
		}
		signDetail := strings.Split(content, "&enc=")[0]
		return qrCodePayload{
			AID:  firstQueryValue(signDetail),
			Code: lastQueryValue(signDetail),
			Enc:  enc,
		}, nil
	}
	parsed, err := url.Parse(rawValue)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return qrCodePayload{}, fault("INVALID_ARGUMENT", "qr_payload is not a recognized QR code")
	}
	q := parsed.Query()
	enc := strings.TrimSpace(q.Get("enc"))
	if enc == "" {
		return qrCodePayload{}, fault("INVALID_ARGUMENT", "qr_payload has no enc parameter")
	}
	aid := firstNonBlank(q.Get("id"), q.Get("aid"))
	return qrCodePayload{AID: aid, Code: strings.TrimSpace(q.Get("c")), Enc: enc}, nil
}

func (c *Client) verifyQRCode(ctx context.Context, activityID string, payload qrCodePayload) error {
	if payload.Code == "" {
		return nil
	}
	aid := payload.AID
	if aid == "" {
		aid = activityID
	}
	b, err := c.get(ctx, signDetailAPI+"?"+orderedQuery("activePrimaryId", aid, "type", "1", "msg", payload.Code))
	if err != nil {
		return err
	}
	var detail struct {
		IsOver   int    `json:"isOver"`
		SignCode string `json:"signCode"`
	}
	if json.Unmarshal(b, &detail) != nil {
		return fault("UPSTREAM_SCHEMA", "QR sign detail not recognized")
	}
	if detail.IsOver == 1 || detail.SignCode != payload.Code {
		return fault("SIGN_QR_EXPIRED", "QR code is expired")
	}
	return nil
}

func queryFragmentValue(value, key string) string {
	for _, part := range strings.Split(value, "&") {
		if keyValue := strings.SplitN(part, "=", 2); len(keyValue) == 2 && keyValue[0] == key {
			return strings.TrimSpace(keyValue[1])
		}
	}
	return ""
}

func firstQueryValue(value string) string {
	first := strings.SplitN(value, "&", 2)[0]
	return strings.TrimPrefix(first, strings.Split(first, "=")[0]+"=")
}

func lastQueryValue(value string) string {
	last := value
	if index := strings.LastIndex(value, "&"); index >= 0 {
		last = value[index+1:]
	}
	return strings.TrimPrefix(last, strings.Split(last, "=")[0]+"=")
}
