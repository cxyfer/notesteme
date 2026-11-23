package hubapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubrepo"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 列表、影片頁與 ASR 前查詢（M5-S4，HREP-R16、R17、R19）。
//
// 可見度分兩層：SQL 先依 hubrepo.Listable（與上傳者的 EXISTS）預先過濾，分頁才不會被看不到的版本佔掉；
// 取出後每一筆再經過 routes.canSee（與版本詳情同一個判斷）。列表、影片頁與查詢只列 state 為 visible 的版本
// （hidden 即使上傳者與 moderate 看得到也不列，HREP-R15、R16）。
//
// query 參數一律宣告成字串、在 handler 內驗證：Huma 自己的驗證錯誤沒有 errors[].path（apierr 的全域設定），
// 而規格要求 422 的 errors[].path 指到參數（例如 query.limit）。

// 列表的上限與預設（HREP-R16）。
const (
	defaultListLimit = 50
	maxListLimit     = 200
	// maxParamRunes 是文字參數（頻道、影片 ID、模型）的長度上限。
	maxParamRunes = 256
	// maxCursorBytes 是 cursor 的長度上限。
	maxCursorBytes = 512
)

// 平台、種類與變體的合法值；變體與種類的順序也是影片頁的分組順序（HREP-R17）。
var (
	listPlatforms = []string{"youtube", "bilibili", "podcast"}
	listKinds     = []string{"transcript", "chapters", "note", "frames"}
	listVariants  = []string{"source", "normalized", "segmented", "corrected", "translated", "edited"}
)

var (
	langPattern   = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8}){0,4}$`)
	partSuffix    = regexp.MustCompile(`^(.+)/p([1-9][0-9]{0,3})$`)
	prefixPattern = regexp.MustCompile(`^[0-9a-f]{4}$`)
)

// VersionItem 是列表中的一個版本（HREP-R16 的欄位）。
type VersionItem struct {
	ID              string         `json:"id"`
	Video           VideoRef       `json:"video"`
	Kind            string         `json:"kind"`
	Variant         string         `json:"variant"`
	Lang            string         `json:"lang"`
	TranscribeModel string         `json:"transcribe_model"`
	CorrectModel    string         `json:"correct_model"`
	TranslateModel  string         `json:"translate_model"`
	ModelVerified   bool           `json:"model_verified" doc:"true 只代表由 Client 的 api executor 記錄，Hub 無法驗證模型"`
	Badges          []string       `json:"badges,omitempty" doc:"model_verified 為 false 時有 unverified_model"`
	License         string         `json:"license"`
	Score           int            `json:"score"`
	Uploaders       []UploaderView `json:"uploaders"`
	CreatedAt       time.Time      `json:"created_at"`
	SHA256          string         `json:"sha256"`
}

// VersionListView 是 GET /api/v1/versions 的回應；還有下一頁時有 next_cursor。
type VersionListView struct {
	Versions   []VersionItem `json:"versions"`
	NextCursor string        `json:"next_cursor,omitempty" doc:"下一頁的 cursor；沒有下一頁時省略"`
}

type listVersionsInput struct {
	Platform          string `query:"platform" doc:"youtube、bilibili 或 podcast"`
	Channel           string `query:"channel" doc:"頻道的 external_id"`
	Video             string `query:"video" doc:"<platform>:<external_id>，Bilibili 的分P加 /p<n>"`
	Lang              string `query:"lang" doc:"語言（BCP 47）"`
	Kind              string `query:"kind" doc:"transcript、chapters、note 或 frames"`
	Variant           string `query:"variant" doc:"source、normalized、segmented、corrected、translated 或 edited"`
	TranscribeModel   string `query:"transcribe_model"`
	CorrectModel      string `query:"correct_model"`
	TranslateModel    string `query:"translate_model"`
	ExcludeUnverified string `query:"exclude_unverified" doc:"true 時排除 model_verified 為 false 的版本"`
	Sort              string `query:"sort" doc:"score（預設）或 new"`
	Limit             string `query:"limit" doc:"1～200，預設 50"`
	Cursor            string `query:"cursor" doc:"上一頁回應的 next_cursor"`
}

type listVersionsOutput struct{ Body VersionListView }

// VideoView 是影片頁的影片（標題與頻道是上傳的文字，只當作文字顯示）。
type VideoView struct {
	Platform          string `json:"platform"`
	ExternalID        string `json:"external_id"`
	Part              *int   `json:"part"`
	Title             string `json:"title"`
	ChannelExternalID string `json:"channel_external_id"`
	ChannelTitle      string `json:"channel_title"`
}

// VersionGroup 是影片頁的一組版本（同 kind、variant、lang），組內依分數由高到低。
type VersionGroup struct {
	Kind     string        `json:"kind"`
	Variant  string        `json:"variant"`
	Lang     string        `json:"lang"`
	Versions []VersionItem `json:"versions"`
}

// VideoPageView 是 GET /api/v1/videos/<platform>/<external_id> 的回應（HREP-R17）。
type VideoPageView struct {
	Video  VideoView      `json:"video"`
	Groups []VersionGroup `json:"groups"`
}

type videoPageInput struct {
	Platform   string `path:"platform" doc:"youtube、bilibili 或 podcast"`
	ExternalID string `path:"external_id"`
	Part       string `query:"part" doc:"Bilibili 的分P（1 起算）；省略時是沒有分P的影片"`
}

type videoPageOutput struct{ Body VideoPageView }

// LookupVersionView 是 ASR 前查詢的一個版本（HREP-R19）。
type LookupVersionView struct {
	ID            string `json:"id"`
	Variant       string `json:"variant"`
	Lang          string `json:"lang"`
	SHA256        string `json:"sha256"`
	Score         int    `json:"score"`
	ModelVerified bool   `json:"model_verified"`
}

// LookupVideoView 是 ASR 前查詢的一部影片：只有完整的影片雜湊，不含 external_id 與標題。
type LookupVideoView struct {
	VideoKeySHA256 string              `json:"video_key_sha256"`
	Versions       []LookupVersionView `json:"versions"`
}

// LookupView 是 GET /api/v1/lookup 的回應。
type LookupView struct {
	Videos []LookupVideoView `json:"videos"`
}

type lookupInput struct {
	Platform string `query:"platform" doc:"youtube、bilibili 或 podcast"`
	Prefix   string `query:"prefix" doc:"sha256(<platform>:<external_id>:<part>) 的前 4 個小寫十六進位字元"`
}

type lookupOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         LookupView
}

func registerListing(api huma.API, rt *routes) {
	reg := func(op huma.Operation, access Access, spec string) huma.Operation {
		op.Description = "規則：" + spec + "。"
		op.Extensions = map[string]any{AccessExtension: string(access)}
		return op
	}
	authErrs := []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests}
	huma.Register(api, reg(huma.Operation{
		OperationID: "versions-list", Method: http.MethodGet, Path: "/api/v1/versions",
		Summary: "篩選版本（只列看得到的 visible 版本）", Errors: append(authErrs, http.StatusUnprocessableEntity),
	}, perm(hubconfig.PermRead), "HREP-R7、R16"), rt.listVersions)
	huma.Register(api, reg(huma.Operation{
		OperationID: "videos-get", Method: http.MethodGet, Path: "/api/v1/videos/{platform}/{external_id}",
		Summary: "影片的所有版本，依種類、變體與語言分組", Errors: append(authErrs, http.StatusNotFound, http.StatusUnprocessableEntity),
	}, perm(hubconfig.PermRead), "HREP-R17"), rt.videoPage)
	huma.Register(api, reg(huma.Operation{
		OperationID: "lookup", Method: http.MethodGet, Path: "/api/v1/lookup",
		Summary: "ASR 前查詢：以影片雜湊的前 4 碼查詢原始字幕", Errors: append(authErrs, http.StatusUnprocessableEntity),
	}, perm(hubconfig.PermRegenerate), "HREP-R19"), rt.lookup)
	registerDownload(api, rt, reg, authErrs)
}

// ---- 參數驗證 ----

type fieldErrs []validate.FieldError

func (f *fieldErrs) add(path, msg string) { *f = append(*f, validate.FieldError{Path: path, Message: msg}) }

// err 把逐欄位錯誤轉成 422（訊息是第一個錯誤，同 hubrepo 的欄位錯誤）。
func (f fieldErrs) err() error {
	if len(f) == 0 {
		return nil
	}
	e := apierr.New(http.StatusUnprocessableEntity, apierr.CodeInvalidInput, f[0].Path+"："+f[0].Message)
	e.Errors = append([]validate.FieldError{}, f...)
	return e
}

// plainText 回報文字參數是否合法：UTF-8、沒有控制字元、不超過 maxParamRunes。
func plainText(s string) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxParamRunes {
		return false
	}
	return !strings.ContainsFunc(s, unicode.IsControl)
}

func oneOf(vals []string) string { return "必須是 " + strings.Join(vals, "、") + " 之一。" }

func (f *fieldErrs) enum(path, v string, vals []string) {
	if v != "" && !slices.Contains(vals, v) {
		f.add(path, oneOf(vals))
	}
}

func (f *fieldErrs) text(path, v string) {
	if v != "" && !plainText(v) {
		f.add(path, "不能有控制字元，也不能超過 256 個字元。")
	}
}

// parseVideoParam 解析 video 參數：<platform>:<external_id>，分P加 /p<n>。
func parseVideoParam(v string) (*hubstore.VideoKey, bool) {
	platform, rest, ok := strings.Cut(v, ":")
	if !ok || !slices.Contains(listPlatforms, platform) || rest == "" || !plainText(rest) {
		return nil, false
	}
	k := &hubstore.VideoKey{Platform: platform, ExternalID: rest}
	if m := partSuffix.FindStringSubmatch(rest); m != nil {
		k.ExternalID = m[1]
		k.Part, _ = strconv.Atoi(m[2])
	}
	return k, true
}

// listCursor 是 next_cursor 的內容（base64url 的 JSON）：綁定排序方式，竄改只會換到別的位置，
// 不會繞過可見度（SQL 與 canSee 每一頁都重新過濾）。
type listCursor struct {
	Sort  string `json:"o"`
	Score int    `json:"s"`
	At    int64  `json:"t"`
	ID    string `json:"i"`
}

func encodeCursor(sort string, v hubstore.ListedVersion) string {
	b, _ := json.Marshal(listCursor{Sort: sort, Score: v.SortScore, At: v.CreatedAt.UnixMilli(), ID: v.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s, sort string) (*hubstore.VersionCursor, bool) {
	if len(s) > maxCursorBytes {
		return nil, false
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var c listCursor
	if err := dec.Decode(&c); err != nil || dec.More() {
		return nil, false
	}
	if c.Sort != sort || c.ID == "" || len(c.ID) > 64 || c.At < 0 || (sort == "new" && c.Score != 0) {
		return nil, false
	}
	return &hubstore.VersionCursor{Score: c.Score, CreatedAt: time.UnixMilli(c.At).UTC(), ID: c.ID}, true
}

// parseList 驗證 GET /api/v1/versions 的參數，錯誤依參數順序列出。
func parseList(in *listVersionsInput) (hubstore.VersionFilter, int, string, error) {
	var errs fieldErrs
	f := hubstore.VersionFilter{Platform: in.Platform, Channel: in.Channel, Lang: in.Lang, Kind: in.Kind, Variant: in.Variant,
		TranscribeModel: in.TranscribeModel, CorrectModel: in.CorrectModel, TranslateModel: in.TranslateModel}
	errs.enum("query.platform", in.Platform, listPlatforms)
	errs.text("query.channel", in.Channel)
	if in.Video != "" {
		k, ok := parseVideoParam(in.Video)
		if !ok {
			errs.add("query.video", "必須是 <platform>:<external_id>，Bilibili 的分P加 /p<n>。")
		}
		f.Video = k
	}
	if in.Lang != "" && (len(in.Lang) > 35 || !langPattern.MatchString(in.Lang)) {
		errs.add("query.lang", "必須是語言代碼，例如 en、zh-Hant。")
	}
	errs.enum("query.kind", in.Kind, listKinds)
	errs.enum("query.variant", in.Variant, listVariants)
	errs.text("query.transcribe_model", in.TranscribeModel)
	errs.text("query.correct_model", in.CorrectModel)
	errs.text("query.translate_model", in.TranslateModel)
	switch in.ExcludeUnverified {
	case "", "false":
	case "true":
		f.ExcludeUnverified = true
	default:
		errs.add("query.exclude_unverified", "必須是 true 或 false。")
	}
	sort := in.Sort
	switch sort {
	case "":
		sort = "score"
	case "score", "new":
	default:
		errs.add("query.sort", oneOf([]string{"score", "new"}))
	}
	f.ByScore = sort == "score"
	limit := defaultListLimit
	if in.Limit != "" {
		n, err := strconv.Atoi(in.Limit)
		if err != nil || n < 1 || n > maxListLimit || len(in.Limit) > 3 {
			errs.add("query.limit", "必須是 1～200 的整數。")
		} else {
			limit = n
		}
	}
	if in.Cursor != "" {
		c, ok := decodeCursor(in.Cursor, sort)
		if !ok {
			errs.add("query.cursor", "cursor 不正確，請從第一頁重新開始。")
		}
		f.After = c
	}
	return f, limit, sort, errs.err()
}

// ---- 可見度 ----

// seenBy 是 viewer 在 SQL 預先過濾的條件（hubrepo.Listable 加上「自己是上傳者」）。
func (rt *routes) seenBy(viewer hubrepo.Viewer) hubstore.SeenBy {
	vis, all := hubrepo.Listable(viewer, rt.d.Config.SiteVisibility)
	s := hubstore.SeenBy{SeeAll: all, Visibilities: vis}
	if !viewer.Anonymous {
		s.UserID = viewer.UserID
	}
	return s
}

// listable 回報版本能不能出現在列表、影片頁與查詢中：visible，而且 canSee（回傳上傳者給回應重用）。
func (rt *routes) listable(ctx context.Context, v hubstore.Version, viewer hubrepo.Viewer) (bool, []hubstore.Uploader) {
	if v.State != hubrepo.StateVisible {
		return false, nil
	}
	return rt.canSee(ctx, v, viewer)
}

func versionItem(v hubstore.Version, video hubstore.Video, ups []hubstore.Uploader) VersionItem {
	it := VersionItem{
		ID: v.ID, Video: videoRef(video), Kind: v.Kind, Variant: v.Variant, Lang: v.Lang,
		TranscribeModel: v.TranscribeModel, CorrectModel: v.CorrectModel, TranslateModel: v.TranslateModel,
		ModelVerified: v.ModelVerified, License: v.License, Score: v.Score, CreatedAt: v.CreatedAt, SHA256: v.SHA256,
		Uploaders: make([]UploaderView, 0, len(ups)),
	}
	if !v.ModelVerified {
		it.Badges = []string{"unverified_model"}
	}
	for _, u := range ups {
		it.Uploaders = append(it.Uploaders, UploaderView{DisplayName: u.DisplayName})
	}
	return it
}

func (rt *routes) viewerOf(ctx context.Context) (hubrepo.Viewer, error) {
	// 匿名的請求只有在站台公開、middleware 放行時才會到這裡（HACC-R13）。
	return rt.viewer(ctx, principalFrom(ctx))
}

// ---- 列表 ----

func (rt *routes) listVersions(ctx context.Context, in *listVersionsInput) (*listVersionsOutput, error) {
	f, limit, sort, err := parseList(in)
	if err != nil {
		return nil, err
	}
	viewer, err := rt.viewerOf(ctx)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	f.Seen = rt.seenBy(viewer)
	f.Limit = limit + 1
	rows, err := rt.d.Store.ListVersions(ctx, f)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	out := &listVersionsOutput{Body: VersionListView{Versions: []VersionItem{}}}
	if len(rows) > limit {
		rows = rows[:limit]
		// next_cursor 取 SQL 掃到的最後一列，不是過濾後的最後一列，才不會重複或跳過。
		out.Body.NextCursor = encodeCursor(sort, rows[limit-1])
	}
	for _, r := range rows {
		ok, ups := rt.listable(ctx, r.Version, viewer)
		if !ok {
			continue
		}
		out.Body.Versions = append(out.Body.Versions, versionItem(r.Version, r.Video, ups))
	}
	return out, nil
}

// ---- 影片頁 ----

func videoNotFound() *apierr.Error {
	return apierr.New(http.StatusNotFound, apierr.CodeNotFound, "找不到這部影片。")
}

func orderOf(vals []string, v string) int {
	if i := slices.Index(vals, v); i >= 0 {
		return i
	}
	return len(vals)
}

// videoPage 回傳影片所有看得到的 visible 版本（HREP-R17）。影片不存在、平台不對，或看不到任何版本時，
// 一律回相同的 404（不洩漏只有私人版本的影片存在）。
func (rt *routes) videoPage(ctx context.Context, in *videoPageInput) (*videoPageOutput, error) {
	part := 0
	if in.Part != "" {
		n, err := strconv.Atoi(in.Part)
		if err != nil || n < 1 || n > 9999 || len(in.Part) > 4 {
			var errs fieldErrs
			errs.add("query.part", "必須是 1～9999 的整數。")
			return nil, errs.err()
		}
		part = n
	}
	viewer, err := rt.viewerOf(ctx)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	if !slices.Contains(listPlatforms, in.Platform) || in.ExternalID == "" || !plainText(in.ExternalID) {
		return nil, videoNotFound()
	}
	video, err := rt.d.Store.VideoByKey(ctx, in.Platform, in.ExternalID, part)
	if errors.Is(err, hubstore.ErrNotFound) {
		return nil, videoNotFound()
	}
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	vs, err := rt.d.Store.VideoVersions(ctx, video.ID)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	var items []VersionItem
	for _, v := range vs {
		if ok, ups := rt.listable(ctx, v, viewer); ok {
			items = append(items, versionItem(v, video, ups))
		}
	}
	if len(items) == 0 {
		return nil, videoNotFound()
	}
	slices.SortStableFunc(items, func(a, b VersionItem) int {
		if c := orderOf(listKinds, a.Kind) - orderOf(listKinds, b.Kind); c != 0 {
			return c
		}
		if c := orderOf(listVariants, a.Variant) - orderOf(listVariants, b.Variant); c != 0 {
			return c
		}
		if c := strings.Compare(a.Variant, b.Variant); c != 0 {
			return c
		}
		if c := strings.Compare(a.Lang, b.Lang); c != 0 {
			return c
		}
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	ref := videoRef(video)
	page := VideoPageView{Video: VideoView{Platform: ref.Platform, ExternalID: ref.ExternalID, Part: ref.Part, Title: video.Title,
		ChannelExternalID: video.ChannelExternalID, ChannelTitle: video.ChannelTitle}, Groups: []VersionGroup{}}
	for _, it := range items {
		n := len(page.Groups)
		if n == 0 || page.Groups[n-1].Kind != it.Kind || page.Groups[n-1].Variant != it.Variant || page.Groups[n-1].Lang != it.Lang {
			page.Groups = append(page.Groups, VersionGroup{Kind: it.Kind, Variant: it.Variant, Lang: it.Lang})
			n++
		}
		page.Groups[n-1].Versions = append(page.Groups[n-1].Versions, it)
	}
	return &videoPageOutput{Body: page}, nil
}

// ---- ASR 前查詢 ----

// lookup 回傳影片雜湊以 prefix 開頭的影片與看得到的原始字幕（HREP-R19、ADR-0032 第 3 點）。
// 沒有看得到的版本的影片不列出：「桶中只有別人的私人版本」與「空桶」的回應完全相同。
// 次數限制沿用一般限流（HACC-R18，以帳號計）。
func (rt *routes) lookup(ctx context.Context, in *lookupInput) (*lookupOutput, error) {
	var errs fieldErrs
	switch {
	case in.Platform == "":
		errs.add("query.platform", "請指定平台（"+strings.Join(listPlatforms, "、")+"）。")
	default:
		errs.enum("query.platform", in.Platform, listPlatforms)
	}
	if !prefixPattern.MatchString(in.Prefix) {
		errs.add("query.prefix", "必須是剛好 4 個小寫十六進位字元。")
	}
	if err := errs.err(); err != nil {
		return nil, err
	}
	viewer, err := rt.viewerOf(ctx)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	rows, err := rt.d.Store.LookupVersions(ctx, in.Platform, in.Prefix, rt.seenBy(viewer))
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	out := &lookupOutput{CacheControl: "private, no-store", Body: LookupView{Videos: []LookupVideoView{}}}
	for _, r := range rows {
		if !strings.HasPrefix(r.VideoKeySHA256, in.Prefix) {
			continue
		}
		if ok, _ := rt.listable(ctx, r.Version, viewer); !ok {
			continue
		}
		n := len(out.Body.Videos)
		if n == 0 || out.Body.Videos[n-1].VideoKeySHA256 != r.VideoKeySHA256 {
			out.Body.Videos = append(out.Body.Videos, LookupVideoView{VideoKeySHA256: r.VideoKeySHA256})
			n++
		}
		out.Body.Videos[n-1].Versions = append(out.Body.Videos[n-1].Versions, LookupVersionView{
			ID: r.ID, Variant: r.Variant, Lang: r.Lang, SHA256: r.SHA256, Score: r.Score, ModelVerified: r.ModelVerified})
	}
	return out, nil
}
