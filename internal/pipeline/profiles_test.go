package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

func timingCfg(mode string) map[string]any {
	return map[string]any{"timing": map[string]any{"split_timing": mode}}
}

func mustProfile(t *testing.T, r *rig, name string, cfg map[string]any) Profile {
	t.Helper()
	pf, err := r.p.CreateProfile(context.Background(), name, cfg)
	if err != nil {
		t.Fatalf("CreateProfile(%s)：%v", name, err)
	}
	return pf
}

func layersOf(t *testing.T, r *rig, itemID, stage string) Layers {
	t.Helper()
	_, raw, err := r.p.StageConfig(context.Background(), itemID, stage)
	if err != nil {
		t.Fatal(err)
	}
	var l Layers
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatalf("layers JSON：%v（%s）", err, raw)
	}
	return l
}

func splitTimingOf(t *testing.T, r *rig, itemID, stage string) any {
	t.Helper()
	cfg, _, err := r.p.StageConfig(context.Background(), itemID, stage)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(cfg, &m); err != nil {
		t.Fatal(err)
	}
	timing, _ := m["timing"].(map[string]any)
	return timing["split_timing"]
}

// 內建的 default 在 migration 種入：版本 1、config 是空的一層，而且是預設處理方案。
func TestProfiles_BuiltinDefault(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	list, err := r.p.Profiles(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("Profiles = %+v, %v", list, err)
	}
	d := list[0]
	if d.Name != "default" || d.Version != 1 || !d.Builtin || !d.IsDefault || len(d.Config) != 0 {
		t.Errorf("default = %+v", d)
	}
	if name, err := r.p.DefaultProfileName(ctx); err != nil || name != "default" {
		t.Errorf("DefaultProfileName = %q, %v", name, err)
	}
	if _, err := r.p.Profile(ctx, "nope"); !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("不存在的處理方案 = %v，預期 ErrProfileNotFound", err)
	}
}

// 建立：版本 1、存下的是這一層（不含內建預設）；名稱不合法是 path 為 name 的 422；同名是 ErrProfileExists。
func TestProfiles_Create(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	pf := mustProfile(t, r, "standard", timingCfg("forbid"))
	if pf.Version != 1 || pf.Builtin || pf.IsDefault || !reflect.DeepEqual(pf.Config, timingCfg("forbid")) || len(pf.SHA256) != 64 {
		t.Errorf("standard = %+v", pf)
	}
	if _, err := r.p.CreateProfile(ctx, "standard", nil); !errors.Is(err, ErrProfileExists) {
		t.Errorf("同名 = %v，預期 ErrProfileExists", err)
	}
	if empty, err := r.p.CreateProfile(ctx, "blank", nil); err != nil || len(empty.Config) != 0 {
		t.Errorf("config 省略視為 {}：%+v, %v", empty, err)
	}
	for _, bad := range []string{"", "Standard", "-x", "has space", "a/b", "名稱"} {
		var inv *InvalidError
		_, err := r.p.CreateProfile(ctx, bad, nil)
		if !errors.As(err, &inv) || len(inv.Fields) == 0 || inv.Fields[0].Path != "name" {
			t.Errorf("名稱 %q：err = %v，預期 path 為 name 的 InvalidError", bad, err)
		}
	}
}

// PATCH 是 RFC 7396 merge patch：物件逐欄位合併、null 刪除、陣列整個取代；每次都建立新版本（內容相同也一樣）；
// 舊版本不變（PRF-R3）。內建的 default 也可以修改。
func TestProfiles_PatchCreatesVersion(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	mustProfile(t, r, "standard", map[string]any{
		"language": map[string]any{"targets": []any{"en", "fr"}, "fallback": []any{"groq"}},
		"timing":   map[string]any{"split_timing": "forbid"},
	})
	v2, err := r.p.PatchProfile(ctx, "standard", map[string]any{
		"language": map[string]any{"targets": []any{"ja"}, "fallback": nil},
		"timing":   map[string]any{"split_timing": "free"},
	})
	if err != nil || v2.Version != 2 {
		t.Fatalf("PatchProfile = %+v, %v", v2, err)
	}
	want := map[string]any{
		"language": map[string]any{"targets": []any{"ja"}},
		"timing":   map[string]any{"split_timing": "free"},
	}
	if !reflect.DeepEqual(v2.Config, want) {
		t.Errorf("v2 config = %v，預期 %v", v2.Config, want)
	}
	// 內容相同的 patch 仍然建立新版本。
	v3, err := r.p.PatchProfile(ctx, "standard", map[string]any{})
	if err != nil || v3.Version != 3 || !reflect.DeepEqual(v3.Config, want) || v3.SHA256 != v2.SHA256 {
		t.Errorf("空 patch = %+v, %v，預期版本 3、內容與 sha256 不變", v3, err)
	}
	versions, err := r.p.ProfileVersions(ctx, "standard")
	if err != nil || len(versions) != 3 {
		t.Fatalf("ProfileVersions = %+v, %v", versions, err)
	}
	if versions[0].Version != 1 || versions[0].Config["language"].(map[string]any)["fallback"] == nil {
		t.Errorf("v1 不該被改動：%+v", versions[0])
	}
	if got, _ := r.p.Profile(ctx, "standard"); got.Version != 3 {
		t.Errorf("目前版本 = %d，預期 3", got.Version)
	}
	if _, err := r.p.PatchProfile(ctx, "nope", map[string]any{}); !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("patch 不存在的處理方案 = %v", err)
	}
	if d, err := r.p.PatchProfile(ctx, "default", timingCfg("free")); err != nil || d.Version != 2 || !d.Builtin {
		t.Errorf("內建的 default 也可以修改：%+v, %v", d, err)
	}
	if _, err := r.p.ProfileVersions(ctx, "nope"); !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("不存在的處理方案的版本 = %v", err)
	}
}

// 內建的 default 不能刪除（PRF-R7）；不存在的是 ErrProfileNotFound。
func TestProfiles_DeleteDefault409(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if err := r.p.DeleteProfile(ctx, "default"); !errors.Is(err, ErrBuiltinProfile) {
		t.Fatalf("刪除 default = %v，預期 ErrBuiltinProfile", err)
	}
	if _, err := r.p.Profile(ctx, "default"); err != nil {
		t.Errorf("default 應還在：%v", err)
	}
	if err := r.p.DeleteProfile(ctx, "nope"); !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("刪除不存在的 = %v", err)
	}
	// 就算預設處理方案改成別的，default 仍然是內建的，刪不掉。
	mustProfile(t, r, "other", nil)
	if _, err := r.p.SetDefaultProfile(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	if err := r.p.DeleteProfile(ctx, "default"); !errors.Is(err, ErrBuiltinProfile) {
		t.Errorf("預設改成 other 之後刪除 default = %v，預期 ErrBuiltinProfile", err)
	}
}

// 正被設為預設的處理方案不能刪除，used_by 是 ["default_profile"]；改回別的預設之後可以刪（連同所有版本）。
func TestProfiles_DeleteCurrentDefault409UsedBy(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	mustProfile(t, r, "standard", nil)
	if _, err := r.p.PatchProfile(ctx, "standard", timingCfg("free")); err != nil {
		t.Fatal(err)
	}
	pf, err := r.p.SetDefaultProfile(ctx, "standard")
	if err != nil || !pf.IsDefault {
		t.Fatalf("SetDefaultProfile = %+v, %v", pf, err)
	}
	if d, _ := r.p.Profile(ctx, "default"); d.IsDefault {
		t.Error("default 不再是預設")
	}
	var inUse *InUseError
	err = r.p.DeleteProfile(ctx, "standard")
	if !errors.As(err, &inUse) || inUse.Code != CodeProfileInUse || !reflect.DeepEqual(inUse.UsedBy, []string{"default_profile"}) {
		t.Fatalf("刪除預設處理方案 = %v，預期 profile_in_use、used_by=[default_profile]", err)
	}
	if _, err := r.p.Profile(ctx, "standard"); err != nil {
		t.Errorf("409 之後處理方案應還在：%v", err)
	}
	if _, err := r.p.SetDefaultProfile(ctx, "default"); err != nil {
		t.Fatal(err)
	}
	if err := r.p.DeleteProfile(ctx, "standard"); err != nil {
		t.Fatalf("不再是預設之後刪除：%v", err)
	}
	if _, err := r.p.Profile(ctx, "standard"); !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("刪除後 = %v", err)
	}
	if vs, err := r.st.Read().ProfileVersions(ctx, "standard"); err != nil || len(vs) != 0 {
		t.Errorf("版本應一併刪除：%+v, %v", vs, err)
	}
	if _, err := r.p.SetDefaultProfile(ctx, "standard"); !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("把不存在的設為預設 = %v", err)
	}
}

// 新增時記錄實際用到的 (處理方案, 版本)；沒有指定時用預設處理方案；快照記下各層（含內嵌的處理方案內容）。
func TestIngest_RecordsProfileVersion(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	mustProfile(t, r, "standard", timingCfg("forbid"))
	if _, err := r.p.PatchProfile(ctx, "standard", timingCfg("free")); err != nil {
		t.Fatal(err)
	}

	res, err := r.p.Ingest(ctx, IngestRequest{URL: watch, Profile: "standard"})
	if err != nil {
		t.Fatal(err)
	}
	it := res[0].Item
	if it.ProfileID != "standard" || it.ProfileVersion != 2 {
		t.Errorf("item 的處理方案 = %q v%d，預期 standard v2", it.ProfileID, it.ProfileVersion)
	}
	l := layersOf(t, r, it.ID, StageTranscript)
	if l.Profile.Name != "standard" || l.Profile.Version != 2 || l.Profile.Config["timing"] == nil || l.Channel.ID != "" {
		t.Errorf("layers = %+v", l)
	}
	if got := splitTimingOf(t, r, it.ID, StageTranscript); got != "free" {
		t.Errorf("split_timing = %v，預期 free（處理方案 v2）", got)
	}

	// 沒有指定：用預設處理方案。
	other, err := r.p.Ingest(ctx, IngestRequest{URL: "https://www.youtube.com/watch?v=def456xxxxx"})
	if err != nil {
		t.Fatal(err)
	}
	if o := other[0].Item; o.ProfileID != "default" || o.ProfileVersion != 1 {
		t.Errorf("預設處理方案 = %q v%d，預期 default v1", o.ProfileID, o.ProfileVersion)
	}
	if _, err := r.p.SetDefaultProfile(ctx, "standard"); err != nil {
		t.Fatal(err)
	}
	third, err := r.p.Ingest(ctx, IngestRequest{URL: "https://www.youtube.com/watch?v=ghi789xxxxx"})
	if err != nil {
		t.Fatal(err)
	}
	if o := third[0].Item; o.ProfileID != "standard" || o.ProfileVersion != 2 {
		t.Errorf("預設改成 standard 之後 = %q v%d", o.ProfileID, o.ProfileVersion)
	}
}

// 單次覆寫疊在處理方案之上；沒有指定處理方案時，內建 default 讓生效設定與 M1 相同。
func TestIngest_OverridesBeatProfile(t *testing.T) {
	r := newRig(t)
	mustProfile(t, r, "standard", timingCfg("forbid"))
	res, err := r.p.Ingest(context.Background(), IngestRequest{URL: watch, Profile: "standard", Overrides: timingCfg("free")})
	if err != nil {
		t.Fatal(err)
	}
	if got := splitTimingOf(t, r, res[0].Item.ID, StageNormalize); got != "free" {
		t.Errorf("split_timing = %v，預期 free（影音覆寫優先於處理方案）", got)
	}
}

// 不認識的處理方案是 422，路徑為 profile；沒有建立任何影音。
func TestIngest_UnknownProfile422Path(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	_, err := r.p.Ingest(ctx, IngestRequest{URL: watch, Profile: "nope"})
	var inv *InvalidError
	if !errors.As(err, &inv) || len(inv.Fields) != 1 || inv.Fields[0].Path != "profile" {
		t.Fatalf("err = %v，預期 path 為 profile 的 InvalidError", err)
	}
	if items, _ := r.p.Items(ctx); len(items) != 0 {
		t.Errorf("被拒絕的請求不應建立影音：%d", len(items))
	}
}

func ingestRun(t *testing.T, r *rig, profile string) store.Item {
	t.Helper()
	res, err := r.p.Ingest(context.Background(), IngestRequest{URL: watch, Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	r.run()
	return res[0].Item
}

// PRF-R3：編輯處理方案不影響已完成的影音；重新處理（不帶 profile）時才使用最新版本，並記回影音。
func TestRerun_UsesLatestProfileVersion(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = nil
	mustProfile(t, r, "standard", timingCfg("forbid"))
	it := ingestRun(t, r, "standard")
	oldRun := latestStage(t, r, it.ID, StageNormalize)

	if _, err := r.p.PatchProfile(ctx, "standard", timingCfg("free")); err != nil {
		t.Fatal(err)
	}
	if cur, _ := r.st.Read().ItemByID(ctx, it.ID); cur.ProfileVersion != 1 {
		t.Fatalf("PATCH 不該動到影音：profile_version = %d", cur.ProfileVersion)
	}
	if now := latestStage(t, r, it.ID, StageNormalize); now.ID != oldRun.ID || splitTimingOf(t, r, it.ID, StageNormalize) != "forbid" {
		t.Fatal("PATCH 不該動到已完成的步驟")
	}

	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize}); err != nil {
		t.Fatal(err)
	}
	cur, _ := r.st.Read().ItemByID(ctx, it.ID)
	if cur.ProfileID != "standard" || cur.ProfileVersion != 2 {
		t.Errorf("重跑之後 = %q v%d，預期 standard v2", cur.ProfileID, cur.ProfileVersion)
	}
	if got := splitTimingOf(t, r, it.ID, StageNormalize); got != "free" {
		t.Errorf("重跑後 split_timing = %v，預期 free", got)
	}
	if l := layersOf(t, r, it.ID, StageNormalize); l.Profile.Version != 2 {
		t.Errorf("layers 的處理方案版本 = %d，預期 2", l.Profile.Version)
	}
}

// 影音記錄的處理方案被刪除之後，不帶 profile 的重跑是 422（path 為 profile），而且什麼都不寫。
func TestRerun_DeletedProfile422(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	mustProfile(t, r, "standard", nil)
	it := ingestRun(t, r, "standard")
	if err := r.p.DeleteProfile(ctx, "standard"); err != nil {
		t.Fatal(err)
	}
	before, _ := r.st.Read().StageRunsByItem(ctx, it.ID)

	_, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize})
	var inv *InvalidError
	if !errors.As(err, &inv) || len(inv.Fields) != 1 || inv.Fields[0].Path != "profile" {
		t.Fatalf("err = %v，預期 path 為 profile 的 InvalidError", err)
	}
	if after, _ := r.st.Read().StageRunsByItem(ctx, it.ID); len(after) != len(before) {
		t.Errorf("422 之後 stage run %d → %d", len(before), len(after))
	}
	// body 指定存在的處理方案就可以重跑，而且改記那個處理方案。
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize, Profile: "default"}); err != nil {
		t.Fatalf("改用 default：%v", err)
	}
	if cur, _ := r.st.Read().ItemByID(ctx, it.ID); cur.ProfileID != "default" {
		t.Errorf("profile_id = %q，預期 default", cur.ProfileID)
	}
}

// 重跑指定 profile：改用它、記回影音，之後不帶 profile 的重跑沿用它；不存在的是 422 而且不寫入。
func TestRerun_ProfileUpdatesItem(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	mustProfile(t, r, "mixed", timingCfg("forbid"))
	it := ingestRun(t, r, "")
	if it.ProfileID != "default" {
		t.Fatalf("新增時的處理方案 = %q", it.ProfileID)
	}

	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize, Profile: "nope"}); err == nil {
		t.Fatal("不存在的 profile 應失敗")
	} else if cur, _ := r.st.Read().ItemByID(ctx, it.ID); cur.ProfileID != "default" {
		t.Errorf("失敗的重跑不該改動影音：%q", cur.ProfileID)
	}

	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize, Profile: "mixed"}); err != nil {
		t.Fatal(err)
	}
	cur, _ := r.st.Read().ItemByID(ctx, it.ID)
	if cur.ProfileID != "mixed" || cur.ProfileVersion != 1 {
		t.Fatalf("重跑之後 = %q v%d，預期 mixed v1", cur.ProfileID, cur.ProfileVersion)
	}
	r.run()
	// 之後不帶 profile 的重跑仍用 mixed（PRM-R5：先重跑 correct，再重跑 translate 時仍是同一個處理方案）。
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageTranslate}); err != nil {
		t.Fatal(err)
	}
	if l := layersOf(t, r, it.ID, StageTranslate); l.Profile.Name != "mixed" {
		t.Errorf("沿用的處理方案 = %q，預期 mixed", l.Profile.Name)
	}
}

// 重跑層疊在影音層之上，並帶上影音目前的頻道層。
func TestRerun_RunOverridesAndChannelLayer(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.ChannelID = "UCchan"
	if _, err := r.p.PatchChannelOverrides(ctx, "youtube:UCchan", timingCfg("forbid")); err != nil {
		t.Fatal(err)
	}
	it := ingestRun(t, r, "")
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize}); err != nil {
		t.Fatal(err)
	}
	if got := splitTimingOf(t, r, it.ID, StageNormalize); got != "forbid" {
		t.Errorf("頻道層 = %v，預期 forbid", got)
	}
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize, Overrides: timingCfg("free")}); err != nil {
		t.Fatal(err)
	}
	l := layersOf(t, r, it.ID, StageNormalize)
	if got := splitTimingOf(t, r, it.ID, StageNormalize); got != "free" || l.Channel.ID == "" || l.Run["timing"] == nil {
		t.Errorf("split_timing = %v、layers = %+v，預期重跑層優先、仍有頻道層", got, l)
	}
}

// D13：used_by 依序是處理方案名稱（字母排序、只看目前版本）、settings、channel:<platform>:<external_id>。
func TestPromptUsers_ProfilesSettingsChannels(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	stage := func(prompt string) map[string]any {
		// executor 寫 agent：處理方案中寫出來的 api 步驟必須有 provider 與 model（PRF-R5）。
		return map[string]any{"stages": map[string]any{"correct": map[string]any{"executor": "agent", "prompt": prompt}}}
	}
	mustProfile(t, r, "zeta", stage("my-correct@2"))
	mustProfile(t, r, "alpha", stage("my-correct"))
	mustProfile(t, r, "unrelated", stage("other"))
	mustProfile(t, r, "was-using", stage("my-correct@default"))
	// 只看目前版本：改成引用別的之後就不算。
	if _, err := r.p.PatchProfile(ctx, "was-using", stage("other")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.SetSettings(ctx, "stages", stage("my-correct@default")["stages"].(map[string]any)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.PatchChannelOverrides(ctx, "youtube:UC1", stage("my-correct")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.PatchChannelOverrides(ctx, "bilibili:100", stage("other")); err != nil {
		t.Fatal(err)
	}

	got, err := r.p.PromptUsers(ctx, "my-correct")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "zeta", "settings", "channel:youtube:UC1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("used_by = %v，預期 %v", got, want)
	}
	if none, err := r.p.PromptUsers(ctx, "nobody"); err != nil || len(none) != 0 {
		t.Errorf("沒人引用 = %v, %v", none, err)
	}
}

// 可以 PUT 的全域區段是 D16 列出的那些；timing 存入後讀得回來，而且併進新影音的生效設定；沒有的區段仍是 501。
func TestSettings_TimingSection(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if got, err := r.p.GetSettings(ctx, "timing"); err != nil || len(got) != 0 {
		t.Fatalf("沒存過的 timing = %v, %v，預期 {}", got, err)
	}
	got, err := r.p.SetSettings(ctx, "timing", map[string]any{"split_timing": "forbid"})
	if err != nil || got["split_timing"] != "forbid" {
		t.Fatalf("SetSettings = %v, %v", got, err)
	}
	if back, err := r.p.GetSettings(ctx, "timing"); err != nil || !reflect.DeepEqual(back, got) {
		t.Errorf("GetSettings = %v, %v", back, err)
	}
	for _, section := range globalSections {
		if _, err := r.p.GetSettings(ctx, section); err != nil {
			t.Errorf("GetSettings(%s)：%v", section, err)
		}
	}
	// stages 有內建預設（executor 與 prompt），讀得到。
	if st, _ := r.p.GetSettings(ctx, "stages"); st["correct"] == nil {
		t.Errorf("stages 應含內建預設：%v", st)
	}
	var ni *NotImplementedError
	for _, section := range []string{"outputs", "glossary", "budget"} {
		if _, err := r.p.SetSettings(ctx, section, map[string]any{}); !errors.As(err, &ni) {
			t.Errorf("SetSettings(%s) = %v，預期 *NotImplementedError", section, err)
		}
	}

	res, err := r.p.Ingest(ctx, IngestRequest{URL: watch})
	if err != nil {
		t.Fatal(err)
	}
	if got := splitTimingOf(t, r, res[0].Item.ID, StageTranscript); got != "forbid" {
		t.Errorf("新影音的 split_timing = %v，預期 forbid（全域）", got)
	}
	if l := layersOf(t, r, res[0].Item.ID, StageTranscript); l.Global["timing"] == nil {
		t.Errorf("layers.global 應記下 timing：%+v", l.Global)
	}
}
