package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/assstyle"
	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/bundle"
	"github.com/cxyfer/notesteme/internal/cookies"
	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/digest"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/hubclient"
	"github.com/cxyfer/notesteme/internal/hubsettings"
	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/prompts"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/search"
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/subscription"
	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
	"github.com/cxyfer/notesteme/internal/validate"
)

// i18nSentinels：handler 直接把 err.Error() 放進錯誤回應的 sentinel 錯誤（名稱 → 值）；文字要在目錄中。
// hubclient.MsgNotConfigured 是文字常數，包成 error 一起檢查。
var i18nSentinels = map[string]error{
	"assstyle.ErrBuiltin":                    assstyle.ErrBuiltin,
	"assstyle.ErrExists":                     assstyle.ErrExists,
	"assstyle.ErrNotFound":                   assstyle.ErrNotFound,
	"auth.ErrEmptyNewPassword":               auth.ErrEmptyNewPassword,
	"auth.ErrInvalidPassword":                auth.ErrInvalidPassword,
	"auth.ErrInvalidToken":                   auth.ErrInvalidToken,
	"auth.ErrNoPassword":                     auth.ErrNoPassword,
	"auth.ErrOIDCBusy":                       auth.ErrOIDCBusy,
	"auth.ErrOIDCNotAllowed":                 auth.ErrOIDCNotAllowed,
	"auth.ErrOIDCState":                      auth.ErrOIDCState,
	"auth.ErrSessionNotFound":                auth.ErrSessionNotFound,
	"auth.ErrTokenExists":                    auth.ErrTokenExists,
	"auth.ErrTokenInactive":                  auth.ErrTokenInactive,
	"auth.ErrTokenNotFound":                  auth.ErrTokenNotFound,
	"cookies.ErrTooLarge":                    cookies.ErrTooLarge,
	"digest.ErrExists":                       digest.ErrExists,
	"digest.ErrNotFound":                     digest.ErrNotFound,
	"hubsettings.ErrAcknowledgementRequired": hubsettings.ErrAcknowledgementRequired,
	"notify.ErrExists":                       notify.ErrExists,
	"notify.ErrNotFound":                     notify.ErrNotFound,
	"notify.ErrTooMany":                      notify.ErrTooMany,
	"pipeline.ErrArtifactNotFound":           pipeline.ErrArtifactNotFound,
	"pipeline.ErrBuiltinProfile":             pipeline.ErrBuiltinProfile,
	"pipeline.ErrBuiltinTemplate":            pipeline.ErrBuiltinTemplate,
	"pipeline.ErrChannel":                    pipeline.ErrChannel,
	"pipeline.ErrFrameNotFound":              pipeline.ErrFrameNotFound,
	"pipeline.ErrInTrash":                    pipeline.ErrInTrash,
	"pipeline.ErrMediaMissing":               pipeline.ErrMediaMissing,
	"pipeline.ErrNoAgentTasks":               pipeline.ErrNoAgentTasks,
	"pipeline.ErrNoAPIConfigured":            pipeline.ErrNoAPIConfigured,
	"pipeline.ErrNoFrames":                   pipeline.ErrNoFrames,
	"pipeline.ErrNoMedia":                    pipeline.ErrNoMedia,
	"pipeline.ErrNoNote":                     pipeline.ErrNoNote,
	"pipeline.ErrNotAwaitingUser":            pipeline.ErrNotAwaitingUser,
	"pipeline.ErrNoTranscript":               pipeline.ErrNoTranscript,
	"pipeline.ErrNotWaiting":                 pipeline.ErrNotWaiting,
	"pipeline.ErrProfileExists":              pipeline.ErrProfileExists,
	"pipeline.ErrProfileNotFound":            pipeline.ErrProfileNotFound,
	"pipeline.ErrTaskNotFound":               pipeline.ErrTaskNotFound,
	"pipeline.ErrTemplateExists":             pipeline.ErrTemplateExists,
	"pipeline.ErrTemplateNotFound":           pipeline.ErrTemplateNotFound,
	"pipeline.ErrUnrecognizedURL":            pipeline.ErrUnrecognizedURL,
	"prompts.ErrBuiltin":                     prompts.ErrBuiltin,
	"prompts.ErrExists":                      prompts.ErrExists,
	"prompts.ErrNotFound":                    prompts.ErrNotFound,
	"prompts.ErrVersionNotFound":             prompts.ErrVersionNotFound,
	"pipeline.ErrPromptNotFound":             pipeline.ErrPromptNotFound,
	"pipeline.ErrPromptVersionNotFound":      pipeline.ErrPromptVersionNotFound,
	"ytdlp.ErrInvalidRequest":                ytdlp.ErrInvalidRequest,
	"secrets.ErrInvalidAPIKey":               secrets.ErrInvalidAPIKey,
	"secrets.ErrInvalidHubToken":             secrets.ErrInvalidHubToken,
	"secrets.ErrUnknownProvider":             secrets.ErrUnknownProvider,
	"ytdlp.ErrBusy":                          ytdlp.ErrBusy,
	"ytdlp.ErrExternallyManaged":             ytdlp.ErrExternallyManaged,
	"ytdlp.ErrInvalidVersion":                ytdlp.ErrInvalidVersion,
	"ytdlp.ErrNoPrevious":                    ytdlp.ErrNoPrevious,
	"ytdlp.ErrStateCorrupt":                  ytdlp.ErrStateCorrupt,
	"ytdlp.ErrUnsupportedPlatform":           ytdlp.ErrUnsupportedPlatform,
	"hubclient.MsgNotConfigured":             errors.New(hubclient.MsgNotConfigured),
}

// i18nTyped：handler 直接把 x.Error()、x.Msg 或 x.Message 放進錯誤回應的型別化錯誤（型別 → 代表性的值的訊息）。
// 欄位錯誤的說明（validate.FieldError）另由 apierr 的 TestDomainTextsAreInCatalog 掃描 domain 套件。
var i18nTyped = map[string]func() *Error{
	"pipeline.InvalidError": func() *Error {
		f := []validate.FieldError{{Path: "language.targets", Message: "至少要有一項。"}}
		return fieldsError(http.StatusUnprocessableEntity, CodeInvalidInput, "language.targets：至少要有一項。", f)
	},
	"prompts.InvalidError": func() *Error {
		f := []validate.FieldError{{Path: "body", Message: "內文不可以是空的。"}}
		return fieldsError(http.StatusUnprocessableEntity, CodeInvalidInput, "body：內文不可以是空的。", f)
	},
	"subscription.FieldsError": func() *Error {
		fe := &subscription.FieldsError{Fields: []validate.FieldError{{Path: "filters.live", Message: "直播的處理方式必須是 wait 或 skip。"}}}
		return fieldsError(http.StatusUnprocessableEntity, CodeInvalidInput, fe.Error(), fe.Fields)
	},
	"assstyle.InvalidError": func() *Error {
		inv := &assstyle.InvalidError{Fields: []validate.FieldError{{Path: "font_size", Message: "字級必須在 1 到 400 之間。"}}}
		return newError(http.StatusUnprocessableEntity, CodeInvalidInput, inv.Error())
	},
	"cost.InvalidError": func() *Error {
		inv := &cost.InvalidError{Fields: []validate.FieldError{{Path: "unit", Message: "單位只能是 input_mtok、output_mtok、audio_hour 或 credit。"}}}
		return newError(http.StatusUnprocessableEntity, CodeInvalidInput, inv.Error())
	},
	"digest.InvalidError": func() *Error {
		return newError(http.StatusUnprocessableEntity, CodeInvalidInput, (&digest.InvalidError{}).Error())
	},
	"notify.InvalidError": func() *Error {
		return newError(http.StatusUnprocessableEntity, CodeInvalidInput, (&notify.InvalidError{}).Error())
	},
	"auth.LockedError": func() *Error {
		return newError(http.StatusTooManyRequests, CodeLoginLocked, (&auth.LockedError{RetryAfter: time.Minute}).Error())
	},
	"auth.OIDCDeniedError": func() *Error {
		return newError(http.StatusUnauthorized, CodeOIDCFailed, (&auth.OIDCDeniedError{Code: "access_denied"}).Error())
	},
	"auth.OIDCFailedError": func() *Error {
		return newError(http.StatusUnauthorized, CodeOIDCFailed, (&auth.OIDCFailedError{}).Error())
	},
	"pipeline.NotImplementedError": func() *Error {
		return newError(http.StatusNotImplemented, CodeNotImplemented, (&pipeline.NotImplementedError{What: "重跑步驟 frames"}).Error())
	},
	"pipeline.InUseError": func() *Error {
		return newError(http.StatusConflict, CodeProfileInUse, (&pipeline.InUseError{Code: pipeline.CodeProfileInUse}).Error())
	},
	"prompts.InUseError": func() *Error {
		return newError(http.StatusConflict, CodePromptInUse, (&prompts.InUseError{}).Error())
	},
	"pipeline.CorruptArtifactError": func() *Error {
		return newError(http.StatusConflict, CodeArtifactCorrupt, (&pipeline.CorruptArtifactError{Stage: "translate"}).Error())
	},
	"pipeline.TaskStateError": func() *Error {
		return newError(http.StatusConflict, "task_finished", (&pipeline.TaskStateError{Msg: "這個任務已經結束，不能取消。"}).Error())
	},
	"pipeline.UpstreamError": func() *Error {
		return newError(http.StatusBadGateway, CodeUpstream, (&pipeline.UpstreamError{Msg: "yt-dlp 讀取影音資訊失敗，請稍後再試。"}).Msg)
	},
	"pipeline.PolicyError": func() *Error {
		return newError(http.StatusUnprocessableEntity, CodeBlockedAddress, (&pipeline.PolicyError{Msg: "這個網址指向內部網路，已封鎖。"}).Msg)
	},
	"pipeline.HubError": func() *Error {
		return newError(http.StatusUnprocessableEntity, "share_not_allowed", (&pipeline.HubError{Msg: "非公開影音不能分享到 Hub。"}).Msg)
	},
	"pipeline.BundleConflictError": func() *Error {
		return newError(http.StatusConflict, "bundle_item_exists", (&pipeline.BundleConflictError{Message: "這部影音已在媒體庫中（yt:abc123xxxxx）。要把 bundle 的產出加成新版本，請加上 on_existing=add_versions。"}).Message)
	},
	"bundle.Error": func() *Error {
		return newError(http.StatusUnprocessableEntity, "bundle_invalid", (&bundle.Error{Message: "這不是 Notesteme bundle（沒有 manifest.json）。"}).Message)
	},
	"hubclient.Error": func() *Error {
		he := &hubclient.Error{Kind: hubclient.KindRateLimited, Status: http.StatusTooManyRequests, RetryAfterS: 30}
		return newError(he.HTTPStatus(), he.Code(), he.Error())
	},
	"errclass.Error": func() *Error {
		return newError(http.StatusConflict, CodeNeedsAction, (&errclass.Error{Message: "LLM provider openai 沒有設定 API key。"}).Message)
	},
	"subscription.URLError": func() *Error {
		return newError(http.StatusUnprocessableEntity, "unsupported_url", (&subscription.URLError{Message: "這是單一影片，請直接新增，不需要訂閱。"}).Message)
	},
	"subscription.DuplicateError": func() *Error {
		return newError(http.StatusConflict, CodeDuplicateSubscription, (&subscription.DuplicateError{}).Error())
	},
	"subscription.RateLimitedError": func() *Error {
		return newError(http.StatusTooManyRequests, CodeRateLimited, (&subscription.RateLimitedError{}).Error())
	},
	"subscription.PreviewError": func() *Error {
		return newError(http.StatusBadGateway, CodeUpstream, (&subscription.PreviewError{Message: "暫時無法檢查訂閱（HTTP 429），稍後會自動重試。"}).Message)
	},
	"subscription.ImportError": func() *Error {
		ie := subscription.ImportTooLarge()
		return newError(http.StatusRequestEntityTooLarge, ie.Code, ie.Message)
	},
}

// i18nExempt：無法以上面的方式歸類的訊息（「檔案:運算式」→ 理由）。
var i18nExempt = map[string]string{
	"errors.go:fields[0].Path+\"：\"+fields[0].Message": "firstFieldError：第一個欄位的「路徑：說明」，fieldsError 改用範本 field_invalid（說明另外檢查：handler 的欄位說明常值）",
	"scopes.go:\"只有 owner 可以\" + what + \"。\"":         "requireOwner 的每一種 what 組成的整句都在目錄中（i18n_test.go 的 TestDomainMessagesAreTranslated 逐一檢查）",
	"cookies.go:m.err.Error()":                         "cookieErrors 的每一個錯誤都在目錄中（TestRegisteredDomainTextsAreTranslated 逐一檢查）",
	"queue.go:queueErrorMessage(err)":                  "queue 的兩種錯誤加上句號；格式在 domainFormats（TestExemptSamplesAreTranslated 以 queue 實際的錯誤檢查）",
	"overview.go:cost.ZoneMessage(in.TZ)":              "cost.ZoneMessage 的格式在 domainFormats（TestExemptSamplesAreTranslated）",
	"costs.go:q.Message":                               "cost.QueryError 的訊息（apierr 的 TestDomainTextsAreInCatalog 掃描 cost:QueryError）",
	"subscriptions_preview.go:ie.Message":              "subscription.ImportTooLarge() 的固定句子（i18nTyped 的 subscription.ImportError 以它檢查）",
	"search.go:err.Error()":                            "search.NormalizeQuery 只回傳 *search.QueryError（MsgEmpty、MsgTooLong、MsgBadChars；TestExemptSamplesAreTranslated）",
	"tokens.go:err.Error()":                            "auth.ParseTokenDuration 只回傳包著 ErrInvalidToken 的四種句子；格式「token 參數不正確。: %s」在 domainFormats（TestExemptSamplesAreTranslated）",
}

// i18nExemptSamples 是 i18nExempt 中以實際的值檢查的訊息。
func i18nExemptSamples() map[string]string {
	_, durErr := auth.ParseTokenDuration("0d")
	return map[string]string{
		"queue 不認識的類別":       queueErrorMessage(fmt.Errorf("%w %q，只有 %v", queue.ErrUnknownClass, "bogus", queue.Classes)),
		"queue 不合法的上限":       queueErrorMessage(fmt.Errorf("%w（%s 是 %d）", queue.ErrInvalidLimit, queue.ResourceDownload, 99)),
		"cost.ZoneMessage":   cost.ZoneMessage("Mars/Olympus"),
		"search.MsgEmpty":    search.MsgEmpty,
		"search.MsgTooLong":  search.MsgTooLong,
		"search.MsgBadChars": search.MsgBadChars,
		"token 有效期間":         durErr.Error(),
	}
}

// i18nExempt 中以實際的值檢查的訊息也在目錄中。
func TestExemptSamplesAreTranslated(t *testing.T) {
	for name, s := range i18nExemptSamples() {
		if en, ok := apierr.Translate(s, apierr.LangEn); !ok || hasHan(en) {
			t.Errorf("%s 的訊息不在目錄中：%q", name, s)
		}
	}
}

// 註冊表中的文字都在目錄中：English 與繁中不同（简中可能與繁中同字，不檢查）。
func TestRegisteredDomainTextsAreTranslated(t *testing.T) {
	for name, err := range i18nSentinels {
		if en, ok := apierr.Translate(err.Error(), apierr.LangEn); !ok || hasHan(en) {
			t.Errorf("sentinel %s 的文字不在目錄中：%q", name, err.Error())
		}
	}
	for name, sample := range i18nTyped {
		e := sample()
		if got := apierr.Localize(e, apierr.LangEn); hasHan(got.Message) || got.Code != e.Code {
			t.Errorf("型別化錯誤 %s 的訊息沒有翻譯：%q → %q", name, e.Message, got.Message)
		}
	}
	for _, m := range cookieErrors {
		e := newError(http.StatusUnprocessableEntity, m.code, m.err.Error())
		if got := apierr.Localize(e, apierr.LangEn); hasHan(got.Message) {
			t.Errorf("cookie 錯誤 %s 的文字不在目錄中：%q", m.code, e.Message)
		}
	}
}

// hasHan：英文訊息還留著中文字，代表有一段沒有翻譯。
func hasHan(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) })
}
