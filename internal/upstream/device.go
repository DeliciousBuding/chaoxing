package upstream

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/DeliciousBuding/chaoxing/internal/persist"
)

const (
	defaultUserAgent   = "Dalvik/2.1.0 (Linux; U; Android 12; SM-N9006 Build/8aba9e4.0) (schild:2d97f7b9439f21333c946878fc4d6ccb) (device:SM-N9006) Language/zh_CN com.chaoxing.mobile/ChaoXingStudy_3_6.7.5_android_phone_10941_314 (@Kalimdor)_68f184fd763546c1a04ab3a09b3deebb"
	defaultPackageName = "com.chaoxing.mobile"
	deviceFingerprint  = "samsung/SM-N9006/SM-N9006:12/8aba9e4.0/8aba9e4.0"
	userInfoAPI        = "https://sso.chaoxing.com/apis/login/userLogin4Uname.do"
)

const deviceInfoPublicKey = "MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQC79d8Ot0hCbxxSISC6x8SCwTBspFSzlLKHJUYqoFNu1TSRaw4hEYkOnvEaL1VyoxV6HXcDrzwYvaFZaZaPQPFnfCHZy5dQwxcmifgSHqS+oKXw40Ys4cVIqnU5d90S7EWSRdBglX489jlqVaNcQSkDx2TYmC+DbAq9FV/BU09ISQIDAQAB"

type deviceState struct {
	Version        int       `json:"version"`
	DeviceCode     string    `json:"device_code"`
	AndroidID      string    `json:"android_id"`
	MediaDrmID     string    `json:"media_drm_id"`
	DeviceUniqueID string    `json:"device_unique_id"`
	UserAgent      string    `json:"user_agent"`
	PackageName    string    `json:"package_name"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type signIdentity struct {
	UID        string
	FID        string
	Name       string
	IMPassword string
}

func (c *Client) loadDeviceState() error {
	path := c.deviceStatePath()
	if path == "" {
		state, err := newDeviceState(c.config.DeviceCode, c.config.UserAgent, c.config.PackageName)
		if err != nil {
			return err
		}
		c.device = state
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			state, newErr := newDeviceState(c.config.DeviceCode, c.config.UserAgent, c.config.PackageName)
			if newErr != nil {
				return newErr
			}
			c.device = state
			return c.saveDeviceState()
		}
		return fault("STATE_READ_FAILED", "device state is unavailable")
	}
	var state deviceState
	if json.Unmarshal(b, &state) != nil || state.Version != 1 || state.DeviceCode == "" {
		return fault("STATE_INVALID", "device state is invalid")
	}
	if err = validateDeviceConfig(state.DeviceCode, state.UserAgent, state.PackageName); err != nil {
		return fault("STATE_INVALID", "device state is invalid")
	}
	c.device = state
	c.applyDeviceOverrides()
	return nil
}

func (c *Client) deviceStatePath() string {
	if c.config.DeviceStatePath != "" {
		return c.config.DeviceStatePath
	}
	if c.config.SessionPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(c.config.SessionPath), "device-state.json")
}

func (c *Client) applyDeviceOverrides() {
	if c.config.DeviceCode != "" {
		c.device.DeviceCode = c.config.DeviceCode
	}
	if c.config.UserAgent != "" {
		c.device.UserAgent = c.config.UserAgent
	}
	if c.config.PackageName != "" {
		c.device.PackageName = c.config.PackageName
	}
}

func (c *Client) saveDeviceState() error {
	path := c.deviceStatePath()
	if path == "" {
		return nil
	}
	c.device.Version = 1
	c.device.UpdatedAt = time.Now().UTC()
	if c.device.CreatedAt.IsZero() {
		c.device.CreatedAt = c.device.UpdatedAt
	}
	b, err := json.Marshal(c.device)
	if err != nil {
		return fault("STATE_WRITE_FAILED", "device state encoding failed")
	}
	if err = persist.Write(path, b, 0o600); err != nil {
		return fault("STATE_WRITE_FAILED", "device state replace failed")
	}
	return nil
}

func newDeviceState(code, userAgent, packageName string) (deviceState, error) {
	now := time.Now().UTC()
	if code == "" {
		var err error
		if code, err = randomizedDeviceCode(); err != nil {
			return deviceState{}, err
		}
	}
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	if packageName == "" {
		packageName = defaultPackageName
	}
	androidID, err := randomHex(8)
	if err != nil {
		return deviceState{}, err
	}
	mediaDrmID, err := randomBase64(32)
	if err != nil {
		return deviceState{}, err
	}
	unique := deviceUniqueID(packageName, androidID)
	return deviceState{
		Version: 1, DeviceCode: code, AndroidID: androidID, MediaDrmID: mediaDrmID,
		DeviceUniqueID: unique, UserAgent: userAgent, PackageName: packageName,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func deviceUniqueID(packageName, androidID string) string {
	sum := sha256.Sum256([]byte(packageName + ":" + androidID + ":" + deviceFingerprint))
	return hex.EncodeToString(sum[:])
}

// randomizedDeviceCode mirrors the reference client's stable random fallback:
// SHA-256 of 64 hexadecimal characters, then the digest repeated and Base64ed.
func randomizedDeviceCode() (string, error) {
	raw, err := randomHex(32)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(raw))
	return base64.StdEncoding.EncodeToString(append(sum[:], sum[:]...)), nil
}

func randomHex(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomBase64(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func (c *Client) userAgent() string {
	if c.device.UserAgent != "" {
		return c.device.UserAgent
	}
	return defaultUserAgent
}

type deviceInfoPayload struct {
	DeviceUniqueID string `json:"deviceUniqueId"`
	CDID           string `json:"cdid"`
	DeviceID       string `json:"device_id"`
	AndroidID      string `json:"android_id"`
	MediaDrmID     string `json:"mediaDrmId"`
	OAID           string `json:"oaid"`
	Platform       string `json:"platform"`
	OSName         string `json:"os_name"`
	OSVer          string `json:"os_ver"`
	OSLang         string `json:"os_lang"`
	Brand          string `json:"brand"`
	Board          string `json:"board"`
	Hardware       string `json:"hardware"`
	Model          string `json:"model"`
	CPUAR          string `json:"cpu_ar"`
	AppName        string `json:"app_name"`
	AppVer         string `json:"app_ver"`
	VersionCode    string `json:"versionCode"`
	Signatures     string `json:"signatures"`
	Resolution     string `json:"resolution"`
	DPI            string `json:"dpi"`
	TimeStamp      int64  `json:"time_stamp"`
}

func (c *Client) encryptedDeviceInfo() (string, error) {
	payload := deviceInfoPayload{
		DeviceUniqueID: c.device.DeviceUniqueID, CDID: c.device.DeviceUniqueID, DeviceID: c.device.DeviceUniqueID,
		AndroidID: c.device.AndroidID, MediaDrmID: c.device.MediaDrmID, Platform: "android", OSName: "android",
		OSVer: "12", OSLang: "zh-CN", Brand: "samsung", Board: "universal8890", Hardware: "exynos5422",
		Model: "SM-N9006", CPUAR: "armeabi-v7a,armeabi", AppName: c.device.PackageName, AppVer: "6.7.5",
		VersionCode: "10941", Signatures: "1e27068798b6697821abbeb44a17da5483c4fb7fad7b9ce7890465ed04d0cbe0",
		Resolution: "1080*1920", DPI: "3.0", TimeStamp: time.Now().UnixMilli(),
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return "", fault("INTERNAL", "device info encoding failed")
	}
	keyBytes, err := base64.StdEncoding.DecodeString(deviceInfoPublicKey)
	if err != nil {
		return "", fault("INTERNAL", "device public key unavailable")
	}
	publicKey, err := x509.ParsePKIXPublicKey(keyBytes)
	if err != nil {
		return "", fault("INTERNAL", "device public key unavailable")
	}
	rsaKey, ok := publicKey.(*rsa.PublicKey)
	if !ok {
		return "", fault("INTERNAL", "device public key unavailable")
	}
	var output []byte
	for len(plain) > 0 {
		size := len(plain)
		if size > 117 {
			size = 117
		}
		block, err := rsa.EncryptPKCS1v15(rand.Reader, rsaKey, plain[:size])
		if err != nil {
			return "", fault("INTERNAL", "device info encryption failed")
		}
		output = append(output, block...)
		plain = plain[size:]
	}
	return base64.StdEncoding.EncodeToString(output), nil
}

func (c *Client) fetchSignIdentity(ctx context.Context) (signIdentity, error) {
	if c.identity != nil {
		return *c.identity, nil
	}
	data, err := c.encryptedDeviceInfo()
	if err != nil {
		return signIdentity{}, err
	}
	b, err := c.authenticatedRead(ctx, "POST", userInfoAPI, url.Values{"data": {data}})
	if err != nil {
		return signIdentity{}, err
	}
	var payload struct {
		Msg struct {
			Name        string `json:"name"`
			UID         any    `json:"uid"`
			FID         any    `json:"fid"`
			PUID        any    `json:"puid"`
			AccountInfo struct {
				IMAccount struct {
					Password string `json:"password"`
				} `json:"imAccount"`
			} `json:"accountInfo"`
		} `json:"msg"`
	}
	if json.Unmarshal(b, &payload) != nil {
		return signIdentity{}, fault("AUTH_REQUIRED", "account identity unavailable")
	}
	uid := id(payload.Msg.UID)
	puid := id(payload.Msg.PUID)
	if uid == "" {
		uid = puid
	}
	fid := id(payload.Msg.FID)
	if uid == "" || fid == "" || strings.TrimSpace(payload.Msg.Name) == "" {
		return signIdentity{}, fault("UPSTREAM_SCHEMA", "account identity not recognized")
	}
	c.identity = &signIdentity{
		UID: uid, FID: fid, Name: strings.TrimSpace(payload.Msg.Name),
		IMPassword: strings.TrimSpace(payload.Msg.AccountInfo.IMAccount.Password),
	}
	return *c.identity, nil
}

var validPackageName = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)+$`)

func validateDeviceConfig(code, userAgent, packageName string) error {
	if code != "" && (len(code) > 512 || !validBase64(code)) {
		return errors.New("device code must be standard Base64")
	}
	if userAgent != "" && (len(userAgent) > 1024 || strings.ContainsFunc(userAgent, func(r rune) bool { return r < 0x20 || r > 0x7e })) {
		return errors.New("device user_agent must be printable ASCII")
	}
	if packageName != "" && !validPackageName.MatchString(packageName) {
		return errors.New("device package_name is invalid")
	}
	return nil
}

func validBase64(value string) bool {
	b, err := base64.StdEncoding.Strict().DecodeString(value)
	return err == nil && len(b) > 0
}
