package hubrepo

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestFilterProvenanceKeepsOnlyTheAllowlist(t *testing.T) {
	raw := json.RawMessage(`{
		"executor":"api","provider":"groq","model":"whisper-v3","workdir":"/data/tmp/task-42",
		"config_snapshot":{"api_key":"sk-secret"},"cookies_used":true,
		"prompt":{"id":"p","version":3,"sha256":"abc","body":"secret prompt"},
		"track":{"lang":"en","kind":"manual","url":"https://x"},
		"tool_versions":{"yt-dlp":"2026.11.01","path":{"x":1}},
		"hub_origin":{"version_id":"0193a3b4-0000-7000-8000-000000000001","sha256":"` + strings.Repeat("a", 64) + `","url":"https://hub"},
		"parents_sha256":["` + strings.Repeat("b", 64) + `","../../etc",7],
		"model_verified":"yes","cookie_required":false,
		"imported_from":{"instance_id":"0193a3b4-0000-7000-8000-000000000009"}
	}`)
	out, p := FilterProvenance(raw)
	s := string(out)
	for _, bad := range []string{"workdir", "/data/tmp", "sk-secret", "cookies_used", "secret prompt", "https://", "instance_id", "../../etc", "yes"} {
		if strings.Contains(s, bad) {
			t.Errorf("過濾後仍有 %q：%s", bad, s)
		}
	}
	for _, good := range []string{`"provider":"groq"`, `"version":3`, `"lang":"en"`, `"yt-dlp":"2026.11.01"`, `"cookie_required":false`} {
		if !strings.Contains(s, good) {
			t.Errorf("過濾後少了 %s：%s", good, s)
		}
	}
	if p.Provider != "groq" || p.Model != "whisper-v3" || p.ModelVerified != nil || p.CookieRequired == nil || *p.CookieRequired {
		t.Errorf("Provenance = %+v", p)
	}
	if len(p.ParentsSHA256) != 1 || p.HubOrigin.VersionID == "" || p.HubOrigin.SHA256 == "" {
		t.Errorf("parents／hub_origin = %+v", p)
	}
	if out, _ := FilterProvenance(json.RawMessage(`[1]`)); string(out) != `{}` {
		t.Errorf("不是物件 = %s", out)
	}
	long := json.RawMessage(`{"model":"` + strings.Repeat("x", 600) + `"}`)
	if out, _ := FilterProvenance(long); strings.Contains(string(out), "xxx") {
		t.Errorf("太長的欄位要丟掉：%s", out)
	}
}

func TestStepModels(t *testing.T) {
	f := false
	cases := []struct {
		p          Provenance
		step, tr   string
		unverified bool
	}{
		{Provenance{Executor: "api", Provider: "openai", Model: "gpt-4.1"}, "openai/gpt-4.1", "openai/gpt-4.1", false},
		{Provenance{Executor: "agent", Model: "x", ModelReported: "claude-sonnet-x"}, "claude-sonnet-x", "claude-sonnet-x", true},
		{Provenance{Executor: "agent", Model: "x"}, "x", "x", true},
		{Provenance{Executor: "api", Source: "youtube-manual"}, "", "youtube-manual", false},
		{Provenance{Executor: "api", Model: "m", ModelVerified: &f}, "m", "m", true},
	}
	for _, c := range cases {
		if c.p.StepModel() != c.step || c.p.TranscribeModel() != c.tr || c.p.Unverified() != c.unverified {
			t.Errorf("%+v：%q %q %v", c.p, c.p.StepModel(), c.p.TranscribeModel(), c.p.Unverified())
		}
	}
}

func TestComputeModelsFollowsParents(t *testing.T) {
	src := Node{Kind: "transcript", Variant: "source", SHA256: "s", Prov: Provenance{Executor: "api", Provider: "groq", Model: "whisper-v3"}}
	norm := Node{Kind: "transcript", Variant: "normalized", SHA256: "n", Prov: Provenance{Executor: "api", ParentsSHA256: []string{"s"}}}
	fix := Node{Kind: "transcript", Variant: "corrected", SHA256: "c", Prov: Provenance{Executor: "agent", ModelReported: "claude", ParentsSHA256: []string{"n"}}}
	tr := Node{Kind: "transcript", Variant: "translated", SHA256: "t", Prov: Provenance{Executor: "api", Provider: "openai", Model: "gpt", ParentsSHA256: []string{"c"}}}
	ms := ComputeModels([]Node{tr, fix, norm, src}, nil, nil)
	want := []Models{
		{Transcribe: "groq/whisper-v3", Correct: "claude", Translate: "openai/gpt", Verified: false},
		{Transcribe: "groq/whisper-v3", Correct: "claude", Verified: false},
		{Transcribe: "groq/whisper-v3", Verified: true},
		{Transcribe: "groq/whisper-v3", Verified: true},
	}
	for i := range want {
		if ms[i] != want[i] {
			t.Errorf("%d：%+v，預期 %+v", i, ms[i], want[i])
		}
	}
	// 上游在 Hub 上（同一部影片已有的版本）。
	hub := func(sha string) (Models, bool) {
		if sha == "old" {
			return Models{Transcribe: "youtube-manual", Verified: false}, true
		}
		return Models{}, false
	}
	n := Node{Kind: "transcript", Variant: "corrected", SHA256: "x", Prov: Provenance{Executor: "api", Model: "m", ParentsSHA256: []string{"old", "missing"}}}
	if m := ComputeModels([]Node{n}, hub, nil)[0]; m.Transcribe != "youtube-manual" || m.Correct != "m" || m.Verified {
		t.Errorf("Hub 上游 = %+v", m)
	}
	// executor: hub 依 hub_origin 的版本；找不到時保守地是 false。
	h := Node{Kind: "transcript", Variant: "source", SHA256: "h", Prov: Provenance{Executor: "hub", HubOrigin: HubOrigin{VersionID: "v1"}}}
	if m := ComputeModels([]Node{h}, nil, func(id string) bool { return id == "v1" })[0]; !m.Verified {
		t.Errorf("已驗證的 hub_origin = %+v", m)
	}
	if m := ComputeModels([]Node{h}, nil, func(string) bool { return false })[0]; m.Verified {
		t.Errorf("未驗證的 hub_origin = %+v", m)
	}
	if m := ComputeModels([]Node{h}, nil, nil)[0]; m.Verified {
		t.Errorf("沒有 origin 查詢 = %+v", m)
	}
	// 循環的 parents 不會無限遞迴。
	a := Node{Kind: "transcript", Variant: "source", SHA256: "a", Prov: Provenance{ParentsSHA256: []string{"b"}}}
	b := Node{Kind: "transcript", Variant: "corrected", SHA256: "b", Prov: Provenance{Model: "m", ParentsSHA256: []string{"a"}}}
	_ = ComputeModels([]Node{a, b}, nil, nil)
}

func TestIsMedia(t *testing.T) {
	media := map[string][]byte{
		"artifacts/a.mp4":  []byte("x"),
		"artifacts/a.bin":  []byte("\x00\x00\x00\x18ftypmp42"),
		"artifacts/b.bin":  []byte("\x1a\x45\xdf\xa3...."),
		"artifacts/c.bin":  []byte("OggS...."),
		"artifacts/d.bin":  []byte("ID3\x04"),
		"artifacts/e.bin":  []byte("RIFF\x00\x00\x00\x00WAVE"),
		"artifacts/f.bin":  []byte{0xff, 0xfb, 0x90, 0x00},
		"artifacts/g.FLAC": []byte("x"),
	}
	for name, d := range media {
		if !IsMedia(name, d) {
			t.Errorf("%s 應該是媒體檔", name)
		}
	}
	notMedia := map[string][]byte{
		"artifacts/a.json": []byte(`{"cues":[]}`),
		"artifacts/a.jpg":  {0xff, 0xd8, 0xff, 0xe0},
		"artifacts/a.md":   []byte("## ftyp"),
		"artifacts/b.json": {},
	}
	for name, d := range notMedia {
		if IsMedia(name, d) {
			t.Errorf("%s 不應該是媒體檔", name)
		}
	}
}

func TestCheckRequestedVisibility(t *testing.T) {
	groups := func(g string) bool { return g == "translators" }
	cases := []struct {
		v, site, want string
	}{
		{"public", "authenticated", "可見度不能超過站台上限（authenticated）。"},
		{"public", "public", ""},
		{"authenticated", "authenticated", ""},
		{"private", "authenticated", ""},
		{"group:translators", "authenticated", ""},
		{"group:nope", "authenticated", "不認得的群組：nope。"},
		{"group:", "authenticated", "必須是"},
		{"everyone", "public", "必須是"},
	}
	for _, c := range cases {
		e := CheckRequested(c.v, c.site, groups)
		if c.want == "" {
			if e != nil {
				t.Errorf("%s：%v", c.v, e)
			}
			continue
		}
		if e == nil || e.Status != 422 || len(e.Errors) != 1 || e.Errors[0].Path != "visibility" || !strings.Contains(e.Errors[0].Message, c.want) {
			t.Errorf("%s／%s = %+v，預期 %q", c.v, c.site, e, c.want)
		}
	}
}

func TestCanSee(t *testing.T) {
	anon := Viewer{Anonymous: true}
	bob := Viewer{UserID: "bob", Groups: []string{"members"}}
	carol := Viewer{UserID: "carol", Groups: []string{"members", "translators"}}
	mod := Viewer{UserID: "mod", Moderator: true}
	alice := Viewer{UserID: "alice"}
	sub := func(vis, state string) Subject {
		return Subject{Visibility: vis, State: state, Uploaders: []string{"alice"}}
	}
	cases := []struct {
		s      Subject
		v      Viewer
		site   string
		expect bool
	}{
		{sub("public", "visible"), anon, "public", true},
		{sub("public", "visible"), anon, "authenticated", false}, // 站台上限
		{sub("authenticated", "visible"), anon, "public", false},
		{sub("authenticated", "visible"), bob, "authenticated", true},
		{sub("private", "visible"), bob, "authenticated", false},
		{sub("private", "visible"), alice, "authenticated", true},
		{sub("private", "visible"), mod, "authenticated", true},
		{sub("private", "visible"), Viewer{Anonymous: true, UserID: "alice"}, "public", false},
		{sub("group:translators", "visible"), bob, "authenticated", false},
		{sub("group:translators", "visible"), carol, "authenticated", true},
		{sub("group:translators", "visible"), anon, "public", false},
		{sub("public", "hidden"), bob, "public", false},
		{sub("public", "hidden"), alice, "public", true},
		{sub("public", "hidden"), mod, "public", true},
		{sub("public", "withdrawn"), alice, "public", false},
		{sub("public", "withdrawn"), mod, "public", false},
		{sub("public", "removed"), bob, "public", true}, // 由呼叫端回 410
		{sub("weird", "visible"), bob, "public", false},
	}
	for i, c := range cases {
		if got := CanSee(c.s, c.v, c.site); got != c.expect {
			t.Errorf("%d：%+v／%+v／%s = %v", i, c.s, c.v, c.site, got)
		}
	}
	if Effective("public", "authenticated") != "authenticated" || Effective("private", "public") != "private" {
		t.Error("Effective")
	}
}

// Listable 要和 CanSee 對 visible 版本的判斷完全一致（上傳者另外由 SQL 的 EXISTS 處理）：
// SQL 預先過濾多放行或少放行都會造成洩漏或漏列。
func TestListableAgreesWithCanSee(t *testing.T) {
	viewers := []Viewer{
		{Anonymous: true},
		{UserID: "u1"},
		{UserID: "u1", Groups: []string{"members", "team"}},
		{UserID: "u1", Moderator: true},
		{Anonymous: true, Moderator: true}, // 匿名不可能是 moderate：要當作匿名
	}
	visibilities := []string{"public", "authenticated", "private", "group:team", "group:other", "group:", "bogus", ""}
	for _, site := range []string{"public", "authenticated"} {
		for _, vw := range viewers {
			vis, all := Listable(vw, site)
			for _, v := range visibilities {
				want := CanSee(Subject{Visibility: v, State: StateVisible, Uploaders: []string{"someone-else"}}, vw, site)
				got := all || slices.Contains(vis, v)
				if got != want {
					t.Errorf("site=%s viewer=%+v visibility=%q：Listable=%v，CanSee=%v", site, vw, v, got, want)
				}
			}
		}
	}
}
