package hubapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/bundle"
	"github.com/cxyfer/notesteme/internal/hub/hubauth"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubrepo"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 內容倉庫的路由（M5-S3）：分享（HREP-R1～R5、R8、R9、R11）、撤回（HREP-R10）與版本詳情（HREP-R6、R17 的詳情、HACC-R14）。
//
// POST /api/v1/shares 的本體是 multipart：Huma 只用來產生 OpenAPI 與套用 access middleware，
// 實際的處理在 rawOperations（access middleware 之後、Huma 讀取本體之前），自己控制上傳上限與 413 的錯誤代碼。
// 順序：分享次數 → Content-Length → 讀取 multipart（超過上限 413）→ attest → visibility → 每日上傳量 →
// bundle 驗證（BND-R4～R7 的結構）→ 能不能分享（HREP-R3）→ 來源紀錄的大小（R4）→ 內容格式（BND-R7）→
// 寫入 blob → 一個交易寫入 DB（removed_hashes 在交易中檢查；失敗時刪掉這次新寫的 blob）。

// shareFormOverhead 是 multipart 中 bundle 以外的部分（邊界、標頭、attest、visibility）的額度。
const shareFormOverhead = 64 << 10

// maxSmallField 是 attest、visibility 欄位的上限。
const maxSmallField = 1 << 10

// maxFormParts 是 multipart 最多讀取的 part 數。
const maxFormParts = 16

const (
	codeShareNotFound   = "not_found"
	codeVersionRemoved  = "removed"
	msgVersionNotFound  = "找不到這個版本。"
	msgShareNotFound    = "找不到這個分享。"
	msgVersionRemoved   = "這個版本已依下架申請移除。"
	msgShareRateLimited = "上傳太頻繁，請在 %d 秒後再試。"
)

// VideoRef 是影片的識別；沒有分P時 part 是 null。
type VideoRef struct {
	Platform   string `json:"platform"`
	ExternalID string `json:"external_id"`
	Part       *int   `json:"part"`
}

func videoRef(v hubstore.Video) VideoRef {
	r := VideoRef{Platform: v.Platform, ExternalID: v.ExternalID}
	if v.Part > 0 {
		p := v.Part
		r.Part = &p
	}
	return r
}

// SharedVersionView 是分享結果中的一個版本（HREP-R1）。
type SharedVersionView struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Variant      string `json:"variant"`
	Lang         string `json:"lang"`
	SHA256       string `json:"sha256"`
	Deduplicated bool   `json:"deduplicated"`
	Visibility   string `json:"visibility"`
}

// ShareView 是 POST /api/v1/shares 的 201 回應。
type ShareView struct {
	ShareID  string              `json:"share_id"`
	Video    VideoRef            `json:"video"`
	Versions []SharedVersionView `json:"versions" doc:"依 manifest 中產出的順序"`
}

type shareForm struct {
	Bundle     huma.FormFile `form:"bundle" contentType:"application/zip" required:"true" doc:".notesteme.zip（bundle v0）"`
	Attest     string        `form:"attest" required:"true" doc:"必須是 true：確認有權分享這些內容"`
	Visibility string        `form:"visibility" doc:"public、authenticated、private 或 group:<群組>；省略時依 content_defaults"`
}

type shareInput struct {
	RawBody huma.MultipartFormFiles[shareForm]
}

type shareOutput struct{ Body ShareView }

// UploaderView 是上傳者（只有顯示名稱，HREP-R17）。
type UploaderView struct {
	DisplayName string `json:"display_name"`
}

// RelatedView 是衍生關係另一端的版本（HREP-R6）。
type RelatedView struct {
	ID       string `json:"id"`
	Relation string `json:"relation" enum:"parent,regenerated_from"`
	Kind     string `json:"kind"`
	Variant  string `json:"variant"`
	Lang     string `json:"lang"`
	Uploader string `json:"uploader" doc:"第一位上傳者的顯示名稱"`
	State    string `json:"state"`
	Summary  string `json:"summary,omitempty" doc:"regenerated_from 時是「以 @<上傳者> 的原始字幕重新生成」"`
}

// VersionView 是版本詳情（HREP-R16 的欄位加上來源紀錄、衍生關係、可見度與狀態，HREP-R17）。
type VersionView struct {
	ID              string         `json:"id"`
	Video           VideoRef       `json:"video"`
	Kind            string         `json:"kind"`
	Variant         string         `json:"variant"`
	Lang            string         `json:"lang"`
	TranscribeModel string         `json:"transcribe_model"`
	CorrectModel    string         `json:"correct_model"`
	TranslateModel  string         `json:"translate_model"`
	ModelVerified   bool           `json:"model_verified" doc:"true 只代表由 Client 的 api executor 記錄，Hub 無法驗證模型"`
	Badges          []string       `json:"badges,omitempty"`
	License         string         `json:"license"`
	Score           int            `json:"score"`
	Uploaders       []UploaderView `json:"uploaders"`
	CreatedAt       time.Time      `json:"created_at"`
	SHA256          string         `json:"sha256"`
	Provenance      map[string]any `json:"provenance" doc:"白名單中的來源紀錄欄位（ADR-0032 第 2 點）"`
	Parents         []RelatedView  `json:"parents"`
	Children        []RelatedView  `json:"children"`
	Visibility      string         `json:"visibility"`
	State           string         `json:"state"`
}

type versionInput struct {
	ID string `path:"id" doc:"版本 ID"`
}

type versionOutput struct{ Body VersionView }

type withdrawInput struct {
	ID string `path:"id" doc:"分享 ID"`
}

// registerRepository 註冊 M5-S3 實作的路由（Access 同權限矩陣，HREP 規格「介面」）。
func registerRepository(api huma.API, rt *routes) {
	reg := func(op huma.Operation, access Access, spec string) huma.Operation {
		op.Description = "規則：" + spec + "。"
		op.Extensions = map[string]any{AccessExtension: string(access)}
		return op
	}
	authErrs := []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests}
	huma.Register(api, reg(huma.Operation{
		OperationID: "shares-create", Method: http.MethodPost, Path: "/api/v1/shares", DefaultStatus: http.StatusCreated,
		Summary: "分享 bundle（multipart：bundle、attest、visibility）",
		Errors:  append(authErrs, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity),
	}, perm(hubconfig.PermUpload), "HREP-R1～R5、R8、R9、R11"), func(context.Context, *shareInput) (*shareOutput, error) {
		// 不會執行到：rawOperations 在 Huma 讀取本體之前處理這條路由。
		return nil, apierr.NotImplemented()
	})
	huma.Register(api, reg(huma.Operation{
		OperationID: "shares-withdraw", Method: http.MethodDelete, Path: "/api/v1/shares/{id}", DefaultStatus: http.StatusNoContent,
		Summary: "撤回自己的分享", Errors: append(authErrs, http.StatusNotFound),
	}, perm(hubconfig.PermUpload), "HREP-R10"), rt.withdrawShare)
	huma.Register(api, reg(huma.Operation{
		OperationID: "versions-get", Method: http.MethodGet, Path: "/api/v1/versions/{id}",
		Summary: "版本詳情、來源紀錄與衍生關係", Errors: append(authErrs, http.StatusNotFound, http.StatusGone),
	}, perm(hubconfig.PermRead), "HREP-R6、R17、HACC-R14"), rt.getVersion)
}

// rawOperations 讓需要自己讀取本體的路由在 access middleware 之後接手（不呼叫 next，Huma 不會讀取本體）。
func (rt *routes) rawOperations(ctx huma.Context, next func(huma.Context)) {
	if op := ctx.Operation(); op != nil && op.OperationID == "shares-create" {
		r, w := humago.Unwrap(ctx)
		rt.createShare(w, r.WithContext(ctx.Context()))
		return
	}
	next(ctx)
}

// ---- 分享 ----

type rawShareForm struct {
	bundle        []byte
	hasBundle     bool
	attest        string
	visibility    string
	hasVisibility bool
}

func badForm() *apierr.Error {
	return apierr.New(http.StatusBadRequest, apierr.CodeInvalidInput, "請求的格式不正確：要用 multipart/form-data 送出 bundle、attest 與 visibility。")
}

// readShareForm 讀取 multipart；bundle 超過 maxUpload 時回 413。
func readShareForm(r *http.Request, maxUpload int64) (rawShareForm, *apierr.Error) {
	var f rawShareForm
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		return f, badForm()
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return f, badForm()
	}
	tooLarge := func(err error) bool {
		var mbe *http.MaxBytesError
		return errors.As(err, &mbe)
	}
	for i := 0; ; i++ {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if tooLarge(err) {
				return f, hubrepo.ErrTooLarge(maxUpload)
			}
			return f, badForm()
		}
		if i >= maxFormParts {
			return f, badForm()
		}
		limit := int64(maxSmallField)
		if part.FormName() == "bundle" {
			limit = maxUpload
		}
		d, err := io.ReadAll(io.LimitReader(part, limit+1))
		if err != nil {
			if tooLarge(err) {
				return f, hubrepo.ErrTooLarge(maxUpload)
			}
			return f, badForm()
		}
		if int64(len(d)) > limit {
			if part.FormName() == "bundle" {
				return f, hubrepo.ErrTooLarge(maxUpload)
			}
			return f, badForm()
		}
		switch part.FormName() {
		case "bundle":
			f.bundle, f.hasBundle = d, true
		case "attest":
			f.attest = string(d)
		case "visibility":
			f.visibility, f.hasVisibility = string(d), true
		}
	}
	return f, nil
}

func shareRateLimited(wait time.Duration) *apierr.Error {
	secs := max(int(math.Ceil(wait.Seconds())), 1)
	e := apierr.New(http.StatusTooManyRequests, CodeRateLimited, fmt.Sprintf(msgShareRateLimited, secs))
	e.RetryAfterS = &secs
	return e
}

// checkShareLimits 檢查分享的額度（HREP-R11）：每小時的次數，以及加上 size 之後的每日位元組數。
// 每小時的次數是 DB 中成功的分享（含撤回的，Hub 重新啟動後不會歸零）加上記憶體中失敗的分享（uploadguard.go）。
func (rt *routes) checkShareLimits(ctx context.Context, userID string, size int64) error {
	rl := rt.rateLimits()
	now := rt.d.Clock.Now()
	if rl.SharesPerHour > 0 && size == 0 {
		u, err := rt.d.Store.ShareUsage(ctx, userID, now.Add(-time.Hour))
		if err != nil {
			return err
		}
		n, oldest := rt.uploads.recentFailures(userID)
		if u.Count+n >= rl.SharesPerHour {
			if u.Count > 0 && (n == 0 || u.Oldest.Before(oldest)) {
				oldest = u.Oldest
			}
			return shareRateLimited(oldest.Add(time.Hour).Sub(now))
		}
	}
	if rl.UploadBytesPerDay > 0 && size > 0 {
		u, err := rt.d.Store.ShareUsage(ctx, userID, now.Add(-24*time.Hour))
		if err != nil {
			return err
		}
		if u.Bytes+size > rl.UploadBytesPerDay {
			wait := time.Hour
			if u.Count > 0 {
				wait = u.Oldest.Add(24 * time.Hour).Sub(now)
			}
			return shareRateLimited(wait)
		}
	}
	return nil
}

func (rt *routes) createShare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	if p == nil {
		writeRawErr(w, hubauth.ErrUnauthorized())
		return
	}
	if rt.d.Store == nil || rt.d.Blobs == nil || rt.d.Config == nil {
		rt.d.Log.ErrorContext(ctx, "Hub 沒有設定儲存，無法分享")
		writeRawErr(w, apierr.As(nil))
		return
	}
	out, err := rt.share(ctx, w, r, p)
	if err != nil {
		e := rt.apiError(ctx, err)
		if countsAsFailure(e) {
			rt.uploads.recordFailure(p.User.ID)
		}
		writeRawErr(w, e)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(out)
}

func (rt *routes) share(ctx context.Context, w http.ResponseWriter, r *http.Request, p *hubauth.Principal) (*ShareView, error) {
	cfg := rt.d.Config
	if err := rt.checkShareLimits(ctx, p.User.ID, 0); err != nil {
		return nil, err
	}
	// 全域的並行名額在讀取本體之前取得（uploadguard.go）。
	release, ok := rt.uploads.acquire(p.User.ID)
	if !ok {
		return nil, errShareBusy()
	}
	defer release()
	// 讀取本體的期限（連線層的期限，用真實時間而不是注入的 clock）；不支援時（測試的 ResponseRecorder）略過。
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(shareReadTimeout))
	maxUpload := cfg.Bundles.MaxUploadBytes
	if r.ContentLength > maxUpload+shareFormOverhead {
		return nil, hubrepo.ErrTooLarge(maxUpload)
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+shareFormOverhead)
	form, e := readShareForm(r, maxUpload)
	if e != nil {
		return nil, e
	}
	if form.attest != "true" {
		return nil, hubrepo.ErrAttestRequired()
	}
	if form.hasVisibility {
		exists := func(g string) bool { _, ok := cfg.Groups[g]; return ok }
		if e := hubrepo.CheckRequested(form.visibility, cfg.SiteVisibility, exists); e != nil {
			return nil, e
		}
	}
	if !form.hasBundle || len(form.bundle) == 0 {
		e := apierr.New(http.StatusUnprocessableEntity, apierr.CodeInvalidInput, "bundle：請附上 bundle 檔案。")
		e.Errors = append(e.Errors, validate.FieldError{Path: "bundle", Message: "請附上 bundle 檔案。"})
		return nil, e
	}
	if err := rt.checkShareLimits(ctx, p.User.ID, int64(len(form.bundle))); err != nil {
		return nil, err
	}
	b, err := bundle.Read(form.bundle, bundle.Options{
		Limits: bundle.Limits{MaxFiles: cfg.Bundles.MaxFiles, MaxFileBytes: cfg.Bundles.MaxFileBytes,
			MaxTotalBytes: cfg.Bundles.MaxTotalBytes, MaxRatio: cfg.Bundles.MaxRatio},
		InstanceIDOptional: true,
	})
	if err != nil {
		var be *bundle.Error
		if errors.As(err, &be) {
			return nil, hubrepo.FromBundleError(be)
		}
		return nil, err
	}
	arts := b.Manifest.Artifacts
	provs := make([]hubrepo.Provenance, len(arts))
	filtered := make([][]byte, len(arts))
	for i, a := range arts {
		filtered[i], provs[i] = hubrepo.FilterProvenance(a.Provenance)
	}
	if e := hubrepo.CheckShareable(b, provs, cfg.Licenses); e != nil {
		return nil, e
	}
	if e := hubrepo.CheckProvenanceSize(b); e != nil {
		return nil, e
	}
	if err := b.ValidateContentsContext(ctx); err != nil {
		var be *bundle.Error
		if errors.As(err, &be) {
			return nil, hubrepo.FromBundleError(be)
		}
		return nil, err
	}
	in, err := rt.buildShare(ctx, p, b, provs, filtered, form)
	if err != nil {
		return nil, err
	}
	written, err := rt.writeBlobs(ctx, b, in.Blobs)
	if err != nil {
		return nil, err
	}
	res, err := rt.d.Store.CreateShare(ctx, in)
	if err != nil {
		rt.cleanupBlobs(ctx, written)
		if errors.Is(err, hubstore.ErrRemovedContent) {
			return nil, hubrepo.ErrRemovedContent()
		}
		return nil, err
	}
	if res.Existing {
		rt.cleanupBlobs(ctx, written)
	}
	view := &ShareView{ShareID: res.ShareID, Video: videoRef(res.Video), Versions: make([]SharedVersionView, 0, len(res.Versions))}
	for _, v := range res.Versions {
		view.Versions = append(view.Versions, SharedVersionView{ID: v.ID, Kind: v.Kind, Variant: v.Variant, Lang: v.Lang,
			SHA256: v.SHA256, Deduplicated: v.Deduplicated, Visibility: v.Visibility})
	}
	return view, nil
}

func sha256Hex(d []byte) string {
	sum := sha256.Sum256(d)
	return hex.EncodeToString(sum[:])
}

// buildShare 依 bundle 組出要寫入的分享：每個產出一個版本（模型欄位、model_verified、可見度、衍生關係）與 blob。
func (rt *routes) buildShare(ctx context.Context, p *hubauth.Principal, b *bundle.Bundle, provs []hubrepo.Provenance,
	filtered [][]byte, form rawShareForm) (hubstore.NewShare, error) {
	cfg := rt.d.Config
	it := b.Manifest.Item
	in := hubstore.NewShare{
		UserID: p.User.ID, License: b.Manifest.License, BundleSHA256: sha256Hex(form.bundle), BundleSize: int64(len(form.bundle)),
		Video: hubstore.Video{Platform: it.Platform, ExternalID: it.ExternalID, Part: it.Part, Title: it.Title,
			ChannelExternalID: it.Channel.ExternalID, ChannelTitle: it.Channel.Title, DurationS: it.DurationS,
			OriginalLang: it.OriginalLang, PublishedAt: it.PublishedAt},
	}
	// 同一部影片在 Hub 上已有的版本（parents_sha256 指到它們時沿用模型欄位）。
	known := map[string]hubrepo.Models{}
	if v, err := rt.d.Store.VideoByKey(ctx, it.Platform, it.ExternalID, it.Part); err == nil {
		vs, err := rt.d.Store.VideoVersions(ctx, v.ID)
		if err != nil {
			return in, err
		}
		for _, x := range vs {
			if _, ok := known[x.SHA256]; !ok {
				known[x.SHA256] = hubrepo.Models{Transcribe: x.TranscribeModel, Correct: x.CorrectModel, Translate: x.TranslateModel, Verified: x.ModelVerified}
			}
		}
	} else if !errors.Is(err, hubstore.ErrNotFound) {
		return in, err
	}
	viewer, err := rt.viewer(ctx, p)
	if err != nil {
		return in, err
	}
	// hub_origin 只在版本存在、上傳者看得到時採用（看不到的版本不能藉衍生關係確認存在）。
	origins := map[string]*hubstore.Version{}
	origin := func(id string) *hubstore.Version {
		if v, ok := origins[id]; ok {
			return v
		}
		var out *hubstore.Version
		if id != "" {
			if v, err := rt.d.Store.Version(ctx, id); err == nil {
				if ok, _ := rt.canSee(ctx, v, viewer); ok && v.State != hubrepo.StateRemoved {
					out = &v
				}
			}
		}
		origins[id] = out
		return out
	}
	nodes := make([]hubrepo.Node, len(b.Manifest.Artifacts))
	for i, a := range b.Manifest.Artifacts {
		d, _ := b.File(a.Path)
		nodes[i] = hubrepo.Node{Kind: a.Kind, Variant: a.Variant, SHA256: sha256Hex(d), Prov: provs[i]}
	}
	models := hubrepo.ComputeModels(nodes, func(sha string) (hubrepo.Models, bool) { m, ok := known[sha]; return m, ok },
		func(id string) bool { v := origin(id); return v != nil && v.ModelVerified })
	blobs := map[string]bool{}
	addBlob := func(path string) string {
		d, _ := b.File(path)
		sha := sha256Hex(d)
		if !blobs[sha] {
			blobs[sha] = true
			in.Blobs = append(in.Blobs, hubstore.Blob{SHA256: sha, Size: int64(len(d)), MediaType: mediaType(path)})
		}
		return sha
	}
	for i, a := range b.Manifest.Artifacts {
		d, _ := b.File(a.Path)
		vis := cfg.DefaultVisibility(a.Kind)
		if form.hasVisibility && a.Kind != "frames" {
			vis = form.visibility
		}
		nv := hubstore.NewVersion{
			Version: hubstore.Version{Kind: a.Kind, Variant: a.Variant, Lang: a.Lang, Format: a.Format,
				SHA256: addBlob(a.Path), Size: int64(len(d)), Provenance: filtered[i],
				TranscribeModel: models[i].Transcribe, CorrectModel: models[i].Correct, TranslateModel: models[i].Translate,
				ModelVerified: models[i].Verified, Visibility: vis, License: b.Manifest.License, State: hubrepo.StateVisible},
			ParentsSHA256: provs[i].ParentsSHA256,
		}
		if v := origin(provs[i].HubOrigin.VersionID); v != nil {
			nv.RegeneratedFrom = v.ID
		}
		for _, img := range hubrepo.ImagesOf(b, a) {
			nv.Images = append(nv.Images, addBlob(img))
		}
		in.Versions = append(in.Versions, nv)
	}
	return in, nil
}

func mediaType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "application/json"
	case ".md":
		return "text/markdown"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	}
	return "application/octet-stream"
}

// writeBlobs 寫入 blob，回傳這次新寫入（原本不存在）的 sha256，失敗時由呼叫端刪除。
func (rt *routes) writeBlobs(ctx context.Context, b *bundle.Bundle, blobs []hubstore.Blob) ([]string, error) {
	contents := map[string][]byte{}
	for _, p := range b.Paths() {
		d, _ := b.File(p)
		contents[sha256Hex(d)] = d
	}
	var written []string
	for _, x := range blobs {
		ok, err := rt.d.Blobs.Exists(ctx, x.SHA256)
		if err != nil {
			rt.cleanupBlobs(ctx, written)
			return nil, err
		}
		if ok {
			continue
		}
		if _, _, err := rt.d.Blobs.Put(ctx, bytes.NewReader(contents[x.SHA256]), x.SHA256); err != nil {
			rt.cleanupBlobs(ctx, written)
			return nil, err
		}
		written = append(written, x.SHA256)
	}
	return written, nil
}

// cleanupBlobs 刪掉這次寫入、但沒有任何版本引用的 blob（分享失敗時什麼都不留下，HREP-R2、R9）。
func (rt *routes) cleanupBlobs(ctx context.Context, shas []string) {
	for _, sha := range shas {
		if n, err := rt.d.Store.BlobRefs(ctx, sha); err == nil && n == 0 {
			if err := rt.d.Blobs.Delete(ctx, sha); err != nil {
				rt.d.Log.WarnContext(ctx, "刪除未使用的 blob 失敗", "sha256", sha, "error", err)
			}
		}
	}
}

// ---- 撤回 ----

func (rt *routes) withdrawShare(ctx context.Context, in *withdrawInput) (*struct{}, error) {
	p := principalFrom(ctx)
	if p == nil {
		return nil, hubauth.ErrUnauthorized()
	}
	if err := rt.d.Store.WithdrawShare(ctx, p.User.ID, in.ID); err != nil {
		if errors.Is(err, hubstore.ErrNotFound) {
			return nil, apierr.New(http.StatusNotFound, codeShareNotFound, msgShareNotFound)
		}
		return nil, rt.apiError(ctx, err)
	}
	return nil, nil
}

// ---- 可見度與版本詳情 ----

// viewer 依身分組出可見度判斷用的 Viewer（群組取帳號當下的群組；moderate 依有效權限，PAT 只有它帶的權限）。
func (rt *routes) viewer(ctx context.Context, p *hubauth.Principal) (hubrepo.Viewer, error) {
	if p == nil || rt.d.Auth == nil {
		return hubrepo.Viewer{Anonymous: true}, nil
	}
	groups, err := rt.d.Auth.Groups(ctx, p.User.ID)
	if err != nil {
		return hubrepo.Viewer{}, err
	}
	mod, err := rt.d.Auth.HasPermission(ctx, p, hubconfig.PermModerate)
	if err != nil {
		return hubrepo.Viewer{}, err
	}
	return hubrepo.Viewer{UserID: p.User.ID, Groups: groups, Moderator: mod}, nil
}

// canSee 回報 viewer 看不看得到版本；同時回傳上傳者（之後組回應時重用）。
func (rt *routes) canSee(ctx context.Context, v hubstore.Version, viewer hubrepo.Viewer) (bool, []hubstore.Uploader) {
	ups, err := rt.d.Store.VersionUploaders(ctx, v.ID)
	if err != nil {
		rt.d.Log.ErrorContext(ctx, "讀取版本的上傳者失敗", "error", err)
		return false, nil
	}
	ids := make([]string, 0, len(ups))
	for _, u := range ups {
		ids = append(ids, u.UserID)
	}
	return hubrepo.CanSee(hubrepo.Subject{Visibility: v.Visibility, State: v.State, Uploaders: ids}, viewer, rt.d.Config.SiteVisibility), ups
}

func versionNotFound() *apierr.Error {
	return apierr.New(http.StatusNotFound, apierr.CodeNotFound, msgVersionNotFound)
}

func (rt *routes) getVersion(ctx context.Context, in *versionInput) (*versionOutput, error) {
	viewer, err := rt.viewer(ctx, principalFrom(ctx))
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	v, err := rt.d.Store.Version(ctx, in.ID)
	if errors.Is(err, hubstore.ErrNotFound) {
		return nil, versionNotFound()
	}
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	ok, ups := rt.canSee(ctx, v, viewer)
	if !ok {
		return nil, versionNotFound()
	}
	if v.State == hubrepo.StateRemoved {
		return nil, apierr.New(http.StatusGone, codeVersionRemoved, msgVersionRemoved)
	}
	video, err := rt.d.Store.VideoByID(ctx, v.VideoID)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	out := &versionOutput{}
	view := VersionView{
		ID: v.ID, Video: videoRef(video), Kind: v.Kind, Variant: v.Variant, Lang: v.Lang,
		TranscribeModel: v.TranscribeModel, CorrectModel: v.CorrectModel, TranslateModel: v.TranslateModel,
		ModelVerified: v.ModelVerified, License: v.License, Score: v.Score, CreatedAt: v.CreatedAt, SHA256: v.SHA256,
		Visibility: v.Visibility, State: v.State, Uploaders: make([]UploaderView, 0, len(ups)),
		Provenance: map[string]any{}, Parents: []RelatedView{}, Children: []RelatedView{},
	}
	if !v.ModelVerified {
		view.Badges = []string{"unverified_model"}
	}
	for _, u := range ups {
		view.Uploaders = append(view.Uploaders, UploaderView{DisplayName: u.DisplayName})
	}
	_ = json.Unmarshal(v.Provenance, &view.Provenance)
	parents, children, err := rt.d.Store.VersionDerivations(ctx, v.ID)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	view.Parents = rt.related(ctx, parents, viewer)
	view.Children = rt.related(ctx, children, viewer)
	out.Body = view
	return out, nil
}

// related 只列出 viewer 看得到的版本（removed 的 parent 仍列出、state: removed，HREP-R6）。
func (rt *routes) related(ctx context.Context, ds []hubstore.Derived, viewer hubrepo.Viewer) []RelatedView {
	out := []RelatedView{}
	for _, d := range ds {
		ok, ups := rt.canSee(ctx, d.Version, viewer)
		if !ok {
			continue
		}
		r := RelatedView{ID: d.ID, Relation: d.Relation, Kind: d.Kind, Variant: d.Variant, Lang: d.Lang, State: d.State}
		if len(ups) > 0 {
			r.Uploader = ups[0].DisplayName
		}
		if d.Relation == hubstore.RelationRegeneratedFrom && r.Uploader != "" {
			r.Summary = "以 @" + r.Uploader + " 的原始字幕重新生成"
		}
		out = append(out, r)
	}
	return out
}
