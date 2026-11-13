// Package doctor 實作 `notesteme doctor`（CLI-R8）：逐項檢查外部工具與 API key，
// 每項回報 ok、warn 或 fail 與修正方式。
//
// 子程序一律透過 toolexec.Runner 執行（輸出上限、環境變數 allowlist），
// 工具的尋找順序與實際執行時相同（toolexec.Resolver，OPS-R8）。
//
// TODO(CLI-R8)：網路連線檢查。要等 OPS 的「自我檢查用的已知影片」定義後才做；目前不做任何網路存取。
package doctor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// 檢查結果的狀態。
const (
	StatusOK   = "ok"
	StatusWarn = "warn"
	StatusFail = "fail"
)

// 檢查項目的名稱。
const (
	NameFFmpeg   = "ffmpeg"
	NameFFprobe  = "ffprobe"
	NameYtDlp    = "yt-dlp"
	NameDeno     = "deno"
	NameSupadata = "api_key.supadata"
)

// toolTimeout 是單次工具呼叫的逾時；只是查版本或設定，不需要太久。
const toolTimeout = 15 * time.Second

// Check 是單一項目的檢查結果。JSON 欄位與 CLI 場景的斷言一致。
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// Detail 說明結果，例如版本或失敗原因。
	Detail string `json:"detail,omitempty"`
	// Fix 是修正方式；ok 時為空。
	Fix string `json:"fix,omitempty"`
}

// Report 是整份檢查報告，Checks 依固定順序排列。
type Report struct {
	Checks []Check `json:"checks"`
}

// Exit 依 CLI-R6、CLI-R8 回傳結束碼：有任何 fail 為 1，其餘（含只有 warn）為 0。
func (r Report) Exit() int {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return 1
		}
	}
	return 0
}

// Deps 是檢查所需的依賴。
type Deps struct {
	// Resolver 尋找外部工具。
	Resolver toolexec.Resolver
	// Runner 執行外部工具。
	Runner toolexec.Runner
	// Env 是環境變數，用來判斷 API key 是否設定；不讀程序自己的環境。
	Env map[string]string
}

// Run 依固定順序執行檢查：ffmpeg、ffprobe、yt-dlp、deno、API key。
// 單項檢查失敗不會中斷後面的項目；ctx 取消時，仍在執行的工具檢查會回報 fail。
func Run(ctx context.Context, d Deps) Report {
	return Report{Checks: []Check{
		checkFFmpeg(ctx, d),
		checkFFprobe(ctx, d),
		checkYtDlp(ctx, d),
		checkDeno(ctx, d),
		checkSupadata(d),
	}}
}

// runTool 找到並執行工具，回傳 stdout。找不到、無法執行或結束碼非 0 時回傳錯誤。
func runTool(ctx context.Context, d Deps, spec toolexec.Spec, args ...string) (string, error) {
	path, err := d.Resolver.Resolve(spec)
	if err != nil {
		return "", err
	}
	res, err := d.Runner.Run(ctx, toolexec.Cmd{Path: path, Args: args, Timeout: toolTimeout})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("%s %s 的結束碼為 %d：%s", spec.Name, strings.Join(args, " "), res.ExitCode, firstLine(string(res.Stderr)))
	}
	return string(res.Stdout), nil
}

// firstLine 回傳第一個非空行（去除前後空白），沒有時回傳空字串。
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// installHint 是工具不可用時的通用修正方式，說明如何指定路徑（OPS-R8）。
func installHint(spec toolexec.Spec, install string) string {
	return fmt.Sprintf("%s；或以設定檔 tools.%s、環境變數 %s 指定路徑", install, spec.ConfigKey, spec.EnvVar)
}

func checkFFmpeg(ctx context.Context, d Deps) Check {
	c := Check{Name: NameFFmpeg}
	out, err := runTool(ctx, d, toolexec.FFmpeg, "-hide_banner", "-buildconf")
	if err != nil {
		c.Status, c.Detail = StatusFail, err.Error()
		c.Fix = installHint(toolexec.FFmpeg, "安裝含 libass 的 ffmpeg")
		return c
	}
	if !hasFlag(out, "--enable-libass") {
		c.Status, c.Detail = StatusFail, "ffmpeg 的編譯設定沒有 --enable-libass"
		c.Fix = "安裝含 libass 的 ffmpeg（預覽 ASS 字幕需要）"
		return c
	}
	c.Status, c.Detail = StatusOK, "可執行，含 libass"
	return c
}

// hasFlag 判斷 -buildconf 的輸出是否含完整的 flag（以空白分隔，不做子字串比對）。
func hasFlag(out, flag string) bool {
	for _, f := range strings.Fields(out) {
		if f == flag {
			return true
		}
	}
	return false
}

func checkFFprobe(ctx context.Context, d Deps) Check {
	c := Check{Name: NameFFprobe}
	out, err := runTool(ctx, d, toolexec.FFprobe, "-version")
	if err != nil {
		c.Status, c.Detail = StatusFail, err.Error()
		c.Fix = installHint(toolexec.FFprobe, "安裝 ffprobe（通常與 ffmpeg 一起提供）")
		return c
	}
	c.Status, c.Detail = StatusOK, firstLine(out)
	return c
}

func checkYtDlp(ctx context.Context, d Deps) Check {
	c := Check{Name: NameYtDlp}
	out, err := runTool(ctx, d, toolexec.YtDlp, "--version")
	if err != nil {
		c.Status, c.Detail = StatusFail, err.Error()
		c.Fix = installHint(toolexec.YtDlp, "安裝 yt-dlp（建議 pip install \"yt-dlp[default]\"）")
		return c
	}
	v := firstLine(out)
	if v == "" {
		c.Status, c.Detail = StatusFail, "yt-dlp --version 沒有輸出版本"
		c.Fix = "重新安裝 yt-dlp"
		return c
	}
	c.Status, c.Detail = StatusOK, "版本 "+v
	return c
}

// checkDeno 缺少時是 warn：只有部分 YouTube 解析需要它（ADR-0009）。
func checkDeno(ctx context.Context, d Deps) Check {
	c := Check{Name: NameDeno}
	out, err := runTool(ctx, d, toolexec.Deno, "--version")
	if err != nil {
		c.Status, c.Detail = StatusWarn, err.Error()
		c.Fix = installHint(toolexec.Deno, "安裝 Deno（官方發布的 deno）；缺少時 yt-dlp 的部分 YouTube 解析會失敗")
		return c
	}
	c.Status, c.Detail = StatusOK, firstLine(out)
	return c
}

// checkSupadata 沒有設定 API key 時是 warn：只是少一個 fallback，不影響其他來源。
func checkSupadata(d Deps) Check {
	c := Check{Name: NameSupadata}
	if strings.TrimSpace(d.Env[config.EnvSupadataAPIKey]) == "" {
		c.Status, c.Detail = StatusWarn, config.EnvSupadataAPIKey+" 未設定"
		c.Fix = "設定環境變數 " + config.EnvSupadataAPIKey + "；未設定時會略過 Supadata 這個逐字稿 fallback"
		return c
	}
	c.Status, c.Detail = StatusOK, "已設定"
	return c
}
