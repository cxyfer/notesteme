package harness

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/netguard"
)

// groqTranscribe 經 fakenet 與 netguard，以正式主機名稱呼叫 Groq 的轉錄端點；auth 非空時帶 Authorization。
func groqTranscribe(t *testing.T, w *World, item, auth string) (int, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("response_format", "verbose_json")
	fw, _ := mw.CreateFormFile("file", "audio.m4a")
	_, _ = fw.Write([]byte(faketool.MediaMarker(item, 6)))
	_ = mw.Close()
	req, err := http.NewRequest(http.MethodPost, "https://"+GroqHost+"/openai/v1/audio/transcriptions", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	c := &http.Client{Transport: netguard.Transport(w.Net.Deps()), Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// 預設有 Groq 的 key：環境變數帶入測試金鑰，fake 的 api.groq.com 要求 Bearer。
func TestNewHasGroqKeyByDefault(t *testing.T) {
	w := NewT(t)
	if w.Env[GroqAPIKeyEnv] != TestGroqKey || GroqAPIKeyEnv != "NOTESTEME_GROQ_API_KEY" {
		t.Fatalf("Env[%s] = %q", GroqAPIKeyEnv, w.Env[GroqAPIKeyEnv])
	}
	w.Sources.Returns("groq", "yt:nocap1xxxxx", "groq-en.json")
	if code, _ := groqTranscribe(t, w, "nocap1xxxxx", ""); code != http.StatusUnauthorized {
		t.Errorf("沒有 key 應為 401：%d", code)
	}
	if code, _ := groqTranscribe(t, w, "nocap1xxxxx", "Bearer wrong"); code != http.StatusUnauthorized {
		t.Errorf("錯誤的 key 應為 401：%d", code)
	}
	code, body := groqTranscribe(t, w, "nocap1xxxxx", "Bearer "+TestGroqKey)
	if code != http.StatusOK || !strings.Contains(body, `"language": "english"`) {
		t.Fatalf("正確的 key：%d %s", code, body)
	}
	if w.Sources.Calls("groq") != 3 {
		t.Errorf("groq 呼叫數 = %d，預期 3（含被拒絕的）", w.Sources.Calls("groq"))
	}
}

// RemoveAPIKey 同時移除環境變數與 fake 的 key 要求；未知的來源回傳錯誤。
func TestRemoveAPIKey(t *testing.T) {
	w := NewT(t)
	w.Sources.Returns("groq", "yt:nocap1xxxxx", "groq-en.json")
	if err := w.RemoveAPIKey("groq"); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.Env[GroqAPIKeyEnv]; ok {
		t.Error("groq 的環境變數應該被移除")
	}
	if _, ok := w.Env[config.EnvSupadataAPIKey]; !ok {
		t.Error("supadata 的 key 不應被動到")
	}
	if code, _ := groqTranscribe(t, w, "nocap1xxxxx", ""); code != http.StatusOK {
		t.Errorf("移除後 fake 不再要求 key：%d", code)
	}
	if err := w.RemoveAPIKey("supadata"); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.Env[config.EnvSupadataAPIKey]; ok {
		t.Error("supadata 的環境變數應該被移除")
	}
	// LLM provider：移除環境變數，fakellm 也不再要求 key。
	if err := w.RemoveAPIKey("anthropic"); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.Env[AnthropicAPIKeyEnv]; ok {
		t.Error("anthropic 的環境變數應該被移除")
	}
	if w.Env[OpenAIAPIKeyEnv] == "" {
		t.Error("openai 的 key 不應被動到")
	}
	if err := w.RemoveAPIKey("gemini"); err == nil {
		t.Error("未知的來源應該回傳錯誤")
	}
}
