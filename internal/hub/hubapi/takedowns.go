package hubapi

import (
	"context"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
)

// 下架申請（M5-S5，HACC-R15）：不需要登入。
//
// - 不檢查申請的版本存不存在、申請人看不看得到（預設站台是 authenticated，版權人不一定有帳號）；version_id 只要是 UUID，
//   任何版本都回相同的 201，所以申請不能用來探測版本是否存在。版主決定 remove 時版本不存在才回 422。
// - 每個 IP 每小時最多 rate_limits.takedowns_per_hour 筆成功的申請，在驗證 body 之前先預留額度、失敗時歸還；
//   計數只在記憶體中（不保存任何 IP 衍生的資料，Hub 重新啟動後歸零）。
// - contact_email 以信封加密：主金鑰以 HKDF-SHA256 推導 KEK，每筆申請一把隨機的資料金鑰（XChaCha20-Poly1305），
//   兩層的 AAD 都綁定申請 ID；只有 moderate 在 GET /api/v1/admin/takedowns 看得到。

const (
	codeTakedownDecided = "takedown_decided"
	msgTakedownCreated  = "已收到下架申請，編號 %s。管理員處理後會在這裡更新狀態。"
	msgTakedownNotFound = "找不到這個下架申請。"
	minStatement        = 20
	maxStatement        = 5000
	maxEmail            = 254
	takedownWindow      = time.Hour
	takedownBodyLimit   = 64 << 10
)

var takedownReasons = []string{"copyright", "privacy", "other"}

// ---- 每小時的 IP 額度 ----

// hourlyLimiter 是滑動一小時的計數器：只記錄成功（commit）的請求時間。
type hourlyLimiter struct {
	clock clock.Clock
	mu    sync.Mutex
	hits  map[string][]time.Time
}

func newHourlyLimiter(clk clock.Clock) *hourlyLimiter {
	if clk == nil {
		clk = clock.Real{}
	}
	return &hourlyLimiter{clock: clk, hits: map[string][]time.Time{}}
}

// reserve 預留一筆額度；超過 limit 時回傳要等待的時間。成功時回傳 release，請求失敗時呼叫它歸還額度。
func (l *hourlyLimiter) reserve(key string, limit int) (release func(), wait time.Duration, ok bool) {
	noop := func() {}
	if limit <= 0 {
		return noop, 0, true
	}
	now := l.clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	// 清掉一小時以前的紀錄（申請很少，逐一檢查的成本可以接受）。
	for k, ts := range l.hits {
		i := 0
		for i < len(ts) && !ts[i].After(now.Add(-takedownWindow)) {
			i++
		}
		if i == len(ts) {
			delete(l.hits, k)
		} else if i > 0 {
			l.hits[k] = ts[i:]
		}
	}
	ts := l.hits[key]
	if len(ts) >= limit {
		return noop, max(ts[0].Add(takedownWindow).Sub(now), time.Second), false
	}
	l.hits[key] = append(ts, now)
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		cur := l.hits[key]
		if i := slices.Index(cur, now); i >= 0 {
			l.hits[key] = slices.Delete(slices.Clone(cur), i, i+1)
		}
	}, 0, true
}

// ---- 聯絡 email 的信封加密 ----

func takedownAAD(kind, id string) []byte { return []byte("notesteme-hub takedown " + kind + "/" + id) }

// errNoMasterKey 表示沒有主金鑰（notesteme-hub serve 會先擋下；hub.Open 不要求它，migrate 與 admin 用不到）。
var errNoMasterKey = errors.New("沒有主金鑰，無法加解密下架申請的聯絡 email")

func (rt *routes) contactKEK() ([]byte, error) {
	if rt.d.Config == nil || rt.d.Config.MasterKey == "" {
		return nil, errNoMasterKey
	}
	return hkdf.Key(sha256.New, []byte(rt.d.Config.MasterKey), nil, "notesteme-hub takedown contact v1", chacha20poly1305.KeySize)
}

// sealContact 以一把新的資料金鑰加密 email，再以 KEK 加密資料金鑰。
func (rt *routes) sealContact(t *hubstore.Takedown, email string) error {
	kek, err := rt.contactKEK()
	if err != nil {
		return err
	}
	dek := make([]byte, chacha20poly1305.KeySize)
	if _, err := rand.Read(dek); err != nil {
		return err
	}
	seal := func(key, plain, aad []byte) ([]byte, []byte, error) {
		a, err := chacha20poly1305.NewX(key)
		if err != nil {
			return nil, nil, err
		}
		nonce := make([]byte, a.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return nil, nil, err
		}
		return nonce, a.Seal(nil, nonce, plain, aad), nil
	}
	if t.DEKNonce, t.DEKSealed, err = seal(kek, dek, takedownAAD("dek", t.ID)); err != nil {
		return err
	}
	t.ContactNonce, t.ContactSealed, err = seal(dek, []byte(email), takedownAAD("contact", t.ID))
	return err
}

// openContact 解開 email；任何失敗（換了主金鑰、資料被竄改）都回錯誤，不帶內容。
func (rt *routes) openContact(t hubstore.Takedown) (string, error) {
	kek, err := rt.contactKEK()
	if err != nil {
		return "", err
	}
	open := func(key, nonce, sealed, aad []byte) ([]byte, error) {
		a, err := chacha20poly1305.NewX(key)
		if err != nil {
			return nil, err
		}
		if len(nonce) != a.NonceSize() {
			return nil, errors.New("nonce 長度不符")
		}
		return a.Open(nil, nonce, sealed, aad)
	}
	dek, err := open(kek, t.DEKNonce, t.DEKSealed, takedownAAD("dek", t.ID))
	if err != nil {
		return "", errors.New("無法解開下架申請的資料金鑰")
	}
	plain, err := open(dek, t.ContactNonce, t.ContactSealed, takedownAAD("contact", t.ID))
	if err != nil {
		return "", errors.New("無法解開下架申請的聯絡 email")
	}
	return string(plain), nil
}

// ---- 路由 ----

type createTakedownInput struct {
	Body struct {
		// 欄位的範圍不寫進 schema：由 handler 驗證，422 的 errors[].path 才會指到欄位。
		VersionID    string `json:"version_id" required:"false" doc:"要下架的版本 ID"`
		Reason       string `json:"reason" required:"false" doc:"copyright、privacy、other"`
		Statement    string `json:"statement" required:"false" doc:"說明，20～5,000 個字元"`
		ContactEmail string `json:"contact_email" required:"false" doc:"聯絡 email（加密存放，只有版主看得到）"`
		GoodFaith    *bool  `json:"good_faith" required:"false" doc:"善意聲明，必須是 true"`
	}
}

// TakedownCreatedView 是建立下架申請的結果。
type TakedownCreatedView struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message"`
}

type takedownCreatedOutput struct{ Body TakedownCreatedView }

// TakedownStateView 是不需要登入的狀態查詢（只有 {id, state}）。
type TakedownStateView struct {
	ID    string `json:"id"`
	State string `json:"state" doc:"open、removed、rejected"`
}

type takedownStateOutput struct{ Body TakedownStateView }

type takedownPath struct {
	ID string `path:"id" maxLength:"64" doc:"下架申請 ID"`
}

func registerTakedowns(api huma.API, rt *routes) {
	public := map[string]any{AccessExtension: string(AccessPublic)}
	huma.Register(api, huma.Operation{
		OperationID: "takedowns-create", Method: http.MethodPost, Path: "/api/v1/takedowns", DefaultStatus: http.StatusCreated,
		Summary: "提出下架申請，不需要登入", Description: "規則：HACC-R15。", Extensions: public, MaxBodyBytes: takedownBodyLimit,
		Errors: []int{http.StatusUnprocessableEntity, http.StatusTooManyRequests, http.StatusServiceUnavailable},
	}, rt.createTakedown)
	huma.Register(api, huma.Operation{
		OperationID: "takedowns-get", Method: http.MethodGet, Path: "/api/v1/takedowns/{id}",
		Summary: "查詢下架申請的狀態，不需要登入", Description: "規則：HACC-R15。", Extensions: public,
		Errors: []int{http.StatusNotFound, http.StatusTooManyRequests},
	}, rt.getTakedown)
}

// checkStatement 驗證說明：去掉頭尾空白後 20～5,000 個字元，除了換行與 tab 不能有控制字元。
func checkStatement(s string) bool {
	return utf8.RuneCountInString(s) >= minStatement && freeText(s, maxStatement)
}

// checkEmail 只接受單純的位址（沒有顯示名稱、沒有控制字元），最多 254 個字元。
func checkEmail(s string) bool {
	if s == "" || len(s) > maxEmail || strings.ContainsFunc(s, unicode.IsControl) {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Name == "" && a.Address == s
}

func validVersionID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil && len(s) == 36
}

func (rt *routes) createTakedown(ctx context.Context, in *createTakedownInput) (*takedownCreatedOutput, error) {
	release, wait, ok := rt.takedownLimiter.reserve(ipKey(remoteAddrFrom(ctx)), rt.rateLimits().TakedownsPerHour)
	if !ok {
		return nil, handlerRateLimited(wait)
	}
	out, err := rt.takedown(ctx, in)
	if err != nil {
		release()
		return nil, err
	}
	return out, nil
}

func (rt *routes) takedown(ctx context.Context, in *createTakedownInput) (*takedownCreatedOutput, error) {
	b := in.Body
	statement := strings.TrimSpace(b.Statement)
	email := strings.TrimSpace(b.ContactEmail)
	var f fieldErrs
	if !validVersionID(b.VersionID) {
		f.add("version_id", "必須是版本 ID。")
	}
	if !slices.Contains(takedownReasons, b.Reason) {
		f.add("reason", oneOf(takedownReasons))
	}
	if !checkStatement(statement) {
		f.add("statement", fmt.Sprintf("請以 %d～%d 個字元說明理由。", minStatement, maxStatement))
	}
	if !checkEmail(email) {
		f.add("contact_email", "必須是有效的 email。")
	}
	if b.GoodFaith == nil || !*b.GoodFaith {
		f.add("good_faith", "必須聲明這是基於善意的申請（true）。")
	}
	if err := f.err(); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	t := hubstore.Takedown{ID: id.String(), VersionID: strings.ToLower(b.VersionID), Reason: b.Reason, Statement: statement}
	if err := rt.sealContact(&t, email); err != nil {
		if errors.Is(err, errNoMasterKey) {
			rt.d.Log.ErrorContext(ctx, "沒有主金鑰，無法受理下架申請")
			return nil, apierr.New(http.StatusServiceUnavailable, "not_configured", "Hub 尚未設定主金鑰，暫時無法受理下架申請。")
		}
		return nil, rt.apiError(ctx, err)
	}
	t, err = rt.d.Store.CreateTakedown(ctx, t)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	return &takedownCreatedOutput{Body: TakedownCreatedView{ID: t.ID, State: t.State, Message: fmt.Sprintf(msgTakedownCreated, t.ID)}}, nil
}

func (rt *routes) getTakedown(ctx context.Context, in *takedownPath) (*takedownStateOutput, error) {
	t, err := rt.d.Store.Takedown(ctx, in.ID)
	if errors.Is(err, hubstore.ErrNotFound) {
		return nil, apierr.New(http.StatusNotFound, apierr.CodeNotFound, msgTakedownNotFound)
	}
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	return &takedownStateOutput{Body: TakedownStateView{ID: t.ID, State: t.State}}, nil
}
