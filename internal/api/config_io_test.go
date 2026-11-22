package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/configio"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/validate"
)

type exportBody struct {
	Content        string   `json:"content"`
	Profiles       []string `json:"profiles"`
	DefaultProfile string   `json:"default_profile"`
}

type importBody struct {
	Created        []string `json:"created"`
	Updated        []string `json:"updated"`
	Unchanged      []string `json:"unchanged"`
	DefaultProfile string   `json:"default_profile"`
	DefaultChanged bool     `json:"default_changed"`
}

func importReq(content string) string {
	b, _ := json.Marshal(map[string]string{"content": content})
	return string(b)
}

// 用真正的 Pipeline 走完整條路：建立處理方案 → 匯出 → 匯入壞檔案（422、完全不變）→ 匯入好檔案。
func TestConfigIOAPI_EndToEnd(t *testing.T) {
	r := newItemsRigAPI(t, nil)
	if rec := r.owner("POST", "/api/profiles", `{"name":"standard","config":{"language":{"targets":["zh-Hant-TW"]}}}`); rec.Code != http.StatusCreated {
		t.Fatalf("建立處理方案 = %d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("POST", "/api/profiles/standard/default", ""); rec.Code != http.StatusOK {
		t.Fatalf("設為預設 = %d %s", rec.Code, rec.Body)
	}

	rec := r.owner("GET", "/api/config/export", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d %s", rec.Code, rec.Body)
	}
	exp := decode[exportBody](t, rec)
	if exp.DefaultProfile != "standard" || len(exp.Profiles) != 1 || exp.Profiles[0] != "standard" ||
		!strings.Contains(exp.Content, "zh-Hant-TW") || !strings.Contains(exp.Content, "default_profile: standard") {
		t.Errorf("export = %+v", exp)
	}

	// 壞檔案：422、code、逐欄位的 errors（帶 profiles.<名稱> 前綴），而且什麼都沒有改變。
	bad := "profiles:\n  standard: { language: { targets: [en] } }\n  broken: { language: { targets: \"not-a-list\" } }\n"
	rec = r.owner("POST", "/api/config/import", importReq(bad))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("壞檔案 = %d %s", rec.Code, rec.Body)
	}
	e := decodeError(t, rec.Body.Bytes())
	if e.Code != CodeConfigInvalid || len(e.Errors) != 1 || e.Errors[0].Path != "profiles.broken.language.targets" || !strings.Contains(e.Message, "沒有套用") {
		t.Errorf("error = %+v", e)
	}
	after := decode[exportBody](t, r.owner("GET", "/api/config/export", ""))
	if after.Content != exp.Content {
		t.Errorf("壞檔案改變了設定：\n%s\n---\n%s", exp.Content, after.Content)
	}

	// 好檔案：更新 standard、新增 other；JSON 輸出的清單不是 null。
	rec = r.owner("POST", "/api/config/import", importReq("profiles:\n  standard: {timing: {split_timing: free}}\n  other: {}\n"))
	if rec.Code != http.StatusOK {
		t.Fatalf("匯入 = %d %s", rec.Code, rec.Body)
	}
	got := decode[importBody](t, rec)
	if len(got.Created) != 1 || got.Created[0] != "other" || len(got.Updated) != 1 || got.Updated[0] != "standard" || got.Unchanged == nil || len(got.Unchanged) != 0 || got.DefaultChanged {
		t.Errorf("import = %+v", got)
	}
	if !strings.Contains(rec.Body.String(), `"unchanged":[]`) {
		t.Errorf("unchanged 應為 []：%s", rec.Body)
	}
	// 往返：匯出後原樣匯入，全部未變更。
	exp2 := decode[exportBody](t, r.owner("GET", "/api/config/export", ""))
	rec = r.owner("POST", "/api/config/import", importReq(exp2.Content))
	got = decode[importBody](t, rec)
	if rec.Code != http.StatusOK || len(got.Created)+len(got.Updated) != 0 || len(got.Unchanged) != 2 {
		t.Errorf("往返匯入 = %d %+v", rec.Code, got)
	}
}

// 超過上限：YAML 超過 1 MiB 回 422（錯誤碼同其他驗證錯誤），不是 413。
func TestConfigIOAPI_TooLarge(t *testing.T) {
	r := newItemsRigAPI(t, nil)
	rec := r.owner("POST", "/api/config/import", importReq("profiles: {}\n"+strings.Repeat("#", configio.MaxBytes)))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("= %d %.200s", rec.Code, rec.Body)
	}
	if e := decodeError(t, rec.Body.Bytes()); e.Code != CodeConfigInvalid || len(e.Errors) != 1 || !strings.Contains(e.Errors[0].Message, "太大") {
		t.Errorf("error = %+v", e)
	}
	// 請求本體超過 huma 的上限才是 413。
	rec = r.owner("POST", "/api/config/import", importReq(strings.Repeat("a", maxConfigImportBodyBytes+1)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("超大本體 = %d，預期 413", rec.Code)
	}
}

// 權限：匯出要 read、匯入要 admin；缺 scope 在讀本體之前回 403；沒有身分 401。
func TestConfigIOAPI_Scopes(t *testing.T) {
	var svc *auth.Service
	r := newItemsRigAPI(t, func(d *Deps) { svc = d.Auth })
	tok := func(scopes ...string) string {
		ct, err := svc.CreateToken(context.Background(), strings.Join(scopes, "-"), scopes, nil)
		if err != nil {
			t.Fatal(err)
		}
		return ct.Token
	}
	readTok, adminTok, ingestTok := tok(auth.ScopeRead), tok(auth.ScopeAdmin), tok(auth.ScopeIngest, auth.ScopeAgent)

	if rec := r.do("GET", "/api/config/export", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("沒有身分 export = %d", rec.Code)
	}
	if rec := r.do("POST", "/api/config/import", importReq("profiles: {}"), nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("沒有身分 import = %d", rec.Code)
	}
	for _, c := range []struct {
		method, path, body, token, scope string
		want                             int
	}{
		{"GET", "/api/config/export", "", readTok, "", 200},
		{"GET", "/api/config/export", "", adminTok, "read", 403},
		{"GET", "/api/config/export", "", ingestTok, "read", 403},
		{"POST", "/api/config/import", importReq("profiles: {}"), adminTok, "", 200},
		{"POST", "/api/config/import", importReq("profiles: {}"), readTok, "admin", 403},
		{"POST", "/api/config/import", importReq("profiles: {}"), ingestTok, "admin", 403},
		// 缺 scope 時連壞掉的本體也不會被讀：先 403，不是 400／422。
		{"POST", "/api/config/import", `{`, readTok, "admin", 403},
	} {
		rec := r.do(c.method, c.path, c.body, withBearer(c.token))
		if rec.Code != c.want {
			t.Errorf("%s %s = %d %s，預期 %d", c.method, c.path, rec.Code, rec.Body, c.want)
			continue
		}
		if c.want == 403 {
			if e := decodeError(t, rec.Body.Bytes()); e.Code != CodeForbidden || e.RequiredScope != c.scope {
				t.Errorf("%s %s 的錯誤 = %+v，預期 required_scope=%s", c.method, c.path, e, c.scope)
			}
		}
	}
	// token 即使沒有 admin 也不能用匯入蓋過內建處理方案之類：這裡只確認 read token 匯出的內容不含 secrets 欄位名稱。
	rec := r.do("GET", "/api/config/export", "", withBearer(readTok))
	for _, bad := range []string{"api_key", "cookie", "password", "token"} {
		if strings.Contains(rec.Body.String(), bad) {
			t.Errorf("匯出含有 %q：%s", bad, rec.Body)
		}
	}
}

// 沒有注入 Pipeline 是設定錯誤：500（不是 501，501 代表功能還沒做）。
func TestConfigIOAPI_NoPipeline(t *testing.T) {
	r := newItemsRigAPI(t, func(d *Deps) { d.Pipeline = nil })
	for _, c := range [][3]string{{"GET", "/api/config/export", ""}, {"POST", "/api/config/import", importReq("profiles: {}")}} {
		if rec := r.owner(c[0], c[1], c[2]); rec.Code != http.StatusInternalServerError {
			t.Errorf("%s %s = %d，預期 500", c[0], c[1], rec.Code)
		}
	}
}

// 假的 service：驗證 handler 對錯誤的對應，不依賴 pipeline 的實作。
type fakeConfigSvc struct {
	exportErr, importErr error
	got                  string
}

func (f *fakeConfigSvc) ExportConfig(context.Context) (*configio.Document, error) {
	if f.exportErr != nil {
		return nil, f.exportErr
	}
	return &configio.Document{DefaultProfile: "b", Profiles: map[string]map[string]any{"b": {}, "a": {}}}, nil
}

func (f *fakeConfigSvc) ImportConfig(_ context.Context, data []byte) (pipeline.ConfigImportResult, error) {
	f.got = string(data)
	return pipeline.ConfigImportResult{}, f.importErr
}

func TestConfigIOAPI_ErrorMapping(t *testing.T) {
	f := &fakeConfigSvc{}
	// 直接用 handler：不經過 router，驗證對應本身。
	h := &configAPI{rt: &routes{d: Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}, svc: f}
	ctx := context.WithValue(context.Background(), keyPrincipal, auth.Principal{Kind: "session"})

	out, err := h.export(ctx, nil)
	if err != nil || out.Body.DefaultProfile != "b" || strings.Join(out.Body.Profiles, ",") != "a,b" {
		t.Fatalf("export = %+v, %v", out, err)
	}
	f.exportErr = errors.New("db 壞了")
	if _, err := h.export(ctx, nil); err == nil || asError(err).Status != http.StatusInternalServerError || strings.Contains(asError(err).Message, "db 壞了") {
		t.Errorf("內部錯誤要回 500 且不洩漏內容：%v", err)
	}

	in := &configImportInput{}
	in.Body.Content = "x"
	f.importErr = &pipeline.InvalidError{Msg: "m", Fields: []validate.FieldError{{Path: "profiles.x", Message: "y"}}}
	_, err = h.importConfig(ctx, in)
	e := asError(err)
	if e.Status != http.StatusUnprocessableEntity || e.Code != CodeConfigInvalid || len(e.Errors) != 1 || e.Errors[0].Path != "profiles.x" || f.got != "x" {
		t.Errorf("422 對應 = %+v（got %q）", e, f.got)
	}
	f.importErr = errors.New("boom")
	if _, err := h.importConfig(ctx, in); asError(err).Status != http.StatusInternalServerError {
		t.Errorf("一般錯誤 = %v", err)
	}
}
