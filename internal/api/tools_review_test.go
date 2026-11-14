package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
)

// 安裝失敗：HTTP 回應只有固定的一句話，pip 的 stderr 尾段只進 log，而且 log 前先遮蔽 cookie 內容。
func TestInstallFailureResponseHidesPipOutputAndLogIsRedacted(t *testing.T) {
	var logBuf bytes.Buffer
	d := opsDeps()
	d.Log = slog.New(slog.NewJSONHandler(&logBuf, nil))
	detail := "pip：結束碼 1：ERROR: No matching distribution found\nWARNING: skipping cookie file entry: 'SID=sekrit-value'"
	d.Tools = &fakeTools{err: fmt.Errorf("%w：%s", ytdlp.ErrInstall, detail)}

	for _, path := range []string{"/api/tools/yt-dlp/update", "/api/tools/yt-dlp/rollback"} {
		logBuf.Reset()
		rec := localDo(NewLocal(d), "POST", path, "")
		var e Error
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != 502 || e.Code != CodeUpstream {
			t.Fatalf("%s：%d %s", path, rec.Code, rec.Body)
		}
		if e.Message != "yt-dlp 安裝失敗，詳見 server log。" {
			t.Errorf("%s：message = %q", path, e.Message)
		}
		if strings.Contains(rec.Body.String(), "pip") || strings.Contains(rec.Body.String(), "No matching") {
			t.Errorf("%s：回應不該含 pip 的輸出：%s", path, rec.Body)
		}
		if !strings.Contains(logBuf.String(), "No matching distribution") {
			t.Errorf("%s：pip 的輸出要寫進 log：%s", path, logBuf.String())
		}
		if strings.Contains(logBuf.String(), "sekrit-value") {
			t.Errorf("%s：log 前要先遮蔽 cookie 內容：%s", path, logBuf.String())
		}
	}
}

// state.json 損毀：409 tool_state_corrupt，訊息指出 state.json 的位置，讓使用者知道可以刪除重來。
func TestCorruptToolStateIs409(t *testing.T) {
	msg := fmt.Errorf("%w：/data/tools/yt-dlp/state.json 無法使用（x）；可以刪除這個檔案後重試。", ytdlp.ErrStateCorrupt)
	d := opsDeps()
	d.Tools = &fakeTools{err: msg}
	for _, path := range []string{"/api/tools/yt-dlp/update", "/api/tools/yt-dlp/rollback"} {
		rec := localDo(NewLocal(d), "POST", path, "")
		var e Error
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != 409 || e.Code != "tool_state_corrupt" {
			t.Fatalf("%s：%d %s", path, rec.Code, rec.Body)
		}
		if !strings.Contains(e.Message, "/data/tools/yt-dlp/state.json") || !strings.Contains(e.Message, "刪除") {
			t.Errorf("%s：message 要指出 state.json 的位置：%q", path, e.Message)
		}
	}
}

// 平台不支援受管理的 yt-dlp：409，訊息原樣給使用者。
func TestUnsupportedPlatformIs409(t *testing.T) {
	d := opsDeps()
	d.Tools = &fakeTools{err: ytdlp.ErrUnsupportedPlatform}
	rec := localDo(NewLocal(d), "POST", "/api/tools/yt-dlp/update", "")
	var e Error
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != 409 || e.Code != "tool_unsupported_platform" || e.Message != ytdlp.ErrUnsupportedPlatform.Error() {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

// 回退的結果帶 reason（簡短原因），message 不變。
func TestRolledBackResponseIncludesReason(t *testing.T) {
	d := opsDeps()
	d.Tools = &fakeTools{res: &ytdlp.Result{Version: "2026.11.01", RolledBack: true,
		Message: "yt-dlp 更新後自我檢查失敗，已回到 2026.11.01。", Reason: "探測檢查影片失敗"}}
	rec := localDo(NewLocal(d), "POST", "/api/tools/yt-dlp/update", "")
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["reason"] != "探測檢查影片失敗" || got["message"] != "yt-dlp 更新後自我檢查失敗，已回到 2026.11.01。" {
		t.Errorf("回應 = %v", got)
	}
}
