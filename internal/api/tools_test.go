package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
)

type fakeTools struct {
	reqs     []ytdlp.UpdateRequest
	rollback int
	res      *ytdlp.Result
	err      error
}

func (f *fakeTools) UpdateYtDlp(_ context.Context, req ytdlp.UpdateRequest) (*ytdlp.Result, error) {
	f.reqs = append(f.reqs, req)
	return f.res, f.err
}

func (f *fakeTools) RollbackYtDlp(context.Context) (*ytdlp.Result, error) {
	f.rollback++
	return f.res, f.err
}

type fakeSystem struct {
	view *SystemView
	err  error
}

func (f fakeSystem) Info(context.Context) (*SystemView, error) { return f.view, f.err }

func opsDeps() Deps {
	return Deps{Clock: clock.NewFake(time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)), Version: "test",
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil))}
}

// localDo 以本機 owner 的身分送出請求。
func localDo(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUpdateYtDlpPassesTheRequestAndReturnsTheResult(t *testing.T) {
	ft := &fakeTools{res: &ytdlp.Result{Version: "2026.11.10", Previous: "2026.11.01", Message: "yt-dlp 已更新到 2026.11.10。"}}
	d := opsDeps()
	d.Tools = ft
	h := NewLocal(d)

	for _, c := range []struct {
		body string
		want ytdlp.UpdateRequest
	}{
		{"", ytdlp.UpdateRequest{}},
		{`{}`, ytdlp.UpdateRequest{}},
		{`{"pin":"2026.11.01"}`, ytdlp.UpdateRequest{Pin: "2026.11.01"}},
		{`{"unpin":true}`, ytdlp.UpdateRequest{Unpin: true}},
	} {
		ft.reqs = nil
		rec := localDo(h, "POST", "/api/tools/yt-dlp/update", c.body)
		if rec.Code != 200 {
			t.Fatalf("body %q：status = %d %s", c.body, rec.Code, rec.Body)
		}
		if len(ft.reqs) != 1 || ft.reqs[0] != c.want {
			t.Errorf("body %q：請求 = %+v，預期 %+v", c.body, ft.reqs, c.want)
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got["version"] != "2026.11.10" || got["rolled_back"] != false || got["previous"] != "2026.11.01" {
			t.Errorf("body %q：回應 = %v", c.body, got)
		}
	}

	if rec := localDo(h, "POST", "/api/tools/yt-dlp/rollback", ""); rec.Code != 200 || ft.rollback != 1 {
		t.Errorf("rollback：%d %s（呼叫 %d 次）", rec.Code, rec.Body, ft.rollback)
	}
}

// 自我檢查失敗是正常的結果（200 加 rolled_back），不是錯誤。
func TestUpdateYtDlpRolledBackIsNotAnError(t *testing.T) {
	ft := &fakeTools{res: &ytdlp.Result{Version: "2026.11.01", RolledBack: true, Message: "yt-dlp 更新後自我檢查失敗，已回到 2026.11.01。"}}
	d := opsDeps()
	d.Tools = ft
	rec := localDo(NewLocal(d), "POST", "/api/tools/yt-dlp/update", "")
	var got struct {
		Version    string `json:"version"`
		RolledBack bool   `json:"rolled_back"`
		Message    string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !got.RolledBack || got.Version != "2026.11.01" || got.Message != "yt-dlp 更新後自我檢查失敗，已回到 2026.11.01。" {
		t.Errorf("回應 = %+v", got)
	}
}

func TestToolErrorsMapToStatusCodes(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{ytdlp.ErrInvalidVersion, 422, CodeInvalidInput},
		{fmt.Errorf("%w：x", ytdlp.ErrInvalidRequest), 422, CodeInvalidInput},
		{fmt.Errorf("%w：設定檔", ytdlp.ErrExternallyManaged), 409, CodeToolExternallyManaged},
		{ytdlp.ErrBusy, 409, CodeUpdateInProgress},
		{ytdlp.ErrNoPrevious, 409, CodeNoPreviousVersion},
		{fmt.Errorf("%w：pip：結束碼 1", ytdlp.ErrInstall), 502, CodeUpstream},
		{context.Canceled, 503, CodeInternal},
		{errors.New("disk on fire"), 500, CodeInternal},
	} {
		d := opsDeps()
		d.Tools = &fakeTools{err: c.err}
		for _, path := range []string{"/api/tools/yt-dlp/update", "/api/tools/yt-dlp/rollback"} {
			rec := localDo(NewLocal(d), "POST", path, "")
			var e Error
			_ = json.Unmarshal(rec.Body.Bytes(), &e)
			if rec.Code != c.status || e.Code != c.code {
				t.Errorf("%v（%s）：%d %s，預期 %d %s", c.err, path, rec.Code, rec.Body, c.status, c.code)
			}
		}
	}
	// 內部錯誤不洩漏細節。
	d := opsDeps()
	d.Tools = &fakeTools{err: errors.New("disk on fire")}
	if rec := localDo(NewLocal(d), "POST", "/api/tools/yt-dlp/update", ""); strings.Contains(rec.Body.String(), "disk on fire") {
		t.Errorf("500 不該洩漏內部訊息：%s", rec.Body)
	}
}

// token 即使帶了 scope 也不能更新或回退 yt-dlp、讀系統資訊（只有 owner），而且在碰到管理者之前就拒絕。
func TestOwnerOnlyOpsRoutesRefuseTokens(t *testing.T) {
	ft := &fakeTools{res: &ytdlp.Result{}}
	rt := &routes{d: opsDeps()}
	rt.d.Tools, rt.d.System = ft, fakeSystem{view: &SystemView{}}
	tokenCtx := context.WithValue(context.Background(), keyPrincipal, auth.Principal{Kind: "token"})
	ownerCtx := context.WithValue(context.Background(), keyPrincipal, auth.Principal{Kind: "session"})

	calls := map[string]func(context.Context) error{
		"update":   func(c context.Context) error { _, err := rt.updateYtDlp(c, &ytdlpUpdateInput{}); return err },
		"rollback": func(c context.Context) error { _, err := rt.rollbackYtDlp(c, nil); return err },
		"system":   func(c context.Context) error { _, err := rt.getSystem(c, nil); return err },
	}
	for name, call := range calls {
		var e *Error
		if err := call(tokenCtx); !errors.As(err, &e) || e.Status != 403 || e.Code != CodeForbidden {
			t.Errorf("%s（token）：err = %v，預期 403 forbidden", name, err)
		}
		if err := call(ownerCtx); err != nil {
			t.Errorf("%s（owner）：err = %v", name, err)
		}
	}
	if len(ft.reqs) != 1 || ft.rollback != 1 {
		t.Errorf("token 的請求不該到達管理者：更新 %d 次、回退 %d 次（owner 各一次）", len(ft.reqs), ft.rollback)
	}
}

// 沒有登入不能用（AUTH-R1）；沒有實作（nil 依賴）時回 501。
func TestOpsRoutesRequireLoginAndFallBackTo501(t *testing.T) {
	s := newTestServer(t)
	for _, c := range [][2]string{{"GET", "/api/system"}, {"POST", "/api/tools/yt-dlp/update"}, {"POST", "/api/tools/yt-dlp/rollback"}} {
		if rec := s.do(c[0], c[1], "", nil); rec.Code != 401 {
			t.Errorf("%s %s 沒登入：%d", c[0], c[1], rec.Code)
		}
		rec := localDo(NewLocal(opsDeps()), c[0], c[1], "")
		var e Error
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != 501 || e.Code != CodeNotImplemented {
			t.Errorf("%s %s 沒有實作：%d %s", c[0], c[1], rec.Code, rec.Body)
		}
	}
}

func TestSystemRouteReturnsTheView(t *testing.T) {
	d := opsDeps()
	d.System = fakeSystem{view: &SystemView{
		Listen: "127.0.0.1:8080", ListenSource: "default", Data: "/data", DataSource: "env",
		Tools: map[string]ToolView{
			"ffmpeg": {Path: "/opt/ffmpeg/bin/ffmpeg", Source: "config_file", Available: false, Error: "找不到"},
			"yt-dlp": {Path: "/data/tools/yt-dlp-venv/bin/yt-dlp", Source: "managed", Available: true},
		},
		YtDlp: YtDlpView{Pinned: Str("2026.11.01"), Previous: Str("")},
	}}
	rec := localDo(NewLocal(d), "GET", "/api/system", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	ffmpeg := got["tools"].(map[string]any)["ffmpeg"].(map[string]any)
	if got["listen_source"] != "default" || got["data_source"] != "env" || ffmpeg["source"] != "config_file" ||
		ffmpeg["available"] != false || ffmpeg["error"] != "找不到" {
		t.Errorf("回應 = %v", got)
	}
	ytd := got["tools"].(map[string]any)["yt-dlp"].(map[string]any)
	if _, has := ytd["error"]; has || ytd["available"] != true {
		t.Errorf("可用的工具不該有 error：%v", ytd)
	}
	if y := got["yt_dlp"].(map[string]any); y["pinned"] != "2026.11.01" || y["previous"] != "" {
		t.Errorf("yt_dlp = %v", y)
	}

	d.System = fakeSystem{err: errors.New("boom")}
	if rec := localDo(NewLocal(d), "GET", "/api/system", ""); rec.Code != 500 || strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("錯誤：%d %s", rec.Code, rec.Body)
	}
}
