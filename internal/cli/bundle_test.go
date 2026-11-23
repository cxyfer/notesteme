package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/bundle"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// validBundle 產生一個只有一份 corrected 字幕的合法 bundle。
func validBundle(t *testing.T) []byte {
	t.Helper()
	var cues bytes.Buffer
	if err := subtitle.RenderCuesJSON(&cues, subtitle.Transcript{Lang: "en", Variant: subtitle.VariantCorrected,
		Cues: []subtitle.Cue{{Index: 1, StartMS: 0, EndMS: 1000, Text: "hi", Timing: subtitle.TimingSource}}}); err != nil {
		t.Fatal(err)
	}
	lang := "en"
	id := "0190a1b2-0000-7000-8000-0000000000a1"
	data, err := bundle.Write(bundle.OutManifest{
		Item: bundle.OutItem{Platform: "youtube", ExternalID: "abc123xxxxx", Title: "Bundled", Availability: "public"},
		Artifacts: []bundle.OutArtifact{{ID: id, Kind: "transcript", Variant: "corrected", Lang: &lang, Format: bundle.CuesFormat,
			Path: "artifacts/" + id + ".json", Provenance: json.RawMessage(`{"executor":"api"}`)}},
		CreatedBy: bundle.OutCreatedBy{InstanceID: "0190a1b2-0000-7000-8000-000000000001", AppVersion: "0.4.0"},
	}, []bundle.OutEntry{{Path: "artifacts/" + id + ".json", Data: cues.Bytes()}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestBundleImportExportCLI(t *testing.T) {
	env, _ := testEnv(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "in.notesteme.zip")
	if err := os.WriteFile(in, validBundle(t), 0o600); err != nil {
		t.Fatal(err)
	}
	// 以內容判斷：副檔名不是 .notesteme.zip 也當成 bundle。
	other := filepath.Join(dir, "in.bin")
	if err := os.WriteFile(other, validBundle(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if res, _, errOut := run(t, env, "import", other, "--item", "yt:x"); res.Code != 2 || !strings.Contains(errOut, "匯入 bundle 不能指定 --item。") {
		t.Fatalf("--item = %+v %s", res, errOut)
	}
	if res, _, errOut := run(t, env, "import", in, "--lang", "en"); res.Code != 2 || !strings.Contains(errOut, "--lang") {
		t.Fatalf("--lang = %+v %s", res, errOut)
	}
	res, out, errOut := run(t, env, "import", in, "--json")
	if res.Code != 0 || !strings.Contains(out, `"ref":"yt:abc123xxxxx"`) || !strings.Contains(out, `"imported":1`) {
		t.Fatalf("匯入 = %+v %s %s", res, out, errOut)
	}
	// 已存在：409 → 結束碼 1；--add-versions 略過相同內容。
	if res, _, errOut := run(t, env, "import", in); res.Code != 1 || !strings.Contains(errOut, "on_existing=add_versions") {
		t.Fatalf("重複 = %+v %s", res, errOut)
	}
	if res, out, errOut := run(t, env, "import", in, "--add-versions"); res.Code != 0 || !strings.Contains(out, "略過 1 個") {
		t.Fatalf("--add-versions = %+v %s %s", res, out, errOut)
	}
	// 內容不正確：422 → 結束碼 1。
	bad := filepath.Join(dir, "bad.notesteme.zip")
	if err := os.WriteFile(bad, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res, _, errOut := run(t, env, "import", bad); res.Code != 1 || res.ErrorCode != "bundle_invalid" || !strings.Contains(errOut, "無法以 zip 讀取") {
		t.Fatalf("不是 zip = %+v %s", res, errOut)
	}
	// 一般字幕檔仍要求 --item；--add-versions 只能用在 bundle。
	srt := filepath.Join(dir, "a.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,000\nhi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res, _, _ := run(t, env, "import", srt, "--add-versions", "--item", "yt:abc123xxxxx"); res.Code != 2 {
		t.Errorf("字幕加 --add-versions = %+v", res)
	}

	// 匯出：沒有 --out 時寫到目前目錄。
	t.Chdir(dir)
	if res, out, errOut := run(t, env, "export", "yt:abc123xxxxx", "--format", "bundle"); res.Code != 0 || !strings.Contains(out, "yt-abc123xxxxx.notesteme.zip") {
		t.Fatalf("匯出 = %+v %s %s", res, out, errOut)
	}
	data, err := os.ReadFile(filepath.Join(dir, "yt-abc123xxxxx.notesteme.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Read(data, bundle.Options{}); err != nil {
		t.Fatalf("匯出的 bundle：%v", err)
	}
	outPath := filepath.Join(dir, "x.zip")
	if res, _, errOut := run(t, env, "export", "yt:abc123xxxxx", "--format", "bundle", "--out", outPath); res.Code != 0 {
		t.Fatalf("--out = %+v %s", res, errOut)
	}
	if res, _, _ := run(t, env, "export", "yt:abc123xxxxx", "--format", "bundle", "--lang", "en"); res.Code != 2 {
		t.Errorf("bundle 加 --lang = %+v", res)
	}
	if res, _, _ := run(t, env, "export", "yt:nope0000000", "--format", "bundle"); res.Code != 1 {
		t.Errorf("不存在 = %+v", res)
	}
}

func TestBundleNameFrom(t *testing.T) {
	for cd, want := range map[string]string{
		`attachment; filename="yt-a.notesteme.zip"`:        "yt-a.notesteme.zip",
		`attachment; filename="../../etc/x.notesteme.zip"`: "x.notesteme.zip",
		`attachment; filename="evil.sh"`:                   "yt-fallback.notesteme.zip",
		``:                                                 "yt-fallback.notesteme.zip",
	} {
		if got := bundleNameFrom(cd, "yt:fallback"); got != want {
			t.Errorf("bundleNameFrom(%q) = %q，預期 %q", cd, got, want)
		}
	}
	if isBundleFile(filepath.Join(t.TempDir(), "missing")) {
		t.Error("不存在的檔案不是 bundle")
	}
}
