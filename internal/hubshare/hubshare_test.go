package hubshare

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/bundle"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubrepo"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

func cuesJSON(t *testing.T, variant, lang string) []byte {
	t.Helper()
	var buf bytes.Buffer
	err := subtitle.RenderCuesJSON(&buf, subtitle.Transcript{Lang: lang, Variant: subtitle.Variant(variant),
		Cues: []subtitle.Cue{{Index: 1, StartMS: 1000, EndMS: 3000, Text: "Hello.", Timing: subtitle.TimingSource}}})
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jpegBytes() []byte {
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil)
	return buf.Bytes()
}

func sample(t *testing.T) (Item, []Artifact) {
	pub := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	dur := 600
	it := Item{Platform: "youtube", ExternalID: "abcdefghijk", Title: "T", Availability: "public", OriginalLang: "en",
		ChannelExternalID: "UC1", ChannelTitle: "Chan", PublishedAt: &pub, DurationS: &dur}
	prov := json.RawMessage(`{"executor":"api","provider":"anthropic","model":"m","stage":"correct","impl_version":3,
		"chunks":[{"path":"/data/x"}],"prompt":{"id":"p","version":2,"sha256":"x","body":"secret"},
		"tool_versions":{"notesteme":"1.0","yt-dlp":"/usr/bin/yt-dlp"},"imported_from":"/media/a.srt","params_sha256":"abc",
		"cookie_required":false,"model_verified":true,"source":"youtube-manual","track":{"lang":"en","kind":"manual","path":"/x"}}`)
	return it, []Artifact{
		{Kind: "transcript", Variant: "corrected", Lang: "en", Data: cuesJSON(t, "corrected", "en"), Provenance: prov,
			ParentsSHA256: []string{strings.Repeat("a", 64), "not-a-sha"}},
		{Kind: "note", Lang: "en", Data: []byte("## Note\n"), Provenance: json.RawMessage(`{"executor":"api"}`)},
		{Kind: "chapters", Lang: "en", Data: []byte(`{"chapters":[]}`), Provenance: json.RawMessage(`{}`),
			HubOrigin: &HubOrigin{VersionID: "v1", SHA256: strings.Repeat("b", 64)}},
		{Kind: "frames", Data: []byte(`{"frames":[{"n":1}]}`), Provenance: json.RawMessage(`{}`), Images: []Image{{N: 1, Data: jpegBytes()}}},
	}
}

func TestBuildPassesTheHubValidators(t *testing.T) {
	it, arts := sample(t)
	raw, err := Build(it, arts, Options{License: "CC-BY-4.0", AppVersion: "1.2.3", Now: time.Unix(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := bundle.Read(raw, bundle.Options{InstanceIDOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.ValidateContents(); err != nil {
		t.Fatal(err)
	}
	var provs []hubrepo.Provenance
	for _, a := range b.Manifest.Artifacts {
		_, p := hubrepo.FilterProvenance(a.Provenance)
		provs = append(provs, p)
	}
	if e := hubrepo.CheckShareable(b, provs, hubconfig.DefaultLicenses); e != nil {
		t.Fatalf("CheckShareable: %v", e)
	}
	if e := hubrepo.CheckProvenanceSize(b); e != nil {
		t.Fatal(e)
	}
	if b.Manifest.CreatedBy.InstanceID != "" || b.Manifest.CreatedBy.AppVersion != "1.2.3" || b.Manifest.License != "CC-BY-4.0" {
		t.Errorf("manifest = %+v", b.Manifest)
	}
	if len(hubrepo.ImagesOf(b, b.Manifest.Artifacts[3])) != 1 {
		t.Error("畫面圖片應該屬於 frames 產出")
	}
	if !strings.Contains(string(b.ManifestJSON), `"hub_origin"`) {
		t.Error("hub_origin 應該寫進來源紀錄")
	}
}

func TestBuildLeavesOutLocalData(t *testing.T) {
	it, arts := sample(t)
	raw, err := Build(it, arts, Options{License: "CC-BY-4.0", AppVersion: "1", Now: time.Unix(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := bundle.Read(raw, bundle.Options{InstanceIDOptional: true})
	m := string(b.ManifestJSON)
	for _, bad := range []string{"/data", "/media", "/usr/bin", "instance_id", "chunks", "imported_from", "secret", `"stage"`, "impl_version"} {
		if strings.Contains(m, bad) {
			t.Errorf("manifest 不應該含 %q：\n%s", bad, m)
		}
	}
	var mf struct {
		Artifacts []struct {
			Provenance map[string]any `json:"provenance"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(b.ManifestJSON, &mf); err != nil {
		t.Fatal(err)
	}
	p0 := mf.Artifacts[0].Provenance
	if ps, _ := p0["parents_sha256"].([]any); len(ps) != 1 {
		t.Errorf("parents_sha256 = %v，不合法的值要丟掉", p0["parents_sha256"])
	}
	if tv, _ := p0["tool_versions"].(map[string]any); tv["yt-dlp"] != nil || tv["notesteme"] != "1.0" {
		t.Errorf("tool_versions = %v", tv)
	}
	for _, a := range mf.Artifacts[1:] {
		if ps, ok := a.Provenance["parents_sha256"].([]any); !ok || len(ps) != 0 {
			t.Errorf("沒有上游時 parents_sha256 要是空陣列：%v", a.Provenance)
		}
	}
}

func TestBuildNeedsArtifacts(t *testing.T) {
	if _, err := Build(Item{}, nil, Options{}); err != ErrNoArtifacts {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildRejectsInvalidContent(t *testing.T) {
	it, _ := sample(t)
	_, err := Build(it, []Artifact{{Kind: "transcript", Variant: "corrected", Lang: "en", Data: []byte("not json")}},
		Options{License: "CC-BY-4.0", AppVersion: "1", Now: time.Unix(0, 0)})
	if err == nil {
		t.Fatal("內容不是 cues JSON 時要失敗")
	}
}

func TestFilterProvenance(t *testing.T) {
	got := FilterProvenance(json.RawMessage(`{"model":"`+strings.Repeat("x", 600)+`","source":"/etc/passwd","executor":"a\nb","provider":"p"}`), nil, &HubOrigin{VersionID: "v", SHA256: "bad"})
	if _, ok := got["model"]; ok {
		t.Error("太長的值要丟掉")
	}
	if _, ok := got["source"]; ok {
		t.Error("像路徑的值要丟掉")
	}
	if _, ok := got["executor"]; ok {
		t.Error("含控制字元的值要丟掉")
	}
	if _, ok := got["hub_origin"]; ok {
		t.Error("sha256 不合法的 hub_origin 要丟掉")
	}
	if got["provider"] != "p" {
		t.Errorf("got = %v", got)
	}
	if got := FilterProvenance(json.RawMessage(`not json`), nil, nil); len(got) != 1 {
		t.Errorf("不是物件時只有 parents_sha256：%v", got)
	}
}

func TestFormatAndLicenses(t *testing.T) {
	if Format("transcript") != bundle.CuesFormat || Format("note") != "markdown" || Format("chapters") != "json" {
		t.Error("Format")
	}
	if !slices.Equal(Licenses, hubconfig.DefaultLicenses) {
		t.Error("Client 與 Hub 的預設授權應該相同")
	}
}
