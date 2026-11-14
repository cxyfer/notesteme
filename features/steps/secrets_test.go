package steps

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/features/internal/faketool"
)

func TestCookieNeedles(t *testing.T) {
	// 標頭格式：值太短（fixture）只掃「名稱加值」的組合，不單獨掃值。
	got := cookieNeedles([]byte(`{"platform":"bilibili","content":"SESSDATA=fixture; buvid3=abcdefgh12"}`))
	for _, want := range []string{"SESSDATA=fixture", "SESSDATA\tfixture", "buvid3=abcdefgh12", "abcdefgh12"} {
		if !slices.Contains(got, want) {
			t.Errorf("缺少 %q：%q", want, got)
		}
	}
	if slices.Contains(got, "fixture") {
		t.Errorf("太短的值不該單獨掃描：%q", got)
	}

	// Netscape 格式：註解不算，#HttpOnly_ 的行算；值夠長時單獨掃。
	body := "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t0\tSID\tsecret-sid\n#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t0\tHSID\tx"
	got = cookieNeedles([]byte(`{"platform":"youtube","content":` + jsonString(body) + `}`))
	for _, want := range []string{"SID=secret-sid", "SID\tsecret-sid", "secret-sid", "HSID=x"} {
		if !slices.Contains(got, want) {
			t.Errorf("缺少 %q：%q", want, got)
		}
	}
	for _, bad := range []string{"Netscape", "HTTP Cookie File", "x"} {
		if slices.Contains(got, bad) {
			t.Errorf("不該掃描 %q：%q", bad, got)
		}
	}
	if cookieNeedles([]byte("not json")) != nil {
		t.Error("不是 JSON 時應回傳 nil")
	}
}

func jsonString(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, "\t", `\t`), "\n", `\n`) + `"`
}

// POST /api/cookies 的 cookie 值要出現在 log、假工具參數與 /data 的掃描中。
func TestCookieSecretsAreScanned(t *testing.T) {
	s := newTestScenario(t)
	s.noteRequest("GET", "/api/cookies", []byte(`{"content":"A=bbbbbbbbbbbb"}`)) // 不是 POST：不記
	if len(s.cookieSecrets) != 0 {
		t.Fatalf("GET 不該記錄：%v", s.cookieSecrets)
	}
	s.noteRequest("POST", "/api/cookies", []byte(`{"platform":"bilibili","content":"SESSDATA=fixture; sid=secret-sid-123"}`))
	if err := s.checkLogs(); err != nil {
		t.Fatalf("沒有外洩：%v", err)
	}
	if err := s.checkDataDirSecrets(); err != nil {
		t.Fatalf("沒有外洩：%v", err)
	}

	// log。
	_, _ = s.World.Log.Write([]byte("cookie SESSDATA=fixture saved"))
	if err := s.checkLogs(); err == nil || !strings.Contains(err.Error(), "SESSDATA=fixture") {
		t.Errorf("log 含 cookie 值：err = %v", err)
	}
	// 單獨的 fixture 不誤報。
	s = newTestScenario(t)
	s.noteRequest("POST", "/api/cookies", []byte(`{"content":"SESSDATA=fixture"}`))
	_, _ = s.World.Log.Write([]byte("fixture"))
	if err := s.checkLogs(); err != nil {
		t.Errorf("單獨的 fixture 不該誤報：%v", err)
	}

	// 假工具參數。
	s = newTestScenario(t)
	s.noteRequest("POST", "/api/cookies", []byte(`{"content":"sid=secret-sid-123"}`))
	writeToolCalls(t, s, faketool.Call{Tool: "yt-dlp", Argv: []string{"yt-dlp", "--add-header", "Cookie: x=secret-sid-123"}})
	if err := s.checkLogs(); err == nil || !strings.Contains(err.Error(), "呼叫參數") {
		t.Errorf("參數含 cookie 值：err = %v", err)
	}

	// /data（含 DB）。
	p := filepath.Join(s.World.DataDir(), "notesteme.db")
	if err := os.WriteFile(p, []byte("xx secret-sid-123 xx"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.checkDataDirSecrets(); err == nil || !strings.Contains(err.Error(), "notesteme.db") {
		t.Errorf("資料目錄含 cookie 值：err = %v", err)
	}
}

// 執行中的 cookie 檔要存在而且權限相符；結束後不能留下。
func TestCookieFileSteps(t *testing.T) {
	s := newTestScenario(t)
	tmp := filepath.Join(s.World.DataDir(), "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	ck := filepath.Join(tmp, "cookies-123.txt")

	// 還沒有帶 --cookies 的呼叫：兩個步驟都失敗，不會空轉通過。
	if err := s.cookieFileHasMode("0600"); err == nil {
		t.Error("沒有呼叫時應該失敗")
	}
	if err := s.noCookieFileRemains("/data/tmp"); err == nil {
		t.Error("沒有呼叫時應該失敗")
	}

	// 目前這個測試程序視為「執行中」的 yt-dlp。
	call := faketool.Call{Tool: "yt-dlp", Argv: []string{"yt-dlp"}, Cwd: s.World.Root, CookiesFile: ck, Pid: os.Getpid()}
	writeToolCalls(t, s, call)
	if err := s.cookieFileHasMode("0600"); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Errorf("cookie 檔不存在：err = %v", err)
	}
	if err := os.WriteFile(ck, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ck, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.cookieFileHasMode("0600"); err == nil || !strings.Contains(err.Error(), "0644") {
		t.Errorf("權限不符：err = %v", err)
	}
	if err := os.Chmod(ck, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.cookieFileHasMode("0600"); err != nil {
		t.Fatal(err)
	}
	if err := s.noCookieFileRemains("/data/tmp"); err == nil || !strings.Contains(err.Error(), "還在") {
		t.Errorf("檔案還在：err = %v", err)
	}

	// 程序已結束的呼叫不算執行中。
	call.Pid = 0
	writeToolCalls(t, s, call)
	if err := s.cookieFileHasMode("0600"); err == nil {
		t.Error("沒有執行中的呼叫應該失敗")
	}

	// 刪除之後通過；目錄中名稱含 cookie 的其他檔案仍然算。
	if err := os.Remove(ck); err != nil {
		t.Fatal(err)
	}
	if err := s.noCookieFileRemains("/data/tmp"); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(tmp, "yt-dlp-Cookies.tmp")
	if err := os.WriteFile(stray, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.noCookieFileRemains("/data/tmp"); err == nil || !strings.Contains(err.Error(), "yt-dlp-Cookies.tmp") {
		t.Errorf("殘留檔案：err = %v", err)
	}
	_ = os.Remove(stray)
	// 檔案放在別的目錄：斷言驗證不到 /data/tmp，失敗。
	call.CookiesFile = filepath.Join(s.World.Root, "elsewhere.txt")
	writeToolCalls(t, s, call)
	if err := s.noCookieFileRemains("/data/tmp"); err == nil || !strings.Contains(err.Error(), "都不在") {
		t.Errorf("放在別的目錄：err = %v", err)
	}
}

func TestFileHasMode(t *testing.T) {
	s := newTestScenario(t)
	p := filepath.Join(s.World.DataDir(), "f")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.fileHasMode("/data/f", "0600"); err != nil {
		t.Fatal(err)
	}
	if err := s.fileHasMode("/data/f", "0644"); err == nil {
		t.Error("權限不符應該失敗")
	}
	if err := s.fileHasMode("/data/nope", "0600"); err == nil {
		t.Error("檔案不存在應該失敗")
	}
}
