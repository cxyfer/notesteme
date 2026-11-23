package steps

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// GEM G9：`Gemini accepts …` 只對 GEM 的場景放行；note 的一般流程實作之前 anthropic、openai 的 note 回答是 pending（M3-S4 之後放行）。
func TestGeminiGates(t *testing.T) {
	s := newTestScenario(t)
	if err := s.geminiAcceptsUpTo("45m"); !errors.Is(err, godog.ErrPending) {
		t.Errorf("沒有 @GEM- 的場景：%v", err)
	}
	s.tags = []string{"@M3", "@GEM-R3"}
	mustNil(t, s.geminiAcceptsUpTo("45m"))
	mustFail(t, "不是時間長度", s.geminiAcceptsUpTo("forever"))
	mustFail(t, "0", s.geminiAcceptsUpTo("0s"))
	// M3-S4 之後 note 的一般流程已實作（StageAvailable("note")），anthropic 的 note 回答不再 pending。
	mustNil(t, llmNoteGate("anthropic", "note"))
	mustNil(t, llmNoteGate("gemini", "note"))
	mustNil(t, llmNoteGate("anthropic", "correct"))
}

// gemini 的請求內容：原始 body 與解出的文字都比對（JSON 跳脫不影響）。
func TestGeminiRequestContains(t *testing.T) {
	s := newTestScenario(t)
	mustFail(t, "沒有請求", s.llmRequestContains(1, "gemini", "x"))
	body := `{"contents":[{"parts":[{"file_data":{"file_uri":"https://www.youtube.com/watch?v=gem001xxxxx"}},{"text":"a \u003cb\u003e 片長 10:00"}]}],` +
		`"generationConfig":{"responseMimeType":"application/json","responseSchema":{"type":"OBJECT"}}}`
	s.World.Gemini.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1beta/models/m:generateContent", strings.NewReader(body)))
	mustNil(t, s.llmRequestContains(1, "gemini", "https://www.youtube.com/watch?v=gem001xxxxx"))
	mustNil(t, s.llmRequestContains(1, "gemini", "a <b> 片長 10:00"))
	mustNil(t, s.llmRequestContains(1, "gemini", "responseSchema"))
	mustFail(t, "不含", s.llmRequestContains(1, "gemini", "sections[1]"))
	mustNil(t, s.llmRequestDoesNotContain(1, "gemini", "sections[1]"))
	mustFail(t, "含", s.llmRequestDoesNotContain(1, "gemini", "<b>"))
	mustNil(t, s.llmReceived("gemini", 1))
}
