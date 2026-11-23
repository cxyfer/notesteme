package hubconfig

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// blobs.driver: s3 的設定（ADR-0030 第 2 點；R2、MinIO、AWS S3 相容）。

// EnvS3PathStyle 是 path-style 的環境變數（true／false）。
const EnvS3PathStyle = "NOTESTEME_HUB_S3_PATH_STYLE"

// S3 的預設值。
const (
	DefaultS3Region  = "us-east-1"
	DefaultS3Timeout = 60 * time.Second
)

// S3Config 是 S3 相容儲存的設定。SecretAccessKey 只來自環境變數，String 不輸出它。
type S3Config struct {
	// Endpoint 是 S3 API 的網址（例如 https://<account>.r2.cloudflarestorage.com、http://minio:9000）；空字串代表 AWS S3。
	Endpoint string
	Region   string
	Bucket   string
	// Prefix 是物件鍵的前綴（共用 bucket 時用；空字串代表沒有），結尾不含 /。
	Prefix string
	// PathStyle 為 true 時用 <endpoint>/<bucket>/<key>（MinIO 需要），否則用 virtual-hosted style。
	PathStyle       bool
	AccessKeyID     string
	SecretAccessKey string
	// Timeout 是單一請求（上傳、下載整個物件、刪除、存在檢查）的逾時。
	Timeout time.Duration
}

// String 不輸出 secret（避免設定被印進 log 或錯誤訊息）。
func (c S3Config) String() string {
	secret := ""
	if c.SecretAccessKey != "" {
		secret = "******"
	}
	return fmt.Sprintf("{Endpoint:%s Region:%s Bucket:%s Prefix:%s PathStyle:%t AccessKeyID:%s SecretAccessKey:%s Timeout:%s}",
		c.Endpoint, c.Region, c.Bucket, c.Prefix, c.PathStyle, c.AccessKeyID, secret, c.Timeout)
}

// GoString 同 String（%#v 也不輸出 secret）。
func (c S3Config) GoString() string { return c.String() }

type fieldError struct{ field, msg string }

func (e *fieldError) Error() string { return e.field + "：" + e.msg }

// bucketName 是 S3 的 bucket 命名規則（小寫、數字、. 與 -，3～63 字元）。
var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func applyS3File(c *S3Config, f *s3Schema) *fieldError {
	if f.Endpoint != "" {
		c.Endpoint = strings.TrimSpace(f.Endpoint)
	}
	if f.Region != "" {
		c.Region = strings.TrimSpace(f.Region)
	}
	if f.Bucket != "" {
		c.Bucket = strings.TrimSpace(f.Bucket)
	}
	if f.Prefix != "" {
		c.Prefix = strings.Trim(strings.TrimSpace(f.Prefix), "/")
	}
	if f.PathStyle != nil {
		c.PathStyle = *f.PathStyle
	}
	if f.AccessKeyID != "" {
		c.AccessKeyID = strings.TrimSpace(f.AccessKeyID)
	}
	if f.Timeout != "" {
		d, err := time.ParseDuration(strings.TrimSpace(f.Timeout))
		if err != nil || d <= 0 {
			return &fieldError{"timeout", "必須是大於 0 的時間長度（例如 60s）。"}
		}
		c.Timeout = d
	}
	return nil
}

func applyS3Env(c *S3Config, env map[string]string) {
	set := func(dst *string, k string) {
		if v := strings.TrimSpace(env[k]); v != "" {
			*dst = v
		}
	}
	set(&c.Endpoint, EnvS3Endpoint)
	set(&c.Region, EnvS3Region)
	set(&c.Bucket, EnvS3Bucket)
	set(&c.AccessKeyID, EnvS3AccessKeyID)
	// secret 不 trim 以外的處理，也不從設定檔讀。
	c.SecretAccessKey = strings.TrimSpace(env[EnvS3SecretAccessKey])
	if v, err := strconv.ParseBool(strings.TrimSpace(env[EnvS3PathStyle])); err == nil {
		c.PathStyle = v
	}
}

// validate 檢查 s3 driver 的必要欄位並補上預設值。錯誤訊息不含任何憑證。
func (c *S3Config) validate() error {
	if c.Region == "" {
		c.Region = DefaultS3Region
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultS3Timeout
	}
	if !bucketName.MatchString(c.Bucket) {
		return fmt.Errorf("blobs.driver 是 s3 時要設定 bucket（設定檔的 blobs.s3.bucket 或環境變數 %s），名稱只能有小寫字母、數字、. 與 -。", EnvS3Bucket)
	}
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" {
			return errors.New("blobs.s3.endpoint（或 " + EnvS3Endpoint + "）必須是不含帳密與查詢字串的 http(s) 網址。")
		}
		c.Endpoint = strings.TrimRight(c.Endpoint, "/")
	}
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return fmt.Errorf("blobs.driver 是 s3 時要設定 access key（blobs.s3.access_key_id 或 %s）與環境變數 %s。", EnvS3AccessKeyID, EnvS3SecretAccessKey)
	}
	return nil
}

// dsnHasPassword 回報 Postgres 的 DSN（URL 或 key=value 格式）是否含密碼。
func dsnHasPassword(dsn string) bool {
	if u, err := url.Parse(dsn); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		if _, ok := u.User.Password(); ok {
			return true
		}
		return u.Query().Has("password")
	}
	// 解析不了的 URL：保守地看 userinfo 有沒有冒號。
	if _, rest, ok := strings.Cut(dsn, "://"); ok {
		if userinfo, _, ok := strings.Cut(rest, "@"); ok && strings.Contains(userinfo, ":") {
			return true
		}
	}
	for _, f := range strings.Fields(dsn) {
		if k, _, ok := strings.Cut(f, "="); ok && strings.TrimSpace(k) == "password" {
			return true
		}
	}
	return false
}
