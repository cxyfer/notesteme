package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/output"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// publishRig 是 publish 的測試環境：一部本機影片（lecture.mp4）加上一份 edited/en 字幕。
type publishRig struct {
	*localRig
	it    store.Item
	video string
}

func newPublishRig(t *testing.T) *publishRig {
	t.Helper()
	l := newLocalRig(t)
	video := l.file("lecture.mp4", "video")
	it := l.item(video)
	seedVariant(t, l.rig, it, subtitle.VariantEdited, "en", "We use Kafka.", "It is a log.")
	return &publishRig{localRig: l, it: it, video: video}
}

// publish 以 outputs（YAML 已解成的物件）執行一次 publish。
func (r *publishRig) publish(it store.Item, outs ...map[string]any) (result, error) {
	r.t.Helper()
	list := make([]any, len(outs))
	for i, o := range outs {
		list[i] = o
	}
	eff, err := BuildEffective(map[string]any{"outputs": list})
	if err != nil {
		r.t.Fatal(err)
	}
	return r.p.runPublish(context.Background(), it, eff)
}

func sidecarOut(extra ...any) map[string]any {
	m := map[string]any{"target": "sidecar", "formats": []any{"srt"}, "lang": []any{"en"}}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i].(string)] = extra[i+1]
	}
	return m
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func recordFor(t *testing.T, r *rig, it store.Item, path string) store.Output {
	t.Helper()
	o, err := r.st.Read().Output(context.Background(), it.ID, path)
	if err != nil {
		t.Fatalf("沒有 %s 的輸出紀錄：%v", path, err)
	}
	return o
}

// OUT-R10：sidecar 寫成 <影片檔名>.<lang>.<ext>，記錄 sha256。
func TestPublishSidecarNextToLocalVideo(t *testing.T) {
	r := newPublishRig(t)
	res, err := r.publish(r.it, sidecarOut())
	if err != nil || res.state.Status != store.StatusDone || res.task != taskDone {
		t.Fatalf("runPublish = %+v，%v", res, err)
	}
	want := filepath.Join(r.root, "lecture.en.srt")
	got := readText(t, want)
	if !strings.Contains(got, "We use Kafka.") || !strings.Contains(got, "00:00:00,000 --> 00:00:02,000") {
		t.Errorf("內容不是 SRT：%q", got)
	}
	if fi, _ := os.Stat(want); fi.Mode().Perm() != 0o644 {
		t.Errorf("權限 = %v", fi.Mode().Perm())
	}
	if o := recordFor(t, r.rig, r.it, want); o.SHA256 != sha(got) || o.Target != "sidecar" || o.Format != "srt" || o.Lang != "en" {
		t.Errorf("紀錄 = %+v", o)
	}
}

// OUT-R11、R12：沒人改過就覆蓋；使用者改過就另存為 (notesteme 日期)，日期用注入的時鐘。
func TestPublishNeverOverwritesAnEditedFile(t *testing.T) {
	r := newPublishRig(t)
	if _, err := r.publish(r.it, sidecarOut()); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(r.root, "lecture.en.srt")

	// 字幕內容改了、檔案沒人動：覆蓋。
	seedVariant(t, r.rig, r.it, subtitle.VariantEdited, "en", "Changed text.")
	if _, err := r.publish(r.it, sidecarOut()); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, target); !strings.Contains(got, "Changed text.") {
		t.Fatalf("沒改過的檔案應被覆蓋：%q", got)
	}

	// 使用者改過：原檔不動，另存新檔。
	if err := os.WriteFile(target, []byte("I fixed this by hand."), 0o644); err != nil {
		t.Fatal(err)
	}
	seedVariant(t, r.rig, r.it, subtitle.VariantEdited, "en", "Third version.")
	res, err := r.publish(r.it, sidecarOut())
	if err != nil || res.state.Status != store.StatusDone {
		t.Fatalf("另存應成功：%+v，%v", res, err)
	}
	if readText(t, target) != "I fixed this by hand." {
		t.Error("使用者改過的檔案被覆蓋了")
	}
	alt := filepath.Join(r.root, "lecture.en (notesteme 2026-11-12).srt")
	if got := readText(t, alt); !strings.Contains(got, "Third version.") {
		t.Errorf("另存的內容：%q", got)
	}
	recordFor(t, r.rig, r.it, alt)
}

// OUT-R12：skip 不寫入，步驟 blocked（needs_action）；其他沒被改過的輸出照常寫出。
func TestPublishSkipPolicyBlocksOnEditedFile(t *testing.T) {
	r := newPublishRig(t)
	target := filepath.Join(r.root, "lecture.en.srt")
	if err := os.WriteFile(target, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := r.publish(r.it, sidecarOut("on_user_change", "skip", "formats", []any{"srt", "vtt"}))
	var ce *errclass.Error
	if !errors.As(err, &ce) || ce.Class != errclass.ClassNeedsAction || len(ce.Actions) == 0 || !strings.Contains(ce.Message, target) {
		t.Fatalf("err = %v，預期 needs_action 並帶出被改過的檔案", err)
	}
	if readText(t, target) != "mine" {
		t.Error("skip 不該動到使用者的檔案")
	}
	if entries, _ := filepath.Glob(filepath.Join(r.root, "lecture.en (notesteme*")); len(entries) != 0 {
		t.Errorf("skip 不該另存：%v", entries)
	}
	if _, serr := os.Stat(filepath.Join(r.root, "lecture.en.vtt")); serr != nil {
		t.Errorf("沒被改過的 vtt 仍應寫出：%v", serr)
	}
	// 經 classify 之後是 blocked、原因 needs_action。
	res := r.p.classify(store.Task{}, err)
	if res.state.Status != store.StatusBlocked || res.state.BlockReason != "needs_action" {
		t.Errorf("classify = %+v", res.state)
	}
}

// OUT-R10：sidecar 只用於本機檔案；線上影音與上傳檔都略過，並記錄原因。
func TestPublishSidecarIsNotApplicableToOnlineAndUploadedItems(t *testing.T) {
	r := newPublishRig(t)
	online := r.ingest(watch, nil, "metadata")
	r.run()
	res, err := r.publish(online, sidecarOut())
	if err != nil || res.state.Status != store.StatusSkipped || res.state.BlockReason != SkipNotApplicable || res.task != taskDone {
		t.Fatalf("線上影音：%+v，%v", res, err)
	}
	up, _, err := r.st.Write().InsertItemIfAbsent(context.Background(), store.NewItem{
		Platform: "local", ExternalID: "talk.mp4", SourcePath: "uploads/x/talk.mp4", Origin: store.OriginUpload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := r.publish(up, sidecarOut()); err != nil || res.state.BlockReason != SkipNotApplicable {
		t.Fatalf("上傳檔：%+v，%v", res, err)
	}
	// 其他目標仍然執行：只有 sidecar 不適用時才略過整個步驟。
	dir := filepath.Join(r.root, "out")
	seedVariant(t, r.rig, r.it, subtitle.VariantEdited, "en", "x")
	res, err = r.publish(r.it, sidecarOut(), map[string]any{"target": "folder", "path": dir, "formats": []any{"txt"}})
	if err != nil || res.state.Status != store.StatusDone {
		t.Fatalf("混合目標：%+v，%v", res, err)
	}
}

// folder：檔名同下載（OUT-R4，標題已過濾 NTFS 不允許的字元）；資料夾不存在時建立。
func TestPublishFolderTarget(t *testing.T) {
	r := newPublishRig(t)
	if err := r.st.Write().UpdateItemMeta(context.Background(), r.it.ID, store.ItemMeta{Title: "Kafka: in 100 seconds?"}); err != nil {
		t.Fatal(err)
	}
	it, _ := r.st.Read().ItemByID(context.Background(), r.it.ID)
	dir := filepath.Join(r.root, "subs", "kafka")
	res, err := r.publish(it, map[string]any{"target": "folder", "path": dir, "formats": []any{"srt", "ass"}, "lang": []any{"en"}})
	if err != nil || res.state.Status != store.StatusDone {
		t.Fatalf("runPublish = %+v，%v", res, err)
	}
	for _, name := range []string{"Kafka in 100 seconds.en.srt", "Kafka in 100 seconds.en.ass"} {
		got := readText(t, filepath.Join(dir, name))
		if len(got) == 0 {
			t.Errorf("%s 是空的", name)
		}
		recordFor(t, r.rig, it, filepath.Join(dir, name))
	}
	if !strings.Contains(readText(t, filepath.Join(dir, "Kafka in 100 seconds.en.ass")), "[V4+ Styles]") {
		t.Error("ass 的內容不是 ASS")
	}
}

// bilingual-srt 與 srt 的副檔名相同，檔名要分開，不能互相覆蓋。
func TestPublishBilingualSRTHasItsOwnName(t *testing.T) {
	r := newPublishRig(t)
	it := r.ingest(watch, nil, "metadata") // 有完整的步驟紀錄，才能 seed corrected 與 translated
	r.run()
	it, _ = r.st.Read().ItemByID(context.Background(), it.ID) // 取回 metadata 寫入的標題
	seedVariant(t, r.rig, it, subtitle.VariantCorrected, "en", "We use Kafka.", "It is a log.")
	seedVariant(t, r.rig, it, subtitle.VariantTranslated, "zh-Hant-TW", "我們用 Kafka。", "它是日誌。")
	dir := filepath.Join(r.root, "out")
	res, err := r.publish(it, map[string]any{"target": "folder", "path": dir, "formats": []any{"srt", "bilingual-srt"}, "lang": []any{"zh-Hant-TW"}})
	if err != nil || res.state.Status != store.StatusDone {
		t.Fatalf("runPublish = %+v，%v", res, err)
	}
	mono := readText(t, filepath.Join(dir, "T.zh-Hant-TW.srt"))
	bi := readText(t, filepath.Join(dir, "T.zh-Hant-TW.bilingual.srt"))
	if mono == bi || !strings.Contains(bi, "We use Kafka.") || !strings.Contains(bi, "我們用 Kafka。") || strings.Contains(mono, "We use Kafka.") {
		t.Errorf("單語 %q，雙語 %q", mono, bi)
	}
}

// folder 的路徑要通過路徑防護：解析 symlink 與 ".." 之後不能跑出允許的目錄，而且什麼都不寫。
func TestPublishFolderPathMustStayInsideRoots(t *testing.T) {
	r := newPublishRig(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(r.root, "link")); err != nil {
		t.Skip("不能建立 symlink：", err)
	}
	cases := map[string]string{
		"絕對路徑在根目錄外":      outside,
		"用 .. 跑出去":       filepath.Join(r.root, "..", filepath.Base(outside)),
		"symlink 指到外面":   filepath.Join(r.root, "link"),
		"symlink 底下的子目錄": filepath.Join(r.root, "link", "sub"),
	}
	for name, path := range cases {
		_, err := r.publish(r.it, map[string]any{"target": "folder", "path": path})
		var ce *errclass.Error
		if !errors.As(err, &ce) || ce.Class != errclass.ClassPermanent || !strings.Contains(ce.Message, "不在允許的目錄") {
			t.Errorf("%s：err = %v，預期 permanent 的路徑錯誤", name, err)
		}
	}
	if es, _ := os.ReadDir(outside); len(es) != 0 {
		t.Errorf("根目錄外被寫入了：%v", es)
	}
	// 資料目錄本身與內部目錄不能當輸出資料夾（serve 的守衛包含 <data>/uploads）；inbox 這類使用者的子目錄可以。
	data, err := filepath.EvalSymlinks(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"": true, "uploads": true, "uploads/x/y": true, "artifacts": true, "media/z": true, "tmp": true, "backups": true,
		"inbox": false, "inbox/subs": false, "uploadsX": false, "exports": false,
	} {
		if got := r.p.insideDataInternals(filepath.Join(data, name)); got != want {
			t.Errorf("insideDataInternals(%q) = %v，預期 %v", name, got, want)
		}
	}
	// 守衛允許的 <data>/uploads 底下，folder 仍然被拒絕。
	_, err = r.p.folderDir(filepath.Join(data, "uploads", "x"))
	var ce *errclass.Error
	if !errors.As(err, &ce) || !strings.Contains(ce.Message, "資料目錄") {
		t.Errorf("uploads 底下：err = %v，預期資料目錄內部的錯誤", err)
	}
	// 相對路徑在驗證階段就會被拒絕（schema），執行時也不接受。
	if _, err := r.p.folderDir("rel/dir"); err == nil {
		t.Error("相對路徑應被拒絕")
	}
}

// vault 目標缺 vault id 或引用設定檔沒有的 vault：permanent，不是 panic（vault 的寫出見 vault_publish_test.go）。
func TestPublishVaultNeedsAConfiguredVault(t *testing.T) {
	r := newPublishRig(t)
	if _, err := r.publish(r.it, map[string]any{"target": "vault"}); err == nil || !strings.Contains(err.Error(), "必須指定 vault") {
		t.Errorf("err = %v", err)
	}
	_, err := r.publish(r.it, map[string]any{"target": "vault", "vault": "main"})
	var ce *errclass.Error
	if !errors.As(err, &ce) || ce.Class != errclass.ClassPermanent || !strings.Contains(ce.Message, "沒有這個 vault：main") {
		t.Errorf("err = %v", err)
	}
}

// 指定的語言沒有字幕：其他輸出照寫，步驟 done 並說明缺哪些語言。
func TestPublishMissingLanguageIsReported(t *testing.T) {
	r := newPublishRig(t)
	res, err := r.publish(r.it, sidecarOut("lang", []any{"en", "zh-Hant-TW"}))
	if err != nil || res.state.Status != store.StatusDone || !strings.Contains(res.state.Message, "zh-Hant-TW") {
		t.Fatalf("runPublish = %+v，%v", res, err)
	}
	if _, err := os.Stat(filepath.Join(r.root, "lecture.en.srt")); err != nil {
		t.Error("有字幕的語言仍應寫出")
	}
}

// 影片檔不見了：sidecar 不知道要放在哪裡，permanent 失敗，而不是猜一個位置。
func TestPublishSidecarNeedsTheVideoFile(t *testing.T) {
	r := newPublishRig(t)
	if err := os.Remove(r.video); err != nil {
		t.Fatal(err)
	}
	_, err := r.publish(r.it, sidecarOut())
	var ce *errclass.Error
	if !errors.As(err, &ce) || ce.Class != errclass.ClassPermanent {
		t.Errorf("err = %v", err)
	}
}

// 沒有輸出目標：略過（no_outputs）。
func TestPublishWithoutOutputsIsSkipped(t *testing.T) {
	r := newPublishRig(t)
	res, err := r.publish(r.it)
	if err != nil || res.state.Status != store.StatusSkipped || res.state.BlockReason != SkipNoOutputs {
		t.Errorf("runPublish = %+v，%v", res, err)
	}
}

// 經 Rerun 與 worker 的完整流程：publish 任務寫出檔案；之後檔案被改過、政策是 skip 時步驟是 blocked（needs_action），
// 重跑不會覆蓋，也不會留下新檔。
func TestPublishThroughRerunAndWorker(t *testing.T) {
	r := newLocalRig(t)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	if st := r.stage(it, StagePublish); st.Status != store.StatusSkipped || st.BlockReason != SkipNoOutputs {
		t.Fatalf("沒有 outputs 時 publish = %s（%s）", st.Status, st.BlockReason)
	}

	dir := filepath.Join(r.root, "subs")
	outs := func(policy string) map[string]any {
		return map[string]any{"outputs": []any{map[string]any{"target": "folder", "path": dir, "formats": []any{"srt"}, "lang": []any{"en"}, "on_user_change": policy}}}
	}
	rerun := func(policy string) store.StageRun {
		t.Helper()
		if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StagePublish, Overrides: outs(policy)}); err != nil {
			t.Fatal(err)
		}
		r.run()
		return latestStage(t, r.rig, it.ID, StagePublish)
	}
	if st := rerun(output.PolicySkip); st.Status != store.StatusDone {
		t.Fatalf("第一次發布 = %s（%s）", st.Status, st.Message)
	}
	list, err := r.p.Outputs(context.Background(), it.ID)
	if err != nil || len(list) != 1 || list[0].Target != "folder" || filepath.Dir(list[0].Path) != dir {
		t.Fatalf("Outputs = %+v，%v", list, err)
	}
	file := list[0].Path
	if st := rerun(output.PolicySkip); st.Status != store.StatusDone {
		t.Fatalf("檔案沒被改過，再次發布應成功：%s（%s）", st.Status, st.Message)
	}

	if err := os.WriteFile(file, []byte("edited by hand"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := rerun(output.PolicySkip)
	if st.Status != store.StatusBlocked || st.BlockReason != "needs_action" || !strings.Contains(st.Message, file) {
		t.Fatalf("被改過的檔案 + skip = %s（%s）%s", st.Status, st.BlockReason, st.Message)
	}
	if readText(t, file) != "edited by hand" {
		t.Error("被改過的檔案被覆蓋了")
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Errorf("skip 不該寫出其他檔案：%v", es)
	}
	// 改成 save_as_new：原檔不動，另存為 (notesteme 日期)。
	if st := rerun(output.PolicySaveAsNew); st.Status != store.StatusDone {
		t.Fatalf("save_as_new = %s（%s）", st.Status, st.Message)
	}
	if es, _ := os.ReadDir(dir); len(es) != 2 || readText(t, file) != "edited by hand" {
		t.Errorf("save_as_new 應另存一個檔案並保留原檔：%v", es)
	}
}
