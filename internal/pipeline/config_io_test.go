package pipeline

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/configio"
)

// exportYAML 匯出 r 目前的設定，回傳 YAML。
func exportYAML(t *testing.T, r *rig) string {
	t.Helper()
	doc, err := r.p.ExportConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out, err := configio.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// importFail 匯入 yaml，預期失敗，回傳 *InvalidError。
func importFail(t *testing.T, r *rig, yaml string) *InvalidError {
	t.Helper()
	_, err := r.p.ImportConfig(context.Background(), []byte(yaml))
	var inv *InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("ImportConfig 應回傳 *InvalidError，收到 %v", err)
	}
	return inv
}

func invPaths(inv *InvalidError) []string {
	out := make([]string, len(inv.Fields))
	for i, f := range inv.Fields {
		out[i] = f.Path
	}
	return out
}

// snapshotProfiles 記下所有處理方案目前的版本、內容雜湊與預設處理方案，用來確認「完全沒有改變」。
func snapshotProfiles(t *testing.T, r *rig) map[string]any {
	t.Helper()
	ctx := context.Background()
	list, err := r.p.Profiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	for _, p := range list {
		vs, err := r.p.ProfileVersions(ctx, p.Name)
		if err != nil {
			t.Fatal(err)
		}
		out[p.Name] = []any{p.Version, p.SHA256, len(vs), p.IsDefault}
	}
	return out
}

const importOK = `
default_profile: standard
profiles:
  standard:
    language: { targets: [zh-Hant-TW] }
    timing: { split_timing: forbid }
  fast:
    glossary: [a, b]
`

// 沒有任何設定時匯出是空的；內建的 default 不匯出；沒有明確設定預設處理方案就沒有 default_profile。
func TestConfigExport_EmptyAndBuiltinExcluded(t *testing.T) {
	r := newRig(t)
	if got := exportYAML(t, r); got != "profiles: {}\n" {
		t.Errorf("export = %q", got)
	}
	// 修改內建 default 的內容（PATCH 允許）也不會被匯出：內建處理方案不屬於匯出的範圍。
	if _, err := r.p.PatchProfile(context.Background(), "default", timingCfg("free")); err != nil {
		t.Fatal(err)
	}
	if got := exportYAML(t, r); got != "profiles: {}\n" {
		t.Errorf("內建的 default 不應匯出：%q", got)
	}
}

// 往返：匯出的檔案匯入到另一個空的實例之後，再匯出得到同樣的位元組；匯入原本的實例是冪等的。
func TestConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newRig(t)
	mustProfile(t, src, "standard", map[string]any{
		"language": map[string]any{"targets": []any{"zh-Hant-TW", "en"}},
		"timing":   map[string]any{"split_timing": "forbid"},
		"glossary": []any{"Kubernetes", "true", "2024-01-01", "a: b"},
		"stages":   map[string]any{"correct": map[string]any{"provider": "anthropic", "model": "m", "min_similarity": 0.6}},
	})
	mustProfile(t, src, "fast", timingCfg("free"))
	if _, err := src.p.SetDefaultProfile(ctx, "standard"); err != nil {
		t.Fatal(err)
	}
	yaml1 := exportYAML(t, src)
	if !strings.Contains(yaml1, "default_profile: standard") || strings.Contains(yaml1, "name: default") {
		t.Fatalf("export = \n%s", yaml1)
	}

	dst := newRig(t)
	res, err := dst.p.ImportConfig(ctx, []byte(yaml1))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Created, []string{"fast", "standard"}) || len(res.Updated) != 0 || len(res.Unchanged) != 0 || !res.DefaultChanged || res.DefaultProfile != "standard" {
		t.Errorf("result = %+v", res)
	}
	if yaml2 := exportYAML(t, dst); yaml2 != yaml1 {
		t.Errorf("匯入再匯出的內容不同：\n%s\n---\n%s", yaml1, yaml2)
	}
	// 儲存的內容（含雜湊）也相同，不只是輸出的文字。
	for _, name := range []string{"standard", "fast"} {
		a, _ := src.p.Profile(ctx, name)
		b, _ := dst.p.Profile(ctx, name)
		if a.SHA256 != b.SHA256 || !reflect.DeepEqual(a.Config, b.Config) {
			t.Errorf("%s 不同：\n%+v\n%+v", name, a, b)
		}
	}
	if def, _ := dst.p.DefaultProfileName(ctx); def != "standard" {
		t.Errorf("default = %q", def)
	}

	// 同一份檔案再匯入一次：全部是未變更，沒有新版本。
	before := snapshotProfiles(t, dst)
	res, err = dst.p.ImportConfig(ctx, []byte(yaml1))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 0 || len(res.Updated) != 0 || !reflect.DeepEqual(res.Unchanged, []string{"fast", "standard"}) || res.DefaultChanged {
		t.Errorf("第二次 result = %+v", res)
	}
	if after := snapshotProfiles(t, dst); !reflect.DeepEqual(before, after) {
		t.Errorf("重複匯入改變了資料：\n%v\n%v", before, after)
	}
}

// 取代同名的處理方案：建立新版本（PRF-R3），舊版本保留；影音綁定的是新增時的版本，不受影響；
// 檔案沒有提到的處理方案維持原樣（匯入不刪除）。
func TestConfigImport_ReplacesAndKeepsOthers(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	mustProfile(t, r, "standard", timingCfg("forbid"))
	mustProfile(t, r, "untouched", timingCfg("free"))
	itemID := r.ingestWith(t, "standard")

	res, err := r.p.ImportConfig(ctx, []byte("profiles:\n  standard: {timing: {split_timing: free}}\n  brand-new: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Created, []string{"brand-new"}) || !reflect.DeepEqual(res.Updated, []string{"standard"}) || len(res.Unchanged) != 0 || res.DefaultProfile != "" || res.DefaultChanged {
		t.Errorf("result = %+v", res)
	}
	pf, _ := r.p.Profile(ctx, "standard")
	if pf.Version != 2 || !reflect.DeepEqual(pf.Config, timingCfg("free")) {
		t.Errorf("standard = %+v，預期 v2、split_timing free", pf)
	}
	vs, _ := r.p.ProfileVersions(ctx, "standard")
	if len(vs) != 2 || !reflect.DeepEqual(vs[0].Config, timingCfg("forbid")) {
		t.Errorf("舊版本沒有保留：%+v", vs)
	}
	if _, err := r.p.Profile(ctx, "untouched"); err != nil {
		t.Errorf("檔案沒有提到的處理方案被刪掉了：%v", err)
	}
	if got := splitTimingOf(t, r, itemID, StageTranscript); got != "forbid" {
		t.Errorf("已新增的影音用到 %v，預期維持新增當時的 forbid", got)
	}
	// 沒有 default_profile 的檔案不改預設。
	if def, _ := r.p.DefaultProfileName(ctx); def != "default" {
		t.Errorf("預設處理方案被改成 %q", def)
	}
}

// ingestWith 用指定的處理方案新增 watch，回傳影音 ID。
func (r *rig) ingestWith(t *testing.T, profile string) string {
	t.Helper()
	res, err := r.p.Ingest(context.Background(), IngestRequest{URL: watch, Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	return res[0].Item.ID
}

// 場景「An invalid config import changes nothing」的單元版：有一個處理方案不正確，整份都不套用，
// 錯誤路徑有 profiles.<名稱> 前綴；合法的新處理方案、合法的更新、預設處理方案都沒有動。
func TestConfigImport_PartialErrorsChangeNothing(t *testing.T) {
	r := newRig(t)
	mustProfile(t, r, "standard", map[string]any{"language": map[string]any{"targets": []any{"zh-Hant-TW"}}})
	before := snapshotProfiles(t, r)
	exportBefore := exportYAML(t, r)

	inv := importFail(t, r, `
default_profile: good
profiles:
  good: {timing: {split_timing: free}}
  standard: { language: { targets: [en] } }
  broken: { language: { targets: "not-a-list" } }
`)
	if got := invPaths(inv); !reflect.DeepEqual(got, []string{"profiles.broken.language.targets"}) {
		t.Errorf("錯誤路徑 = %v", got)
	}
	if !strings.Contains(inv.Msg, "沒有套用") {
		t.Errorf("訊息要說明沒有套用任何設定：%q", inv.Msg)
	}
	if after := snapshotProfiles(t, r); !reflect.DeepEqual(before, after) {
		t.Errorf("資料被改變了：\n%v\n%v", before, after)
	}
	if got := exportYAML(t, r); got != exportBefore {
		t.Errorf("匯出內容被改變了：\n%s", got)
	}
}

// 每個錯誤都列出來（不是第一個就停），而且依名稱排序、順序固定。
func TestConfigImport_ReportsEveryError(t *testing.T) {
	r := newRig(t)
	yaml := `
unknown_top: 1
default_profile: nowhere
profiles:
  zeta: { timing: { split_timing: bogus } }
  alpha: { language: { targets: [] }, nope: 1 }
  Bad_Name: {}
  default: {}
  ok: {}
`
	var first []string
	for i := 0; i < 3; i++ {
		inv := importFail(t, r, yaml)
		got := invPaths(inv)
		if i == 0 {
			first = got
		} else if !reflect.DeepEqual(got, first) {
			t.Fatalf("錯誤順序不固定：%v 與 %v", first, got)
		}
	}
	want := []string{
		"unknown_top", // Parse：不認識的頂層鍵
		"profiles",    // 名稱 Bad_Name 不正確（路徑只能放 profiles）
		"profiles.alpha.language.targets", "profiles.alpha.nope",
		"profiles.default",
		"profiles.zeta.timing.split_timing",
		"default_profile",
	}
	if !reflect.DeepEqual(first, want) {
		t.Errorf("錯誤路徑：\n got %v\nwant %v", first, want)
	}
	if got := snapshotProfiles(t, r); len(got) != 1 {
		t.Errorf("資料被改變了：%v", got)
	}
}

func TestConfigImport_InvalidInputs(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string // 預期的錯誤路徑
		msg      string   // 第一個錯誤的訊息要含的字
	}{
		{"YAML 語法錯誤", "profiles: [\n", []string{""}, "YAML 格式不正確"},
		{"空檔案", "", []string{""}, "空的"},
		{"不認識的頂層鍵", "profiles: {}\nsecrets: {x: y}\n", []string{"secrets"}, "不認識的頂層鍵"},
		{"不覆蓋內建", "profiles:\n  default: {timing: {split_timing: free}}\n", []string{"profiles.default"}, "內建"},
		{"default_profile 不存在", "default_profile: ghost\nprofiles: {}\n", []string{"default_profile"}, "ghost"},
		{"名稱不合規則", "profiles:\n  'A B': {}\n", []string{"profiles"}, `"A B"`},
		{"名稱含控制字元", "profiles:\n  \"a\\x1b[31m\": {}\n", []string{"profiles"}, `\x1b`},
		{"處理方案是 null", "profiles:\n  p:\n", []string{"profiles.p"}, "物件"},
		{"未知欄位", "profiles:\n  p: {nope: 1}\n", []string{"profiles.p.nope"}, "不認識"},
		{"executor api 缺 provider", "profiles:\n  p: {stages: {correct: {executor: api}}}\n", []string{"profiles.p.stages.correct.provider", "profiles.p.stages.correct.model"}, "provider"},
		{"超過大小上限", "profiles: {}\n" + strings.Repeat("#", configio.MaxBytes), nil, "太大"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			before := snapshotProfiles(t, r)
			inv := importFail(t, r, c.in)
			if c.want != nil && !reflect.DeepEqual(invPaths(inv), c.want) {
				t.Errorf("錯誤路徑 = %v，預期 %v", invPaths(inv), c.want)
			}
			if len(inv.Fields) == 0 || !strings.Contains(inv.Fields[0].Message, c.msg) {
				t.Errorf("第一個錯誤 = %+v，預期訊息含 %q", inv.Fields, c.msg)
			}
			if after := snapshotProfiles(t, r); !reflect.DeepEqual(before, after) {
				t.Errorf("資料被改變了")
			}
		})
	}
}

// 引用不存在的 prompt：和 API 一樣的檢查，錯誤路徑帶前綴。
func TestConfigImport_UnknownPromptReference(t *testing.T) {
	r := newRig(t, func(d *Deps) { d.Prompts = &fakePrompts{missing: map[string]bool{"gone": true}} })
	inv := importFail(t, r, "profiles:\n  p: {stages: {correct: {provider: anthropic, model: m, prompt: gone}}}\n")
	if got := invPaths(inv); !reflect.DeepEqual(got, []string{"profiles.p.stages.correct.prompt"}) {
		t.Errorf("路徑 = %v", got)
	}
}

// default_profile 的各種合法來源：檔案中新建的、本來就存在的、內建的 default。
func TestConfigImport_DefaultProfile(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	mustProfile(t, r, "existing", nil)

	res, err := r.p.ImportConfig(ctx, []byte("default_profile: existing\nprofiles: {}\n"))
	if err != nil || !res.DefaultChanged {
		t.Fatalf("existing：%+v, %v", res, err)
	}
	if def, _ := r.p.DefaultProfileName(ctx); def != "existing" {
		t.Errorf("default = %q", def)
	}
	res, err = r.p.ImportConfig(ctx, []byte("default_profile: existing\nprofiles: {}\n"))
	if err != nil || res.DefaultChanged {
		t.Errorf("已經是預設：%+v, %v", res, err)
	}
	res, err = r.p.ImportConfig(ctx, []byte("default_profile: default\nprofiles: {}\n"))
	if err != nil || !res.DefaultChanged {
		t.Fatalf("改回內建 default：%+v, %v", res, err)
	}
	if def, _ := r.p.DefaultProfileName(ctx); def != "default" {
		t.Errorf("default = %q", def)
	}
	// 檔案中新建、同時設為預設。
	res, err = r.p.ImportConfig(ctx, []byte("default_profile: fresh\nprofiles: {fresh: {}}\n"))
	if err != nil || !res.DefaultChanged || !reflect.DeepEqual(res.Created, []string{"fresh"}) {
		t.Fatalf("fresh：%+v, %v", res, err)
	}
	if def, _ := r.p.DefaultProfileName(ctx); def != "fresh" {
		t.Errorf("default = %q", def)
	}
}

// 交易失敗（寫入到一半出錯）也完全不變：第二個處理方案的寫入被 trigger 擋下，第一個也不會留下。
func TestConfigImport_TransactionFailureChangesNothing(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	mustProfile(t, r, "standard", timingCfg("forbid"))
	before := snapshotProfiles(t, r)
	exportBefore := exportYAML(t, r)

	// 依名稱排序：aaa（新建）、standard（更新）、zzz（新建）。zzz 的版本寫入失敗。
	if _, err := r.rawDB().Exec(`CREATE TRIGGER fail_zzz BEFORE INSERT ON profile_versions WHEN NEW.name = 'zzz' BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := r.p.ImportConfig(ctx, []byte("default_profile: aaa\nprofiles:\n  aaa: {}\n  standard: {timing: {split_timing: free}}\n  zzz: {}\n"))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("預期寫入失敗，收到 %v", err)
	}
	var inv *InvalidError
	if errors.As(err, &inv) {
		t.Fatalf("寫入失敗不是驗證錯誤：%v", err)
	}
	if after := snapshotProfiles(t, r); !reflect.DeepEqual(before, after) {
		t.Errorf("交易沒有回復：\n%v\n%v", before, after)
	}
	if got := exportYAML(t, r); got != exportBefore {
		t.Errorf("匯出內容被改變了：\n%s", got)
	}
	if _, err := r.p.Profile(ctx, "aaa"); !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("aaa 不該留下：%v", err)
	}
}

// export 不含 secrets：資料庫裡有 API key、cookie 與 token 時，輸出裡沒有它們的任何痕跡。
func TestConfigExport_NeverContainsSecrets(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	mustProfile(t, r, "standard", map[string]any{
		"stages": map[string]any{"correct": map[string]any{"provider": "anthropic", "model": "m"}},
	})
	wr := r.st.Write()
	if err := wr.UpsertSecret(ctx, "provider_api_key", "anthropic", []byte("nonce-SECRETNONCE"), []byte("cipher-SECRETKEY-sk-ant-123456")); err != nil {
		t.Fatal(err)
	}
	if err := wr.UpsertSecret(ctx, "cookie", "youtube", []byte("n"), []byte("SESSIONCOOKIEVALUE")); err != nil {
		t.Fatal(err)
	}
	if err := wr.SetSetting(ctx, "some.secret.setting", "SETTINGVALUE"); err != nil {
		t.Fatal(err)
	}
	out := exportYAML(t, r)
	for _, bad := range []string{"SECRET", "sk-ant", "SESSIONCOOKIE", "SETTINGVALUE", "cipher", "nonce", "api_key", "cookie", "token", "password"} {
		if strings.Contains(out, bad) {
			t.Errorf("匯出內容含有 %q：\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "provider: anthropic") {
		t.Errorf("provider 的引用名稱是設定，應該要匯出：\n%s", out)
	}
}

// 匯入不會寫入 secrets 之類的其他資料表，也不讀它們。
func TestConfigImport_OnlyTouchesProfileTables(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if err := r.st.Write().UpsertSecret(ctx, "cookie", "youtube", []byte("n"), []byte("c")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.ImportConfig(ctx, []byte(importOK)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.Read().Secret(ctx, "cookie", "youtube"); err != nil {
		t.Errorf("匯入影響了 secrets：%v", err)
	}
}
