package ops

import (
	"context"
	"log/slog"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

// Calls 記錄外部工具的呼叫結果（OPS-R17、R18）：yt-dlp 的呼叫統計，以及帶 cookie 成功時 cookie 的最近一次成功使用。
// 記錄失敗只寫 log，不影響工具呼叫本身。不能在寫入交易中呼叫（它自己會寫入）。
type Calls struct {
	Store *store.Store
	Clock clock.Clock
	Log   *slog.Logger
}

// ToolCall 記一次 tool 的呼叫；failed 是「結束碼不是 0，而且不是影片本身不存在或私人」（OPS-R18）。
func (c *Calls) ToolCall(ctx context.Context, tool string, failed bool) {
	if c == nil || ctx.Err() != nil {
		return
	}
	if err := c.Store.Write().AddToolCall(ctx, tool, c.Clock.Now(), failed); err != nil {
		c.warn(ctx, "記錄工具呼叫失敗", "tool", tool, "error", err)
	}
}

// CookieOK 記錄平台的 cookie 剛剛被一次成功的呼叫使用（OPS-R17）。
func (c *Calls) CookieOK(ctx context.Context, platform string) {
	if c == nil || ctx.Err() != nil {
		return
	}
	if err := c.Store.Write().MarkSecretOK(ctx, CookieKind, platform, c.Clock.Now()); err != nil {
		c.warn(ctx, "記錄 cookie 成功使用失敗", "platform", platform, "error", err)
	}
}

func (c *Calls) warn(ctx context.Context, msg string, args ...any) {
	if c.Log != nil {
		c.Log.WarnContext(ctx, msg, args...)
	}
}
