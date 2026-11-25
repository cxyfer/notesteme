package apierr

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// Huma 自己產生的錯誤也是這個格式，而且用繁中的預設訊息。
func TestHumaNewErrorUsesSharedFormat(t *testing.T) {
	err := huma.NewError(http.StatusUnprocessableEntity, "validation failed", errors.New("body.x: required"))
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("huma.NewError 回傳 %T，預期 *apierr.Error", err)
	}
	if e.Code != CodeInvalidInput || e.Message != "請求內容不正確。" || len(e.Details) != 1 {
		t.Errorf("e = %+v", e)
	}
}

// Huma 的驗證錯誤：message 帶第一個錯誤的位置與原因，原因依語言翻譯；details 照 Huma 的原文，不隨語言改變。
func TestHumaNewErrorNamesFirstProblem(t *testing.T) {
	err := huma.NewError(http.StatusUnprocessableEntity, "validation failed",
		&huma.ErrorDetail{Location: "query.limit", Message: "expected number <= 200", Value: 999},
		&huma.ErrorDetail{Location: "body.name", Message: "expected required property name to be present"})
	e := err.(*Error)
	if e.Code != CodeInvalidInput || e.Message != "請求內容不正確：query.limit 必須小於或等於 200。" || len(e.Details) != 2 {
		t.Fatalf("e = %+v", e)
	}
	en := Localize(e, LangEn)
	if en.Message != "The request content is invalid: query.limit must be less than or equal to 200." || en.Code != e.Code {
		t.Errorf("en = %+v", en)
	}
	if hans := Localize(e, LangZhHans); hans.Message != "请求内容不正确：query.limit 必须小于或等于 200。" {
		t.Errorf("zh-Hans = %+v", hans)
	}
	if !slices.Equal(en.Details, e.Details) {
		t.Errorf("details 隨語言改變：%v / %v", en.Details, e.Details)
	}
	// 不認得的原因照 Huma 的原文放進外層句子
	other := huma.NewError(http.StatusUnprocessableEntity, "x", &huma.ErrorDetail{Location: "body.x", Message: "something new"}).(*Error)
	if other.Message != "請求內容不正確：body.x something new。" {
		t.Errorf("不認得的原因：%q", other.Message)
	}
}

func TestAsHidesUnknownErrors(t *testing.T) {
	if e := As(errors.New("secret detail")); e.Status != 500 || e.Code != CodeInternal || e.Message == "secret detail" {
		t.Errorf("As(未知錯誤) = %+v", e)
	}
	orig := New(409, "x", "y")
	if As(orig) != orig {
		t.Error("As 應該原樣回傳 *Error")
	}
}

func TestWriteOmitsEmptyFields(t *testing.T) {
	rec := httptest.NewRecorder()
	e := NotImplemented()
	e.RequiredPermission = "upload"
	Write(rec, e)
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 501 || m["code"] != "not_implemented" || m["required_permission"] != "upload" {
		t.Errorf("回應 = %d %v", rec.Code, m)
	}
	if _, ok := m["required_scope"]; ok {
		t.Error("沒有設定的欄位不應該輸出")
	}
}
