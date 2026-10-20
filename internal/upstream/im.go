package upstream

import (
	"bytes"
	"context"
	"crypto/des"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	imTokenURL     = "https://a1-vip6.easemob.com/cx-dev/cxstudy/token"
	imGroupsURL    = "https://a1-vip6.easemob.com/cx-dev/cxstudy/users/%s/joined_chatgroups?detail=true&version=v3&pagenum=1&pagesize=200"
	imRoamingURL   = "https://a1-vip6.easecdn.com/cx-dev/cxstudy/users/%s/messageroaming"
	imUserAgent    = "Easemob-SDK(Android) 4.9.0.1"
	imPasswordDESK = "SL2(M/eD"
)

type imConfig struct {
	AccessToken string
	Username    string
	UUID        string
}

type imGroup struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// ListGroupActivities reads sign-in cards from Easemob group history. It is a
// read-only discovery source; actual submission still goes through SignActivity.
func (c *Client) ListGroupActivities(ctx context.Context) ([]Activity, error) {
	if err := c.lock(ctx); err != nil {
		return nil, err
	}
	defer c.queries.Release()
	return c.listGroupActivities(ctx)
}

func (c *Client) listGroupActivities(ctx context.Context) ([]Activity, error) {
	if !c.config.GroupActivities {
		return []Activity{}, nil
	}
	if c.imActivities != nil && time.Since(c.imActivitiesAt) < 15*time.Second {
		return append([]Activity(nil), c.imActivities...), nil
	}
	cfg, err := c.imConfig(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := c.fetchIMGroups(ctx)
	if err != nil {
		return nil, err
	}
	if len(groups) > 20 {
		groups = groups[:20]
	}
	seen := map[string]bool{}
	rows := []Activity{}
	successfulGroups := 0
	var firstGroupErr error
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, err := c.imGroupActivities(ctx, cfg, group)
		if err != nil {
			if firstGroupErr == nil {
				firstGroupErr = err
			}
			continue
		}
		successfulGroups++
		for _, item := range items {
			k := item.CourseID + ":" + item.ClassID + ":" + item.ID
			if seen[k] {
				continue
			}
			seen[k] = true
			item.Source = "group"
			item.GroupName = group.Name
			rows = append(rows, item)
		}
	}
	if len(groups) > 0 && successfulGroups == 0 && firstGroupErr != nil {
		return nil, firstGroupErr
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].StartsAt == nil || rows[j].StartsAt == nil {
			return rows[i].StartsAt != nil
		}
		return rows[i].StartsAt.After(*rows[j].StartsAt)
	})
	c.imActivities, c.imActivitiesAt = rows, time.Now()
	return rows, nil
}

func (c *Client) imConfig(ctx context.Context) (imConfig, error) {
	if c.im != nil {
		return *c.im, nil
	}
	identity, err := c.fetchSignIdentity(ctx)
	if err != nil {
		return imConfig{}, err
	}
	if identity.IMPassword == "" {
		return imConfig{}, fault("UPSTREAM_SCHEMA", "IM account password unavailable")
	}
	password, err := decryptIMPassword(identity.IMPassword)
	if err != nil {
		return imConfig{}, fault("UPSTREAM_SCHEMA", "IM account password not recognized")
	}
	tokenRequest, err := json.Marshal(map[string]string{
		"grant_type": "password", "username": identity.UID, "password": password,
	})
	if err != nil {
		return imConfig{}, fault("UPSTREAM_SCHEMA", "IM token request not recognized")
	}
	body, err := c.imDo(ctx, http.MethodPost, imTokenURL, bytes.NewReader(tokenRequest), "application/x-www-form-urlencoded", "")
	if err != nil {
		return imConfig{}, err
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		User        struct {
			UUID     string `json:"uuid"`
			Username string `json:"username"`
		} `json:"user"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.AccessToken == "" || payload.User.Username == "" {
		return imConfig{}, fault("UPSTREAM_SCHEMA", "IM token response not recognized")
	}
	cfg := imConfig{AccessToken: payload.AccessToken, Username: payload.User.Username, UUID: payload.User.UUID}
	c.im = &cfg
	return cfg, nil
}

func (c *Client) fetchIMGroups(ctx context.Context) ([]imGroup, error) {
	if c.imGroups != nil && time.Since(c.imGroupsAt) < 10*time.Minute {
		return append([]imGroup(nil), c.imGroups...), nil
	}
	cfg, err := c.imConfig(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.imDo(ctx, http.MethodGet, fmt.Sprintf(imGroupsURL, cfg.Username), nil, "", "Bearer "+cfg.AccessToken)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data []struct {
			Name        string `json:"name"`
			ID          string `json:"id"`
			Description string `json:"description"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return nil, fault("UPSTREAM_SCHEMA", "IM group list not recognized")
	}
	groups := []imGroup{}
	for _, item := range payload.Data {
		if strings.TrimSpace(item.ID) != "" {
			name := strings.TrimSpace(item.Name)
			if name == "" {
				var description struct {
					CourseInfo struct {
						CourseName string `json:"coursename"`
					} `json:"courseInfo"`
				}
				if json.Unmarshal([]byte(item.Description), &description) == nil {
					name = strings.TrimSpace(description.CourseInfo.CourseName)
				}
			}
			groups = append(groups, imGroup{Name: name, ID: strings.TrimSpace(item.ID)})
		}
	}
	c.imGroups, c.imGroupsAt = groups, time.Now()
	return groups, nil
}

func (c *Client) imGroupActivities(ctx context.Context, cfg imConfig, group imGroup) ([]Activity, error) {
	payload := map[string]any{"end": -1, "queue": group.ID + "@conference.easemob.com", "start": -1}
	b, _ := json.Marshal(payload)
	body, err := c.imDo(ctx, http.MethodPost, fmt.Sprintf(imRoamingURL, cfg.Username), bytes.NewReader(b), "text/plain;charset=UTF-8", "Bearer "+cfg.AccessToken)
	if err != nil {
		return nil, err
	}
	var roaming struct {
		Data struct {
			Msgs []struct {
				Msg string `json:"msg"`
			} `json:"msgs"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &roaming) != nil {
		return nil, fault("UPSTREAM_SCHEMA", "IM history not recognized")
	}
	rows := []Activity{}
	for _, message := range roaming.Data.Msgs {
		raw, err := base64.StdEncoding.DecodeString(message.Msg)
		if err != nil {
			continue
		}
		messageBody, ok := parseEasemobMessage(raw)
		if !ok {
			continue
		}
		for _, ext := range messageBody.Exts {
			if ext.Key != "attachment" {
				continue
			}
			item, ok := parseIMAttachment(ext.StringValue)
			if ok {
				rows = append(rows, item)
			}
		}
	}
	return rows, nil
}

type easemobMessage struct {
	Exts []easemobKeyValue
}

type easemobKeyValue struct {
	Key         string
	StringValue string
}

func parseEasemobMessage(meta []byte) (easemobMessage, bool) {
	fields := parseProtobuf(meta)
	body, ok := fields[6][0].([]byte)
	if !ok {
		return easemobMessage{}, false
	}
	messageFields := parseProtobuf(body)
	out := easemobMessage{}
	for _, value := range messageFields[5] {
		raw, ok := value.([]byte)
		if !ok {
			continue
		}
		keyValue := parseProtobuf(raw)
		key, _ := keyValue[1][0].(string)
		stringValue, _ := keyValue[6][0].(string)
		if key == "attachment" && stringValue != "" {
			out.Exts = append(out.Exts, easemobKeyValue{Key: key, StringValue: stringValue})
		}
	}
	return out, len(out.Exts) > 0
}

func parseIMAttachment(value string) (Activity, bool) {
	var attachment struct {
		AttachmentType int `json:"attachmentType"`
		SignInfo       struct {
			AID        int64  `json:"aid"`
			AType      int    `json:"atype"`
			ATypeName  string `json:"atypeName"`
			Title      string `json:"title"`
			SubTitle   string `json:"subTitle"`
			CourseInfo struct {
				CourseID   int64  `json:"courseid"`
				ClassID    int    `json:"classid"`
				CourseName string `json:"coursename"`
			} `json:"courseInfo"`
		} `json:"att_chat_course"`
	}
	if json.Unmarshal([]byte(value), &attachment) != nil || attachment.AttachmentType != 15 {
		return Activity{}, false
	}
	sign := attachment.SignInfo
	if sign.AID == 0 || (sign.AType != 2 && sign.AType != 74) {
		return Activity{}, false
	}
	return Activity{
		ID: fmt.Sprintf("%d", sign.AID), CourseID: fmt.Sprintf("%d", sign.CourseInfo.CourseID),
		ClassID: fmt.Sprintf("%d", sign.CourseInfo.ClassID), Course: sign.CourseInfo.CourseName,
		Title: firstNonBlank(sign.Title, sign.SubTitle, sign.ATypeName), Kind: "signin",
		StartsAtTitle: strings.TrimSpace(sign.SubTitle),
		State:         "active", UserState: "unknown", RawType: sign.AType, RawOtherID: int(signTypeNumber(sign.ATypeName)),
		SignType: signType(signTypeNumber(sign.ATypeName)), Signable: true,
	}, true
}

func signTypeNumber(name string) int64 {
	switch strings.TrimSpace(name) {
	case "拍照签到":
		return 0
	case "二维码签到":
		return 2
	case "手势签到":
		return 3
	case "位置签到":
		return 4
	case "密码签到", "签到码签到":
		return 5
	default:
		return -1
	}
}

func decryptIMPassword(encrypted string) (string, error) {
	raw, err := hex.DecodeString(encrypted)
	if err != nil || len(raw) == 0 || len(raw)%des.BlockSize != 0 {
		return "", fmt.Errorf("invalid IM password envelope")
	}
	block, err := des.NewCipher([]byte(imPasswordDESK))
	if err != nil {
		return "", err
	}
	out := make([]byte, len(raw))
	for start := 0; start < len(raw); start += des.BlockSize {
		block.Decrypt(out[start:start+des.BlockSize], raw[start:start+des.BlockSize])
	}
	if len(out) == 0 {
		return "", fmt.Errorf("empty IM password")
	}
	padding := int(out[len(out)-1])
	if padding == 0 || padding > des.BlockSize || padding > len(out) {
		return "", fmt.Errorf("invalid IM password padding")
	}
	for _, v := range out[len(out)-padding:] {
		if int(v) != padding {
			return "", fmt.Errorf("invalid IM password padding")
		}
	}
	return string(out[:len(out)-padding]), nil
}

func (c *Client) imDo(ctx context.Context, method, endpoint string, body io.Reader, contentType, authorization string) ([]byte, error) {
	req, err := http.NewRequestWithContext(c.requestContext(ctx), method, endpoint, body)
	if err != nil {
		return nil, fault("INVALID_URL", "IM endpoint not permitted")
	}
	req.Header.Set("User-Agent", imUserAgent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fault("UPSTREAM_UNAVAILABLE", "IM transport failed")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if err != nil {
		return nil, fault("UPSTREAM_UNAVAILABLE", "IM response read failed")
	}
	if len(b) > 1024*1024 {
		return nil, fault("RESPONSE_TOO_LARGE", "IM response exceeds limit")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fault("UPSTREAM_HTTP", fmt.Sprintf("IM HTTP %d", resp.StatusCode))
	}
	return b, nil
}

// parseProtobuf decodes only the wire types needed by the Easemob message
// schema. It intentionally does not become a general protobuf package.
func parseProtobuf(b []byte) map[int][]any {
	out := map[int][]any{}
	for i := 0; i < len(b); {
		key, n := protobufVarint(b[i:])
		if n <= 0 {
			return out
		}
		i += n
		field := int(key >> 3)
		wire := int(key & 7)
		switch wire {
		case 0:
			value, n := protobufVarint(b[i:])
			if n <= 0 {
				return out
			}
			i += n
			out[field] = append(out[field], value)
		case 1:
			if i+8 > len(b) {
				return out
			}
			out[field] = append(out[field], b[i:i+8])
			i += 8
		case 2:
			length, n := protobufVarint(b[i:])
			if n <= 0 || i+n+int(length) > len(b) {
				return out
			}
			i += n
			out[field] = append(out[field], b[i:i+int(length)])
			i += int(length)
		case 5:
			if i+4 > len(b) {
				return out
			}
			out[field] = append(out[field], b[i:i+4])
			i += 4
		default:
			return out
		}
	}
	return out
}

func protobufVarint(b []byte) (uint64, int) {
	var out uint64
	for i := 0; i < len(b) && i < 10; i++ {
		out |= uint64(b[i]&0x7f) << (7 * i)
		if b[i]&0x80 == 0 {
			return out, i + 1
		}
	}
	return 0, -1
}
