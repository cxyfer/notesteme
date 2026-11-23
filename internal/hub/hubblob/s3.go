package hubblob

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // Content-MD5 只用來讓 S3 檢查傳輸完整性，不是安全用途
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3 相容儲存（R2、MinIO、AWS S3；ADR-0030 第 2 點）。物件鍵是 [<prefix>/]<sha256>，內容相同的上傳天然去重。
// 不依賴本機路徑：上傳前在記憶體中（或直接對 io.ReadSeeker）計算 sha256，最多 MaxObjectBytes。
// 錯誤訊息不含 secret；連線不經過 netguard（endpoint 是 owner 設定的，常在私有網路上，例如 compose 中的 MinIO）。

// ErrTooLarge 表示內容超過 S3Options.MaxObjectBytes。
var ErrTooLarge = errors.New("blob 超過大小上限")

// ErrCorrupt 表示下載的內容長度或 sha256 與鍵不符（物件被截斷或損壞）。
var ErrCorrupt = errors.New("blob 的內容不完整或已損壞")

// DefaultS3MaxObjectBytes 是沒有指定時的單一物件上限。
const DefaultS3MaxObjectBytes = 64 << 20

// S3Options 是 NewS3 的設定。
type S3Options struct {
	// Endpoint 是 S3 API 的網址；空字串代表 AWS S3（依 Region）。
	Endpoint  string
	Region    string
	Bucket    string
	Prefix    string
	PathStyle bool
	// AccessKeyID、SecretAccessKey 是靜態憑證；不讀主機上的 AWS 設定檔、環境變數或 IMDS。
	AccessKeyID     string
	SecretAccessKey string
	// Timeout 是單一請求的逾時（Open 時涵蓋讀完整個物件）；0 代表 60 秒。
	Timeout time.Duration
	// MaxObjectBytes 是 Put 接受的最大內容；0 代表 DefaultS3MaxObjectBytes。
	MaxObjectBytes int64
	// HTTPClient 給測試注入；nil 時用預設的 client。
	HTTPClient *http.Client
}

// S3 是 S3 相容儲存上的 blob 儲存。
type S3 struct {
	client  *s3.Client
	bucket  string
	prefix  string
	timeout time.Duration
	max     int64
	secret  string
}

var _ Store = (*S3)(nil)

// NewS3 建立 S3 相容儲存的 Store；不會連線（doctor 用 Ping 檢查）。
func NewS3(o S3Options) (*S3, error) {
	if o.Bucket == "" {
		return nil, errors.New("S3 的 bucket 不能是空的")
	}
	if o.AccessKeyID == "" || o.SecretAccessKey == "" {
		return nil, errors.New("S3 的 access key 與 secret 都要設定")
	}
	if o.Region == "" {
		o.Region = "us-east-1"
	}
	if o.Timeout <= 0 {
		o.Timeout = 60 * time.Second
	}
	if o.MaxObjectBytes <= 0 {
		o.MaxObjectBytes = DefaultS3MaxObjectBytes
	}
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	}
	creds := aws.Credentials{AccessKeyID: o.AccessKeyID, SecretAccessKey: o.SecretAccessKey, Source: "notesteme-hub"}
	opts := s3.Options{
		Region:       o.Region,
		Credentials:  aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) { return creds, nil }),
		UsePathStyle: o.PathStyle,
		HTTPClient:   hc,
		// 新版 SDK 預設附上 CRC32 checksum，部分 S3 相容服務不接受；完整性改由 Content-MD5 與下載時的 sha256 檢查。
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	if o.Endpoint != "" {
		opts.BaseEndpoint = aws.String(strings.TrimRight(o.Endpoint, "/"))
	}
	return &S3{
		client: s3.New(opts), bucket: o.Bucket, prefix: strings.Trim(o.Prefix, "/"),
		timeout: o.Timeout, max: o.MaxObjectBytes, secret: o.SecretAccessKey,
	}, nil
}

func (s *S3) key(sha string) string {
	if s.prefix == "" {
		return sha
	}
	return s.prefix + "/" + sha
}

// wrap 把 SDK 的錯誤轉成不含 secret 的錯誤（保留 context 的逾時與取消，讓呼叫端用 errors.Is 判斷）。
func (s *S3) wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if s.secret != "" {
		msg = strings.ReplaceAll(msg, s.secret, "******")
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("S3 %s 逾時：%w", op, context.DeadlineExceeded)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("S3 %s 已取消：%w", op, context.Canceled)
	}
	return fmt.Errorf("S3 %s 失敗：%s", op, msg)
}

func isNotFound(err error) bool {
	var re *awshttp.ResponseError
	return errors.As(err, &re) && re.HTTPStatusCode() == http.StatusNotFound
}

// spool 讀取 r 並計算 sha256 與 MD5，回傳可以重複讀的內容；超過上限時回 ErrTooLarge。
// r 是 io.ReadSeeker 時不複製，算完雜湊後 seek 回原位。
func (s *S3) spool(r io.Reader) (body io.ReadSeeker, sha string, md5sum []byte, n int64, err error) {
	h, m := sha256.New(), md5.New() //nolint:gosec
	w := io.MultiWriter(h, m)
	limited := io.LimitReader(r, s.max+1)
	if rs, ok := r.(io.ReadSeeker); ok {
		start, err := rs.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, "", nil, 0, err
		}
		if n, err = io.Copy(w, limited); err != nil {
			return nil, "", nil, 0, err
		}
		if n > s.max {
			return nil, "", nil, 0, ErrTooLarge
		}
		if _, err := rs.Seek(start, io.SeekStart); err != nil {
			return nil, "", nil, 0, err
		}
		return &boundedSeeker{rs: rs, start: start, n: n}, sumHex(h), m.Sum(nil), n, nil
	}
	var buf bytes.Buffer
	if n, err = io.Copy(io.MultiWriter(&buf, w), limited); err != nil {
		return nil, "", nil, 0, err
	}
	if n > s.max {
		return nil, "", nil, 0, ErrTooLarge
	}
	return bytes.NewReader(buf.Bytes()), sumHex(h), m.Sum(nil), n, nil
}

func sumHex(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }

// boundedSeeker 只讀 [start, start+n)：SDK 重試時會 seek 回開頭。
type boundedSeeker struct {
	rs       io.ReadSeeker
	start, n int64
	off      int64
}

func (b *boundedSeeker) Read(p []byte) (int, error) {
	if b.off >= b.n {
		return 0, io.EOF
	}
	if rem := b.n - b.off; int64(len(p)) > rem {
		p = p[:rem]
	}
	k, err := b.rs.Read(p)
	b.off += int64(k)
	if err == io.EOF && b.off < b.n {
		err = io.ErrUnexpectedEOF
	}
	return k, err
}

func (b *boundedSeeker) Seek(off int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = off
	case io.SeekCurrent:
		abs = b.off + off
	case io.SeekEnd:
		abs = b.n + off
	default:
		return 0, errors.New("invalid whence")
	}
	if abs < 0 {
		return 0, errors.New("negative position")
	}
	if _, err := b.rs.Seek(b.start+min(abs, b.n), io.SeekStart); err != nil {
		return 0, err
	}
	b.off = abs
	return abs, nil
}

// Put 實作 Store：先算 sha256（與 want 不符時不上傳），已存在時不重寫，上傳時附 Content-MD5 讓服務端檢查完整性。
func (s *S3) Put(ctx context.Context, r io.Reader, want string) (string, int64, error) {
	if want != "" && !ValidSHA(want) {
		return "", 0, fmt.Errorf("不是有效的 sha256：%q", want)
	}
	body, sha, md5sum, n, err := s.spool(r)
	if err != nil {
		return "", 0, err
	}
	if want != "" && sha != want {
		return "", 0, ErrHashMismatch
	}
	if ok, err := s.Exists(ctx, sha); err != nil {
		return "", 0, err
	} else if ok {
		return sha, n, nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(s.key(sha)), Body: body, ContentLength: aws.Int64(n),
		ContentMD5: aws.String(base64.StdEncoding.EncodeToString(md5sum)), ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		return "", 0, s.wrap("上傳", err)
	}
	return sha, n, nil
}

// Open 實作 Store。回傳的 reader 讀到結尾時檢查長度與 sha256，不符時回 ErrCorrupt（不會把截斷的內容當成完整的）；
// 逾時涵蓋整個下載，Close 時才釋放。
func (s *S3) Open(ctx context.Context, sha string) (io.ReadCloser, error) {
	if !ValidSHA(sha) {
		return nil, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key(sha))})
	if err != nil {
		cancel()
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, s.wrap("下載", err)
	}
	size := int64(-1)
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	return &verifyingReader{body: out.Body, cancel: cancel, want: sha, size: size, h: sha256.New(), s: s}, nil
}

type verifyingReader struct {
	body   io.ReadCloser
	cancel context.CancelFunc
	want   string
	size   int64
	n      int64
	h      hash.Hash
	s      *S3
	done   bool
}

func (v *verifyingReader) Read(p []byte) (int, error) {
	if v.done {
		return 0, io.EOF
	}
	k, err := v.body.Read(p)
	v.n += int64(k)
	v.h.Write(p[:k])
	switch {
	case err == io.EOF:
		v.done = true
		if (v.size >= 0 && v.n != v.size) || sumHex(v.h) != v.want {
			return k, ErrCorrupt
		}
		return k, io.EOF
	case err != nil:
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return k, fmt.Errorf("%w：%v", ErrCorrupt, err)
		}
		return k, v.s.wrap("下載", err)
	}
	return k, nil
}

func (v *verifyingReader) Close() error {
	err := v.body.Close()
	v.cancel()
	return err
}

// Exists 實作 Store。
func (s *S3) Exists(ctx context.Context, sha string) (bool, error) {
	if !ValidSHA(sha) {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key(sha))})
	if err == nil {
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, s.wrap("檢查", err)
}

// Delete 實作 Store；不存在時不算錯誤。
func (s *S3) Delete(ctx context.Context, sha string) error {
	if !ValidSHA(sha) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key(sha))})
	if err != nil && !isNotFound(err) {
		return s.wrap("刪除", err)
	}
	return nil
}

// Ping 確認 bucket 存在且憑證可以使用（notesteme-hub doctor）。
func (s *S3) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	if isNotFound(err) {
		return fmt.Errorf("S3 的 bucket %q 不存在", s.bucket)
	}
	return s.wrap("檢查 bucket", err)
}
