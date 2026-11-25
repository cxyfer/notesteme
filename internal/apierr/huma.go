package apierr

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/validation"
)

// Huma 產生的驗證錯誤（NFR-I18N-2、AUTH-R23）：message 帶上第一個錯誤的位置與原因，例如
// 「請求內容不正確：query.limit 必須是整數。」；原因有三種語言。details 照 Huma 的原文（English），不隨語言改變。
//
// humaReasons 把 Huma 的原因（validation.Msg* 與參數解析的訊息）對應到繁中的格式；只列 OpenAPI 中實際用到的限制
// （必填、不支援的欄位、型別、範圍、長度、列舉、RFC 3339 時間、oneOf）與查詢參數的解析錯誤。
// 不認得的原因照 Huma 的原文放進外層句子。
var humaReasons = []struct{ huma, hant string }{
	{validation.MsgExpectedRequiredProperty, "缺少必填欄位 %s"},
	{validation.MsgUnexpectedProperty, "是不支援的欄位"},
	{validation.MsgExpectedBoolean, "必須是布林值"},
	{validation.MsgExpectedNumber, "必須是數字"},
	{validation.MsgExpectedInteger, "必須是整數"},
	{validation.MsgExpectedString, "必須是字串"},
	{validation.MsgExpectedArray, "必須是陣列"},
	{validation.MsgExpectedObject, "必須是物件"},
	{validation.MsgExpectedOneOf, "必須是下列其中之一：%s"},
	{validation.MsgExpectedMinimumNumber, "必須大於或等於 %s"},
	{validation.MsgExpectedMaximumNumber, "必須小於或等於 %s"},
	{validation.MsgExpectedExclusiveMinimumNumber, "必須大於 %s"},
	{validation.MsgExpectedExclusiveMaximumNumber, "必須小於 %s"},
	{validation.MsgExpectedMinLength, "長度必須至少是 %s"},
	{validation.MsgExpectedMaxLength, "長度最多是 %s"},
	{validation.MsgExpectedRFC3339DateTime, "必須是 RFC 3339 時間"},
	{validation.MsgExpectedMatchExactlyOneSchema, "不符合任何一種允許的格式"},
	// 查詢與路徑參數的解析（huma.go 的 errors.New）
	{"invalid integer", "必須是整數"},
	{"invalid float", "必須是數字"},
	{"invalid floating value", "必須是數字"},
	{"invalid boolean", "必須是 true 或 false"},
}

// humaFormats 是上面的繁中原因與外層句子的其他語言（與 domainFormats 同樣經 translateText 反解）。
var humaFormats = []struct{ hant, hans, en string }{
	{"缺少必填欄位 %s", "缺少必填字段 {0}", "is missing the required property {0}"},
	{"必須是下列其中之一：%s", "必须是下列其中之一：{0}", "must be one of: {0}"},
	{"必須大於或等於 %s", "必须大于或等于 {0}", "must be greater than or equal to {0}"},
	{"必須小於或等於 %s", "必须小于或等于 {0}", "must be less than or equal to {0}"},
	{"必須大於 %s", "必须大于 {0}", "must be greater than {0}"},
	{"必須小於 %s", "必须小于 {0}", "must be less than {0}"},
	{"長度必須至少是 %s", "长度必须至少是 {0}", "must have a length of at least {0}"},
	{"長度最多是 %s", "长度最多是 {0}", "must have a length of at most {0}"},
}

// humaFixedReasons 是沒有參數的原因（放進 messages 的查找）。
var humaFixedReasons = map[string]translation{
	"是不支援的欄位":          {"是不支持的字段", "is not a supported property"},
	"必須是布林值":           {"必须是布尔值", "must be a boolean"},
	"必須是數字":            {"必须是数字", "must be a number"},
	"必須是整數":            {"必须是整数", "must be an integer"},
	"必須是字串":            {"必须是字符串", "must be a string"},
	"必須是陣列":            {"必须是数组", "must be an array"},
	"必須是物件":            {"必须是对象", "must be an object"},
	"必須是 RFC 3339 時間":  {"必须是 RFC 3339 时间", "must be an RFC 3339 time"},
	"不符合任何一種允許的格式":     {"不符合任何一种允许的格式", "does not match any of the allowed forms"},
	"必須是 true 或 false": {"必须是 true 或 false", "must be true or false"},
}

type humaReason struct {
	re   *regexp.Regexp
	hant string
}

var compiledHumaReasons = func() []humaReason {
	out := make([]humaReason, 0, len(humaReasons))
	for _, r := range humaReasons {
		re, _ := compileFormat(strings.ReplaceAll(r.huma, "%d", "%s"))
		out = append(out, humaReason{re, r.hant})
	}
	return out
}()

// humaReasonText 把 Huma 的原因換成繁中；不認得時回傳原文。
func humaReasonText(msg string) string {
	for _, r := range compiledHumaReasons {
		if m := r.re.FindStringSubmatch(msg); m != nil {
			args := make([]any, len(m)-1)
			for i, a := range m[1:] {
				args[i] = a
			}
			return fmt.Sprintf(r.hant, args...)
		}
	}
	return msg
}

// LocalizeTransformer 是 Huma 的 transformer：錯誤回應的訊息依 Accept-Language 選擇語言，code 與其他欄位不變
// （NFR-I18N-2、AUTH-R23）。Client 與 Hub 的 API 共用。
func LocalizeTransformer(ctx huma.Context, _ string, v any) (any, error) {
	if e, ok := v.(*Error); ok {
		return Localize(e, Negotiate(ctx.Header("Accept-Language"))), nil
	}
	return v, nil
}

func init() {
	for k, v := range humaFixedReasons {
		domainMessages[k] = v
	}
	// Huma 產生的錯誤（請求驗證失敗等）也使用同一種格式。這是 Huma 的全域設定，只能在這裡設定一次。
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		e := &Error{Status: status, Code: CodeInvalidInput, Message: msg}
		if d, ok := DefaultMessages[status]; ok {
			e.Code, e.Message = d.Code, d.Message
		}
		for _, err := range errs {
			if err == nil {
				continue
			}
			e.Details = append(e.Details, err.Error())
			if e.msgKey != "" {
				continue
			}
			// 第一個有位置的錯誤：message 帶上位置與原因（400 是參數解析、422 是 schema 驗證）。
			var det *huma.ErrorDetail
			if d, ok := err.(huma.ErrorDetailer); ok {
				det = d.ErrorDetail()
			}
			if det == nil || det.Location == "" {
				continue
			}
			key := ""
			switch status {
			case http.StatusUnprocessableEntity:
				key = "huma_invalid_at"
			case http.StatusBadRequest:
				key = "huma_malformed_at"
			}
			if key != "" {
				ne := Newf(status, e.Code, key, det.Location, humaReasonText(det.Message))
				e.Message, e.msgKey, e.msgArgs = ne.Message, ne.msgKey, ne.msgArgs
			}
		}
		return e
	}
}
