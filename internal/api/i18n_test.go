package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/compat"
	"github.com/cxyfer/notesteme/internal/cookies"
	"github.com/cxyfer/notesteme/internal/hubclient"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/secrets"
)

// NFR-I18N-2：handler 直接回傳的 domain 錯誤（固定的句子）與組出來的整句也在目錄中，
// English 和繁中不同；code 不變。
func TestDomainMessagesAreTranslated(t *testing.T) {
	msgs := []string{
		auth.ErrInvalidPassword.Error(), auth.ErrEmptyNewPassword.Error(), pipeline.ErrInTrash.Error(),
		secrets.ErrInvalidAPIKey.Error(), cookies.ErrTooLarge.Error(), hubclient.MsgNotConfigured, installFailedMessage,
	}
	for _, what := range []string{"上傳 cookie", "修改密碼", "備份", "刪除影音", "回退 yt-dlp", "更新 yt-dlp", "查看系統資訊", "登出"} {
		msgs = append(msgs, "只有 owner 可以"+what+"。")
	}
	for _, m := range msgs {
		e := newError(http.StatusConflict, "some_code", m)
		// 简中可能與繁中同字（例如「只有 owner 可以更新 yt-dlp。」），只檢查 English 一定不同。
		got := apierr.Localize(e, apierr.LangEn)
		if got.Message == m || got.Code != "some_code" || got.Status != http.StatusConflict {
			t.Errorf("%q 沒有翻譯（%+v）", m, got)
		}
	}
	// requireOwner 的訊息確實是上面的形式（token 呼叫只有 owner 可以做的事）。
	ctx := context.WithValue(context.Background(), keyPrincipal, auth.Principal{Kind: "token"})
	if err := requireOwner(ctx, "備份"); err == nil || apierr.Localize(asError(err), apierr.LangEn).Message != "Only the owner can make backups." {
		t.Errorf("requireOwner = %v", err)
	}
	// 帶參數的訊息
	if got := apierr.Localize(errForbiddenScope("admin"), apierr.LangEn); got.Message != "Permission denied: the admin scope is required." || got.RequiredScope != "admin" {
		t.Errorf("scope = %+v", got)
	}
}

// CLI-R9 的 426：繁中訊息與 CLI 自己的檢查（compat.Message）逐字相同。
func TestVersionMessagesMatchCompat(t *testing.T) {
	for _, c := range []struct{ cli, serve, key string }{
		{"0.1.0", "0.9.0", "cli_too_old"},
		{"0.9.0", "0.1.3", "serve_too_old"},
	} {
		v := compat.Check(c.cli, c.serve)
		e := apierr.Newf(http.StatusUpgradeRequired, CodeClientIncompatible, c.key, c.cli, c.serve, compat.MajorMinor(c.serve))
		if e.Message != compat.Message(v, c.cli, c.serve) {
			t.Errorf("%s：%q vs %q", c.key, e.Message, compat.Message(v, c.cli, c.serve))
		}
	}
}
