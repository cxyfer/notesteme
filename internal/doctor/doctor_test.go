package doctor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

const buildconfWithLibass = "configuration:\n  --enable-gpl\n  --enable-libass\n  --enable-shared\n"

// fakeRunner 依工具檔名回應，並記錄呼叫順序。
type fakeRunner struct {
	out   map[string]toolexec.Result // key 為工具檔名
	err   map[string]error
	calls []string // "<工具> <參數...>"
}

func (f *fakeRunner) Run(_ context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	name := filepath.Base(c.Path)
	f.calls = append(f.calls, name+" "+strings.Join(c.Args, " "))
	if err := f.err[name]; err != nil {
		return toolexec.Result{}, err
	}
	return f.out[name], nil
}

// newDeps 在暫存目錄建立 tools 指定的工具檔（可執行），PATH 指向該目錄。
func newDeps(t *testing.T, tools []string, f *fakeRunner, env map[string]string) Deps {
	t.Helper()
	dir := t.TempDir()
	for _, n := range tools {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if env == nil {
		env = map[string]string{}
	}
	vars := map[string]string{"PATH": dir}
	for k, v := range env {
		vars[k] = v
	}
	return Deps{Resolver: toolexec.Resolver{Vars: vars}, Runner: f, Env: env}
}

func okRunner() *fakeRunner {
	return &fakeRunner{out: map[string]toolexec.Result{
		"ffmpeg":  {Stdout: []byte(buildconfWithLibass)},
		"ffprobe": {Stdout: []byte("ffprobe version 7.1\nbuilt with gcc\n")},
		"yt-dlp":  {Stdout: []byte("2026.11.01\n")},
		"deno":    {Stdout: []byte("deno 2.5.0 (stable)\nv8 14.0\n")},
	}}
}

var allTools = []string{"ffmpeg", "ffprobe", "yt-dlp", "deno"}

func keyEnv() map[string]string {
	return map[string]string{config.EnvSupadataAPIKey: "k", config.EnvGroqAPIKey: "k"}
}

func byName(t *testing.T, r Report, name string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("報告中沒有 %q：%+v", name, r.Checks)
	return Check{}
}

func TestAllOK(t *testing.T) {
	f := okRunner()
	r := Run(context.Background(), newDeps(t, allTools, f, keyEnv()))
	for _, c := range r.Checks {
		if c.Status != StatusOK || c.Fix != "" {
			t.Errorf("%s 應為 ok 且沒有 fix：%+v", c.Name, c)
		}
	}
	if r.Exit() != 0 {
		t.Errorf("Exit = %d", r.Exit())
	}
	if d := byName(t, r, NameYtDlp).Detail; !strings.Contains(d, "2026.11.01") {
		t.Errorf("yt-dlp 的 detail 應含版本：%q", d)
	}
	if d := byName(t, r, NameDeno).Detail; d != "deno 2.5.0 (stable)" {
		t.Errorf("deno 的 detail：%q", d)
	}
}

func TestOrderAndCalls(t *testing.T) {
	f := okRunner()
	r := Run(context.Background(), newDeps(t, allTools, f, keyEnv()))
	var names []string
	for _, c := range r.Checks {
		names = append(names, c.Name)
	}
	want := []string{NameFFmpeg, NameFFprobe, NameYtDlp, NameDeno, NameSupadata, NameGroq}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("順序 = %v，預期 %v", names, want)
	}
	wantCalls := []string{"ffmpeg -hide_banner -buildconf", "ffprobe -version", "yt-dlp --version", "deno --version"}
	if strings.Join(f.calls, "|") != strings.Join(wantCalls, "|") {
		t.Errorf("呼叫 = %v，預期 %v", f.calls, wantCalls)
	}
}

func TestFFmpegMissing(t *testing.T) {
	f := okRunner()
	d := newDeps(t, []string{"ffprobe", "yt-dlp", "deno"}, f, keyEnv())
	r := Run(context.Background(), d)
	c := byName(t, r, NameFFmpeg)
	if c.Status != StatusFail || !strings.Contains(c.Fix, "libass") || c.Detail == "" {
		t.Errorf("ffmpeg：%+v", c)
	}
	if r.Exit() != 1 {
		t.Errorf("Exit = %d", r.Exit())
	}
	// 其他項目照常檢查。
	if byName(t, r, NameFFprobe).Status != StatusOK {
		t.Error("ffprobe 不應受影響")
	}
}

func TestFFmpegBadPathFromEnvIsFail(t *testing.T) {
	// 對應場景：NOTESTEME_FFMPEG 指到不存在的路徑。
	f := okRunner()
	env := keyEnv()
	env["NOTESTEME_FFMPEG"] = "/nonexistent/ffmpeg"
	r := Run(context.Background(), newDeps(t, allTools, f, env))
	if c := byName(t, r, NameFFmpeg); c.Status != StatusFail || !strings.Contains(c.Detail, "NOTESTEME_FFMPEG") {
		t.Errorf("ffmpeg：%+v", c)
	}
	if r.Exit() != 1 {
		t.Errorf("Exit = %d", r.Exit())
	}
}

func TestNoLibass(t *testing.T) {
	f := okRunner()
	// libass 只出現在別的字串中不算：必須是完整的 flag。
	f.out["ffmpeg"] = toolexec.Result{Stdout: []byte("configuration:\n  --enable-gpl\n  --disable-libass\n")}
	r := Run(context.Background(), newDeps(t, allTools, f, keyEnv()))
	c := byName(t, r, NameFFmpeg)
	if c.Status != StatusFail || !strings.Contains(c.Fix, "libass") {
		t.Errorf("ffmpeg：%+v", c)
	}
	if r.Exit() != 1 {
		t.Errorf("Exit = %d", r.Exit())
	}
}

func TestFFmpegRunFailures(t *testing.T) {
	// 結束碼非 0 與 Runner 錯誤（逾時、取消）都是 fail。
	f := okRunner()
	f.out["ffmpeg"] = toolexec.Result{ExitCode: 1, Stderr: []byte("\nUnrecognized option\n")}
	c := byName(t, Run(context.Background(), newDeps(t, allTools, f, keyEnv())), NameFFmpeg)
	if c.Status != StatusFail || !strings.Contains(c.Detail, "Unrecognized option") {
		t.Errorf("ffmpeg：%+v", c)
	}

	f = okRunner()
	f.err = map[string]error{"ffmpeg": context.DeadlineExceeded}
	c = byName(t, Run(context.Background(), newDeps(t, allTools, f, keyEnv())), NameFFmpeg)
	if c.Status != StatusFail {
		t.Errorf("ffmpeg：%+v", c)
	}
}

func TestDenoMissingIsWarn(t *testing.T) {
	f := okRunner()
	r := Run(context.Background(), newDeps(t, []string{"ffmpeg", "ffprobe", "yt-dlp"}, f, keyEnv()))
	c := byName(t, r, NameDeno)
	if c.Status != StatusWarn || c.Fix == "" {
		t.Errorf("deno：%+v", c)
	}
	if r.Exit() != 0 {
		t.Errorf("只有 warn 時 Exit = %d，預期 0", r.Exit())
	}
}

func TestYtDlpMissing(t *testing.T) {
	f := okRunner()
	r := Run(context.Background(), newDeps(t, []string{"ffmpeg", "ffprobe", "deno"}, f, keyEnv()))
	c := byName(t, r, NameYtDlp)
	if c.Status != StatusFail || c.Fix == "" {
		t.Errorf("yt-dlp：%+v", c)
	}
	if r.Exit() != 1 {
		t.Errorf("Exit = %d", r.Exit())
	}
}

func TestYtDlpEmptyVersionIsFail(t *testing.T) {
	f := okRunner()
	f.out["yt-dlp"] = toolexec.Result{Stdout: []byte("\n")}
	r := Run(context.Background(), newDeps(t, allTools, f, keyEnv()))
	if c := byName(t, r, NameYtDlp); c.Status != StatusFail {
		t.Errorf("yt-dlp：%+v", c)
	}
}

func TestFFprobeMissing(t *testing.T) {
	r := Run(context.Background(), newDeps(t, []string{"ffmpeg", "yt-dlp", "deno"}, okRunner(), keyEnv()))
	if c := byName(t, r, NameFFprobe); c.Status != StatusFail || c.Fix == "" {
		t.Errorf("ffprobe：%+v", c)
	}
	if r.Exit() != 1 {
		t.Errorf("Exit = %d", r.Exit())
	}
}

func TestAPIKeyUnsetIsWarn(t *testing.T) {
	for _, env := range []map[string]string{nil, {config.EnvSupadataAPIKey: "  "}} {
		r := Run(context.Background(), newDeps(t, allTools, okRunner(), env))
		c := byName(t, r, NameSupadata)
		if c.Status != StatusWarn || !strings.Contains(c.Fix, "Supadata") || !strings.Contains(c.Fix, "fallback") {
			t.Errorf("env=%v：%+v", env, c)
		}
		if r.Exit() != 0 {
			t.Errorf("Exit = %d", r.Exit())
		}
	}
}

func TestGroqAPIKeyUnsetIsWarn(t *testing.T) {
	// 只設定 Supadata：Groq 是 warn，Supadata 是 ok，結束碼仍為 0。
	r := Run(context.Background(), newDeps(t, allTools, okRunner(), map[string]string{config.EnvSupadataAPIKey: "k"}))
	c := byName(t, r, NameGroq)
	if c.Status != StatusWarn || !strings.Contains(c.Fix, config.EnvGroqAPIKey) || !strings.Contains(c.Fix, "Groq") {
		t.Errorf("groq：%+v", c)
	}
	if byName(t, r, NameSupadata).Status != StatusOK || r.Exit() != 0 {
		t.Errorf("supadata：%+v，Exit = %d", byName(t, r, NameSupadata), r.Exit())
	}
}

func TestAPIKeyValueNeverAppearsInReport(t *testing.T) {
	const secret = "sk-very-secret"
	r := Run(context.Background(), newDeps(t, allTools, okRunner(), map[string]string{config.EnvSupadataAPIKey: secret, config.EnvGroqAPIKey: secret}))
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), secret) {
		t.Errorf("報告不應含 API key 的值：%s", b)
	}
}

func TestJSONShape(t *testing.T) {
	f := okRunner()
	r := Run(context.Background(), newDeps(t, []string{"ffprobe", "yt-dlp", "deno"}, f, keyEnv()))
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Checks []map[string]any `json:"checks"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Checks) != 6 || got.Checks[0]["name"] != "ffmpeg" || got.Checks[0]["status"] != "fail" {
		t.Errorf("JSON：%s", b)
	}
	if got.Checks[0]["fix"] == "" || got.Checks[0]["detail"] == "" {
		t.Errorf("fail 的項目應有 detail 與 fix：%s", b)
	}
}
