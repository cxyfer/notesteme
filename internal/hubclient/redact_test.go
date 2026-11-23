package hubclient

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// 惡意的 Hub 把收到的 token 放進回應：錯誤訊息、欄位錯誤、上傳者名稱都不能帶出 token（\u 跳脫也一樣）。
func TestHubCannotEchoTheToken(t *testing.T) {
	escaped := `n` + testToken[1:] // 第一個字元用 \u 跳脫
	var status int
	var body string
	c, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
	for _, msg := range []string{testToken, escaped} {
		status = 422
		body = `{"code":"bad","message":"token=` + msg + `","errors":[{"path":"` + msg + `","message":"x ` + msg + `"}]}`
		e := asErr(t, func() error { _, err := c.Me(context.Background()); return err }())
		all := e.Error() + e.HubMessage
		for _, fe := range e.Errors {
			all += fe.Path + fe.Message
		}
		if strings.Contains(all, testToken) || !strings.Contains(e.HubMessage, redactedToken) {
			t.Errorf("422 的訊息帶出 token：%q", all)
		}
	}
	status = 200
	body = strings.Replace(versionJSON(nil), `"display_name":"alice‮"`, `"display_name":"`+escaped+`"`, 1)
	c2, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) })
	v, err := c2.Version(context.Background(), "v1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(v.Uploader, testToken) {
		t.Errorf("uploader = %q", v.Uploader)
	}
}

// 版本內容有 sha256 不能改寫：含 token 時整份拒絕。
func TestContentWithTheTokenIsRefused(t *testing.T) {
	for _, content := range [][]byte{
		[]byte(`{"lang":"en","variant":"source","speakers":[],"cues":[{"text":"` + testToken + `"}]}`),
		[]byte(`{"lang":"en","variant":"source","speakers":[],"cues":[{"text":"n` + testToken[1:] + `"}]}`),
	} {
		c, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(content) })
		e := asErr(t, func() error { _, err := c.Content(context.Background(), "v1", SHA256Hex(content), 0); return err }())
		if e.Kind != KindIntegrity {
			t.Errorf("e = %+v", e)
		}
	}
}

func TestScrubToken(t *testing.T) {
	if got := string(scrubToken([]byte("plain nsh_test text"), testToken)); got != "plain "+redactedToken+" text" {
		t.Errorf("不是 JSON：%q", got)
	}
	if got := string(scrubToken([]byte(`{"nsh_test":["a<nsh_test>",1.50]}`), testToken)); got != `{"[已遮蔽]":["a<[已遮蔽]>",1.50]}` {
		t.Errorf("JSON：%q", got)
	}
	in := []byte(`{"a":"b"}`)
	if got := scrubToken(in, testToken); string(got) != string(in) {
		t.Errorf("沒有 token 時不改寫：%q", got)
	}
	if got := scrubToken(in, ""); string(got) != string(in) {
		t.Error("沒有 token")
	}
	if containsToken([]byte(`{"a":"A"}`), testToken) || containsToken([]byte(`not json \u`), testToken) || containsToken(in, "") {
		t.Error("containsToken 誤判")
	}
}
