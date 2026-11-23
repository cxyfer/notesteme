package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"regexp"
	"strings"

	"github.com/cxyfer/notesteme/internal/hubclient"
	"github.com/cxyfer/notesteme/internal/hubshare"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/source/bilibili"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// Client 端的 Hub（HSHR）：transcript 的 cookie_required（R7）、從 Hub 重新生成（R9～R11）、
// ASR 前查詢（R12～R15）與分享（R2～R6）。Hub 的回應一律經 internal/hubclient 驗證。

// SourceHub 是從 Hub 取得的字幕的來源 ID（steps.md 第 3 節）與 executor。
const SourceHub = "hub"

// msgHubPrecheckFailed 是 ASR 前查詢失敗、改用付費來源時的提醒（HSHR-R15）。
func msgHubPrecheckFailed(reason string) string {
	return "無法查詢 Hub（" + reason + "），這次改用付費來源。"
}

// HubError 是分享、重新生成時 Client 端的檢查錯誤（HSHR-R3～R5、R9）。Path 非空時是欄位錯誤。
type HubError struct {
	Status  int
	Code    string
	Msg     string
	Path    string
	Actions []string
}

func (e *HubError) Error() string { return e.Msg }

func hubErr(status int, code, msg string) *HubError { return &HubError{Status: status, Code: code, Msg: msg} }

func notShareable(msg string) *HubError {
	return hubErr(http.StatusUnprocessableEntity, "not_shareable", msg)
}

// cookieRequired 是 transcript 寫入 source 產出時的 cookie_required（HSHR-R7）：
// 來源需要登入才拿得到時為 true（bilibili-ai、availability 不是 public 的平台字幕與媒體）。
// 本機檔案與 availability 不明的影音為 false。
func cookieRequired(it store.Item, sourceID string) bool {
	if sourceID == transcript.SourceBilibiliAI {
		return true
	}
	if it.Platform == source.PlatformLocal || it.Availability == "" {
		return false
	}
	return it.Availability != "public"
}

// hubOrigin 是 hub_imports.origin_json 與來源紀錄的 hub_origin（HSHR-R9）。
type hubOrigin struct {
	URL       string `json:"url"`
	VersionID string `json:"version_id"`
	SHA256    string `json:"sha256"`
	Uploader  string `json:"uploader"`
	// 以下是 Hub 上原本的模型欄位，寫在來源紀錄的頂層（model_verified 照抄）。
	Lang            string `json:"lang"`
	TranscribeModel string `json:"transcribe_model,omitempty"`
	CorrectModel    string `json:"correct_model,omitempty"`
	TranslateModel  string `json:"translate_model,omitempty"`
	ModelVerified   bool   `json:"model_verified"`
}

func originOf(hubURL string, v hubclient.Version) hubOrigin {
	return hubOrigin{URL: hubURL, VersionID: v.ID, SHA256: v.SHA256, Uploader: v.Uploader, Lang: v.Lang,
		TranscribeModel: v.TranscribeModel, CorrectModel: v.CorrectModel, TranslateModel: v.TranslateModel, ModelVerified: v.ModelVerified}
}

// hubArtifact 把驗證過的 Hub 字幕寫成 source 產出，來源紀錄為 executor: hub、source: hub。
func (p *Pipeline) hubArtifact(ctx context.Context, it store.Item, tr subtitle.Transcript, o hubOrigin) (*store.NewArtifact, string, error) {
	lang := transcript.NormalizeLang(o.Lang)
	if lang == "" {
		lang = "und"
	}
	fields := map[string]any{
		"stage": StageTranscript, "impl_version": p.implVersion(StageTranscript),
		"executor": SourceHub, "source": SourceHub, "timing": "cue", "cookie_required": false,
		"hub_origin":     map[string]any{"url": o.URL, "version_id": o.VersionID, "sha256": o.SHA256, "uploader": o.Uploader},
		"model_verified": o.ModelVerified,
		"track":          map[string]string{"lang": lang, "kind": SourceHub},
	}
	for k, v := range map[string]string{"transcribe_model": o.TranscribeModel, "correct_model": o.CorrectModel, "translate_model": o.TranslateModel} {
		if v != "" {
			fields[k] = v
		}
	}
	prov, err := p.provenance(fields, nil)
	if err != nil {
		return nil, "", err
	}
	art, err := p.writeCuesArtifact(ctx, it.ID, subtitle.Transcript{Lang: lang, Variant: subtitle.VariantSource, Cues: tr.Cues}, nil, prov)
	return art, lang, err
}

// runHubImport 寫入等待中的 Hub 字幕（HSHR-R9、R11）：不呼叫任何其他字幕來源，transcript 步驟 done、來源 hub。
// 快取鍵含版本的 sha256，所以不會命中先前其他來源的結果；寫入確實 commit 之後才刪掉等待中的列。
func (p *Pipeline) runHubImport(ctx context.Context, it store.Item, imp store.HubImport) (result, error) {
	var o hubOrigin
	if err := json.Unmarshal([]byte(imp.OriginJSON), &o); err != nil {
		return result{}, errPermanent("Hub 字幕的來源紀錄損壞，請重新從 Hub 匯入。")
	}
	tr, err := hubclient.ValidateCues(imp.Content)
	if err != nil {
		return result{}, errPermanent(err.Error())
	}
	stage := &store.StageResult{Executor: SourceHub, ImplVersion: p.implVersion(StageTranscript),
		CacheKey: cacheKey{Item: it.ID, Stage: StageTranscript, Impl: p.implVersion(StageTranscript), Provider: SourceHub,
			Upstream: paramsSHA(map[string]any{"hub_sha256": imp.SHA256}), Params: paramsSHA(map[string]any{"version": imp.VersionID})}.String()}
	art, lang, err := p.hubArtifact(ctx, it, tr, o)
	if err != nil {
		return result{}, err
	}
	sha := imp.SHA256
	return result{
		task: taskDone, advance: true, stage: stage, artifact: art,
		probes: toProbes([]transcript.Decision{{Source: SourceHub, Lang: lang, State: transcript.StateUsed}}),
		state:  store.StageState{Status: store.StatusDone},
		afterCommit: func() {
			if err := p.st.Write().DeleteHubImport(context.WithoutCancel(ctx), it.ID, sha); err != nil {
				p.log.Warn("刪除已寫入的 Hub 字幕失敗", "item", it.ID, "error", err)
			}
		},
	}, nil
}

// pendingHubImport 回傳影音等待中的 Hub 字幕；沒有時 ok 為 false。
func (p *Pipeline) pendingHubImport(ctx context.Context, itemID string) (store.HubImport, bool, error) {
	imp, err := p.st.Read().HubImport(ctx, itemID)
	if errors.Is(err, store.ErrNotFound) {
		return store.HubImport{}, false, nil
	}
	return imp, err == nil, err
}

// ---- ASR 前查詢（HSHR-R12～R15）----

// paidSource 回報 fallback 來源是否要付費（Supadata 與 ASR provider）。
func paidSource(id string) bool { return id == "supadata" || isASR(id) }

// hubPrecheckOutcome 是一次 ASR 前查詢的結果：hit 非 nil 是命中；note 非空是失敗的提醒。
type hubPrecheckOutcome struct {
	hit  *result
	note string
}

// precheckHub 在即將呼叫第一個付費來源時查詢 Hub 一次（HSHR-R13、R14）。不查詢（關閉、沒有設定、
// 本機檔案或非公開影音）時回傳零值；失敗時照常繼續（R15），回傳提醒。
func (p *Pipeline) precheckHub(ctx context.Context, it store.Item, decisions []transcript.Decision, stage *store.StageResult) (hubPrecheckOutcome, error) {
	if p.hubPrecheck == nil || it.Platform == source.PlatformLocal || it.Availability != "public" {
		return hubPrecheckOutcome{}, nil
	}
	if ok, err := p.hub.Configured(ctx); err != nil || !ok {
		return hubPrecheckOutcome{}, nil
	}
	if on, err := p.hubPrecheck(ctx); err != nil || !on {
		return hubPrecheckOutcome{}, nil
	}
	fail := func(err error) (hubPrecheckOutcome, error) {
		if ctx.Err() != nil {
			return hubPrecheckOutcome{}, ctx.Err()
		}
		var he *hubclient.Error
		if !errors.As(err, &he) {
			p.log.Warn("Hub 查詢失敗", "item", it.ID, "error", err)
			return hubPrecheckOutcome{note: msgHubPrecheckFailed("連線失敗")}, nil
		}
		p.log.Info("Hub 查詢失敗，改用付費來源", "item", it.ID, "code", he.Code(), "status", he.Status)
		return hubPrecheckOutcome{note: msgHubPrecheckFailed(he.PrecheckReason())}, nil
	}
	key := hubclient.VideoKeySHA256(it.Platform, it.ExternalID, it.Part)
	videos, err := p.hub.Lookup(ctx, it.Platform, hubclient.Prefix(key))
	if err != nil {
		return fail(err)
	}
	pick, ok := hubclient.Pick(videos, key, it.OriginalLang)
	if !ok {
		return hubPrecheckOutcome{}, nil
	}
	v, err := p.hub.Version(ctx, pick.ID)
	if err != nil {
		return fail(err)
	}
	// 版本必須是這部影片的原始字幕，而且內容雜湊與查詢結果一致（Hub 是不可信的輸入）。
	if !sameVideo(v.Video, it) || v.Kind != "transcript" || (v.Variant != "source" && v.Variant != "normalized") || v.SHA256 != pick.SHA256 {
		return fail(&hubclient.Error{Kind: hubclient.KindContentInvalid, Detail: "版本與查詢結果不一致"})
	}
	data, err := p.hub.Content(ctx, v.ID, v.SHA256, hubclient.LookupTimeout)
	if err != nil {
		return fail(err)
	}
	tr, err := hubclient.ValidateCues(data)
	if err != nil {
		return fail(err)
	}
	art, lang, err := p.hubArtifact(ctx, it, tr, originOf(p.hub.URL(), v))
	if err != nil {
		return hubPrecheckOutcome{}, err
	}
	ds := append(slices.Clone(decisions), transcript.Decision{Source: SourceHub, Lang: lang, State: transcript.StateUsed})
	return hubPrecheckOutcome{hit: &result{
		task: taskDone, advance: true, stage: stage, artifact: art, probes: toProbes(ds),
		state: store.StageState{Status: store.StatusDone},
	}}, nil
}

// sameVideo 回報 Hub 版本的影片是否就是這部影音。
func sameVideo(v hubclient.VideoRef, it store.Item) bool {
	part := 0
	if v.Part != nil {
		part = *v.Part
	}
	return v.Platform == it.Platform && v.ExternalID == it.ExternalID && part == it.Part
}

// addNote 把提醒附在步驟的訊息上（已有訊息時以「；」連接）。
func addNote(res *result, note string) {
	if note == "" || res.state.Status != store.StatusDone {
		return
	}
	if res.state.Message == "" {
		res.state.Message = note
		return
	}
	res.state.Message += "；" + note
}

// ---- 重新生成（HSHR-R9～R11）----

// HubImportRequest 是 POST /api/hub/import 的內容。
type HubImportRequest struct {
	Version   string
	Profile   string
	Overrides map[string]any
}

// HubImportResult 是重新生成的影音。
type HubImportResult struct {
	ItemID string
	Ref    string
}

// msgNotRaw 是 HSHR-R9 的 not_raw。
const msgNotRaw = "只能用別人的原始字幕（source 或 normalized）重新生成。"

// ImportFromHub 以 Hub 上的原始字幕重新生成影音（HSHR-R9～R11）：先取得並驗證版本與內容，
// 通過之後才新增影音（或對既有影音從 transcript 重跑），transcript 步驟寫入這份字幕、不呼叫其他來源。
//
// 錯誤：hubclient.ErrNotConfigured（409）、*hubclient.Error（502／404／422）、*HubError（not_raw）、
// *InTrashError（409）、新增影音或重跑的錯誤。
func (p *Pipeline) ImportFromHub(ctx context.Context, req HubImportRequest) (HubImportResult, error) {
	if ok, err := p.hub.Configured(ctx); err != nil {
		return HubImportResult{}, err
	} else if !ok {
		return HubImportResult{}, hubclient.ErrNotConfigured
	}
	v, err := p.hub.Version(ctx, req.Version)
	if err != nil {
		return HubImportResult{}, err
	}
	if v.Kind != "transcript" || (v.Variant != "source" && v.Variant != "normalized") {
		return HubImportResult{}, hubErr(http.StatusUnprocessableEntity, "not_raw", msgNotRaw)
	}
	data, err := p.hub.Content(ctx, v.ID, v.SHA256, 0)
	if err != nil {
		return HubImportResult{}, err
	}
	if _, err := hubclient.ValidateCues(data); err != nil {
		return HubImportResult{}, err
	}
	origin, err := json.Marshal(originOf(p.hub.URL(), v))
	if err != nil {
		return HubImportResult{}, err
	}
	part := 0
	if v.Video.Part != nil {
		part = *v.Video.Part
	}
	imp := store.HubImport{VersionID: v.ID, SHA256: v.SHA256, OriginJSON: string(origin), Content: data}

	it, err := p.st.Read().ItemByKey(ctx, v.Video.Platform, v.Video.ExternalID, part)
	switch {
	case err == nil && it.DeletedAt != nil:
		return HubImportResult{}, &InTrashError{ItemID: it.ID}
	case err == nil:
		// HSHR-R11：既有的影音從 transcript 重跑，新的 source 版本取代現行版本（舊版本保留）。
		imp.ItemID = it.ID
		if err := p.st.Write().UpsertHubImport(ctx, imp); err != nil {
			return HubImportResult{}, err
		}
		if _, err := p.Rerun(ctx, it.ID, RerunRequest{From: StageTranscript, Profile: req.Profile, Overrides: req.Overrides}); err != nil {
			_ = p.st.Write().DeleteHubImport(context.WithoutCancel(ctx), it.ID, v.SHA256)
			return HubImportResult{}, err
		}
		return HubImportResult{ItemID: it.ID, Ref: source.Ref(it.Platform, it.ExternalID, it.Part)}, nil
	case !errors.Is(err, store.ErrNotFound):
		return HubImportResult{}, err
	}
	// 新的影音：網址由 Client 依驗證過的影片 ID 組出，不使用 Hub 給的任何網址；影音資訊照常由 metadata 從平台取得。
	var u string
	switch v.Video.Platform {
	case source.PlatformYouTube:
		u = youtube.WatchURL(v.Video.ExternalID)
	case source.PlatformBilibili:
		u = bilibili.VideoURL(v.Video.ExternalID, part)
	default:
		return HubImportResult{}, hubErr(http.StatusUnprocessableEntity, "not_in_library",
			"這個 Podcast 單集不在媒體庫中。請先新增它的 feed，再從 Hub 重新生成。")
	}
	res, err := p.Ingest(ctx, IngestRequest{URL: u, Profile: req.Profile, Overrides: req.Overrides})
	if err != nil {
		return HubImportResult{}, err
	}
	if len(res) == 0 {
		return HubImportResult{}, fmt.Errorf("新增影音沒有結果")
	}
	got := res[0]
	if got.InTrash {
		return HubImportResult{}, &InTrashError{ItemID: got.Item.ID}
	}
	imp.ItemID = got.Item.ID
	if err := p.st.Write().UpsertHubImport(ctx, imp); err != nil {
		return HubImportResult{}, err
	}
	if got.AlreadyInLibrary {
		// 在查詢與新增之間被別人新增：同既有影音。
		if _, err := p.Rerun(ctx, got.Item.ID, RerunRequest{From: StageTranscript, Profile: req.Profile, Overrides: req.Overrides}); err != nil {
			return HubImportResult{}, err
		}
	}
	return HubImportResult{ItemID: got.Item.ID, Ref: source.Ref(got.Item.Platform, got.Item.ExternalID, got.Item.Part)}, nil
}

// ---- 分享（HSHR-R2～R6）----

// HubArtifactRef 指定要分享的產出（HSHR-R5 的 artifacts）。
type HubArtifactRef struct {
	Kind, Variant, Lang string
}

// HubShareRequest 是 POST /api/items/:id/hub/share 的內容。
type HubShareRequest struct {
	License       string
	Attest        bool
	Artifacts     []HubArtifactRef
	IncludeFrames bool
	Visibility    string
}

// HubSharedVersion 是分享結果的一個版本。
type HubSharedVersion struct {
	Kind         string `json:"kind"`
	Variant      string `json:"variant"`
	Lang         string `json:"lang"`
	VersionID    string `json:"version_id"`
	Deduplicated bool   `json:"deduplicated"`
}

// HubShareResult 是分享的結果。
type HubShareResult struct {
	ShareID  string
	Versions []HubSharedVersion
}

// CheckShareRequest 依 HSHR-R3 檢查請求（不連線到 Hub）。
func CheckShareRequest(req HubShareRequest) error {
	if !req.Attest {
		return hubErr(http.StatusUnprocessableEntity, "attest_required", "分享前要確認你有權分享這些內容。")
	}
	if req.License == "" {
		e := hubErr(http.StatusUnprocessableEntity, "invalid_request", "請選擇授權方式。")
		e.Path = "license"
		return e
	}
	if !slices.Contains(hubshare.Licenses, req.License) {
		return hubErr(http.StatusUnprocessableEntity, "license_invalid", "不支援的授權："+clip(req.License, 100)+"。")
	}
	return nil
}

func clip(s string, n int) string {
	r := []rune(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

// variantOrder 是分享時字幕變體的順序（管線順序）。
var variantOrder = []string{"source", "normalized", "segmented", "corrected", "translated", "edited"}

// kindOrder 是分享時產出種類的順序。
var kindOrder = []string{"transcript", "chapters", "note", "frames"}

// ShareToHub 產生 bundle 並送到 Hub（HSHR-R2～R6），成功時記入 hub_shares。
func (p *Pipeline) ShareToHub(ctx context.Context, itemID string, req HubShareRequest) (HubShareResult, error) {
	if err := CheckShareRequest(req); err != nil {
		return HubShareResult{}, err
	}
	r := p.st.Read()
	it, err := r.ItemByID(ctx, itemID)
	if err != nil {
		return HubShareResult{}, err
	}
	if it.DeletedAt != nil {
		return HubShareResult{}, &InTrashError{ItemID: it.ID}
	}
	if ok, err := p.hub.Configured(ctx); err != nil {
		return HubShareResult{}, err
	} else if !ok {
		return HubShareResult{}, hubclient.ErrNotConfigured
	}
	if it.Platform == source.PlatformLocal {
		return HubShareResult{}, notShareable("本機檔案不能分享到 Hub。")
	}
	if it.Availability != "public" {
		return HubShareResult{}, notShareable("非公開影音不能分享到 Hub。")
	}
	arts, err := p.shareArtifacts(ctx, it, req)
	if err != nil {
		return HubShareResult{}, err
	}
	raw, err := hubshare.Build(hubshare.Item{
		Platform: it.Platform, ExternalID: it.ExternalID, Part: it.Part, Title: cmpOr(it.Title, it.ExternalID),
		PublishedAt: it.PublishedAt, DurationS: it.DurationS, OriginalLang: validLang(it.OriginalLang), Availability: it.Availability,
	}, arts, hubshare.Options{License: req.License, AppVersion: p.version, Now: p.clock.Now()})
	if err != nil {
		return HubShareResult{}, err
	}
	res, err := p.hub.Share(ctx, raw, req.Visibility)
	if err != nil {
		return HubShareResult{}, err
	}
	out := HubShareResult{ShareID: res.ShareID, Versions: []HubSharedVersion{}}
	for _, v := range res.Versions {
		out.Versions = append(out.Versions, HubSharedVersion{Kind: v.Kind, Variant: v.Variant, Lang: v.Lang, VersionID: v.ID, Deduplicated: v.Deduplicated})
	}
	vj, err := json.Marshal(out.Versions)
	if err != nil {
		return HubShareResult{}, err
	}
	// 分享已經在 Hub 上成立：記錄失敗只寫 log，不讓請求失敗（重送會在 Hub 去重）。
	if err := p.st.Write().InsertHubShare(context.WithoutCancel(ctx), store.HubShare{ItemID: it.ID, HubURL: p.hub.URL(), ShareID: res.ShareID, VersionsJSON: string(vj)}); err != nil {
		p.log.Error("記錄 Hub 分享失敗", "item", it.ID, "error", err)
	}
	return out, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var langTagRe = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8}){0,4}$`)

// validLang 回傳合法的 BCP 47 標記，否則空字串（bundle 驗證不接受不合法的語言）。
func validLang(l string) string {
	if l == "" || l == "und" || !langTagRe.MatchString(l) {
		return ""
	}
	return l
}

// shareArtifacts 選出要分享的產出（HSHR-R5），檢查衍生鏈上的 cookie_required（R4），並組出 bundle 的產出（R6）。
func (p *Pipeline) shareArtifacts(ctx context.Context, it store.Item, req HubShareRequest) ([]hubshare.Artifact, error) {
	r := p.st.Read()
	cur, err := r.CurrentArtifacts(ctx, it.ID, "")
	if err != nil {
		return nil, err
	}
	var chosen []store.Artifact
	if len(req.Artifacts) > 0 {
		for _, want := range req.Artifacts {
			i := slices.IndexFunc(cur, func(a store.Artifact) bool {
				return a.Kind == want.Kind && (want.Variant == "" || a.Variant == want.Variant) && (want.Lang == "" || a.Lang == want.Lang)
			})
			if i < 0 || !slices.Contains(kindOrder, want.Kind) {
				return nil, hubErr(http.StatusUnprocessableEntity, "artifact_not_found",
					"這部影音沒有「"+clip(cmpOr(want.Variant, want.Kind), 40)+"（"+clip(cmpOr(want.Lang, "-"), 20)+"）」的字幕。")
			}
			if !slices.ContainsFunc(chosen, func(a store.Artifact) bool { return a.ID == cur[i].ID }) {
				chosen = append(chosen, cur[i])
			}
		}
	} else {
		for _, a := range cur {
			switch {
			case a.Kind == "transcript" && a.Variant == "source" && executorOf(a.Provenance) == SourceHub:
				// 從 Hub 取得的原始字幕已經在 Hub 上（HSHR-R5）。
			case a.Kind == "transcript", a.Kind == "chapters", a.Kind == "note":
				chosen = append(chosen, a)
			case a.Kind == "frames" && req.IncludeFrames:
				chosen = append(chosen, a)
			}
		}
	}
	slices.SortStableFunc(chosen, func(a, b store.Artifact) int {
		if c := slices.Index(kindOrder, a.Kind) - slices.Index(kindOrder, b.Kind); c != 0 {
			return c
		}
		if c := slices.Index(variantOrder, a.Variant) - slices.Index(variantOrder, b.Variant); c != 0 {
			return c
		}
		return strings.Compare(a.Lang, b.Lang)
	})
	if len(chosen) == 0 {
		return nil, hubErr(http.StatusUnprocessableEntity, "nothing_to_share", "這部影音還沒有可以分享的字幕或筆記。")
	}
	chain := chainWalker{r: r, ctx: ctx, it: it, seen: map[string]store.Artifact{}}
	out := make([]hubshare.Artifact, 0, len(chosen))
	for _, a := range chosen {
		needsLogin, origin, err := chain.inspect(a)
		if err != nil {
			return nil, err
		}
		if needsLogin {
			return nil, notShareable("需要登入才能取得的字幕不能分享到 Hub。")
		}
		ha, err := p.bundleArtifact(ctx, a)
		if err != nil {
			return nil, err
		}
		ha.HubOrigin = origin
		for _, pid := range a.Parents {
			if pa, err := chain.get(pid); err == nil {
				ha.ParentsSHA256 = append(ha.ParentsSHA256, pa.SHA256)
			}
		}
		out = append(out, ha)
	}
	return out, nil
}

func executorOf(prov json.RawMessage) string {
	var m struct {
		Executor string `json:"executor"`
	}
	_ = json.Unmarshal(prov, &m)
	return m.Executor
}

// chainWalker 沿著 parents 走訪衍生鏈（有快取）。
type chainWalker struct {
	r    *store.Repo
	ctx  context.Context
	it   store.Item
	seen map[string]store.Artifact
}

func (c *chainWalker) get(id string) (store.Artifact, error) {
	if a, ok := c.seen[id]; ok {
		return a, nil
	}
	a, err := c.r.Artifact(c.ctx, id)
	if err != nil {
		return store.Artifact{}, err
	}
	c.seen[id] = a
	return a, nil
}

// inspect 回報衍生鏈（含自己）上的 source 是否需要登入（HSHR-R4、R7；缺欄位的舊產出依 ADR-0032 第 2 點推導），
// 以及鏈上 hub 來源的 hub_origin。
func (c *chainWalker) inspect(a store.Artifact) (bool, *hubshare.HubOrigin, error) {
	var origin *hubshare.HubOrigin
	stack, visited := []store.Artifact{a}, map[string]bool{}
	for len(stack) > 0 && len(visited) < 1000 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[cur.ID] {
			continue
		}
		visited[cur.ID] = true
		var prov struct {
			Executor       string `json:"executor"`
			Source         string `json:"source"`
			CookieRequired *bool  `json:"cookie_required"`
			HubOrigin      struct {
				VersionID string `json:"version_id"`
				SHA256    string `json:"sha256"`
			} `json:"hub_origin"`
		}
		_ = json.Unmarshal(cur.Provenance, &prov)
		if cur.Kind == "transcript" && cur.Variant == "source" {
			need := cookieRequired(c.it, prov.Source)
			if prov.CookieRequired != nil {
				need = *prov.CookieRequired || prov.Source == transcript.SourceBilibiliAI
			}
			if need && prov.Executor != SourceHub {
				return true, nil, nil
			}
		}
		if prov.Executor == SourceHub && prov.HubOrigin.VersionID != "" && origin == nil {
			origin = &hubshare.HubOrigin{VersionID: prov.HubOrigin.VersionID, SHA256: prov.HubOrigin.SHA256}
		}
		for _, pid := range cur.Parents {
			pa, err := c.get(pid)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return false, nil, err
			}
			stack = append(stack, pa)
		}
	}
	return false, origin, nil
}

// bundleArtifact 讀出產出的內容並轉成送到 Hub 的形式：字幕原樣（cues JSON）、筆記取 Markdown 本文、
// 章節原樣、畫面只放選中的畫面（沒有候選與本機檔名）與圖片。
func (p *Pipeline) bundleArtifact(ctx context.Context, a store.Artifact) (hubshare.Artifact, error) {
	data, err := os.ReadFile(filepath.Join(p.dataDir, filepath.FromSlash(a.Path)))
	if err != nil {
		return hubshare.Artifact{}, fmt.Errorf("讀取產出 %s：%w", a.ID, err)
	}
	out := hubshare.Artifact{Kind: a.Kind, Variant: a.Variant, Lang: validLang(a.Lang), Provenance: a.Provenance}
	switch a.Kind {
	case "transcript":
		if out.Lang == "" {
			out.Lang = "und"
		}
		out.Data = data
	case "note":
		var doc noteDoc
		if err := json.Unmarshal(data, &doc); err != nil {
			return hubshare.Artifact{}, fmt.Errorf("筆記的內容損壞：%w", err)
		}
		out.Data = []byte(doc.Markdown)
	case "frames":
		var m framesManifest
		if err := json.Unmarshal(data, &m); err != nil {
			return hubshare.Artifact{}, fmt.Errorf("畫面的內容損壞：%w", err)
		}
		type frame struct {
			N       int    `json:"n"`
			AtMS    int64  `json:"at_ms"`
			Caption string `json:"caption,omitempty"`
			Chapter string `json:"chapter,omitempty"`
		}
		frames := []frame{}
		for _, c := range m.Candidates {
			if !c.Selected {
				continue
			}
			img, err := os.ReadFile(filepath.Join(framesDir(p.dataDir, a.ItemID, a.ID), frameFile(c.N)))
			if err != nil {
				return hubshare.Artifact{}, fmt.Errorf("讀取畫面 %d：%w", c.N, err)
			}
			frames = append(frames, frame{N: c.N, AtMS: c.AtMS, Caption: c.Caption, Chapter: c.Chapter})
			out.Images = append(out.Images, hubshare.Image{N: c.N, Data: img})
		}
		if out.Data, err = json.Marshal(map[string]any{"frames": frames}); err != nil {
			return hubshare.Artifact{}, err
		}
	default:
		if !json.Valid(data) {
			return hubshare.Artifact{}, fmt.Errorf("產出 %s 不是 JSON", a.ID)
		}
		out.Data = bytes.TrimSpace(data)
	}
	return out, nil
}

// ItemHubShares 列出影音的分享紀錄（GET /api/items/:id 的 hub_shares）。
func (p *Pipeline) ItemHubShares(ctx context.Context, itemID string) ([]store.HubShare, error) {
	return p.st.Read().ItemHubShares(ctx, itemID)
}

// configuredOK 回報有 configured 檢查的來源目前是否有設定（讀取失敗時視為有設定，交給 fetch 處理）。
func configuredOK(ctx context.Context, f fallbackFetcher) bool {
	ok, err := f.configured(ctx)
	return err != nil || ok
}
