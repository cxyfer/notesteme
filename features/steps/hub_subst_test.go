package steps

import (
	"testing"

	"github.com/cucumber/godog"
)

// steps.md 4.30：M5 的參照在請求路徑、請求 body 與 CLI 參數中代入，預期結果的 JSON 不展開。
func TestM5RefsOnRequestSide(t *testing.T) {
	s := newTestScenario(t)
	s.World.SetHubRef("version", "raw", "v-123")
	s.World.SetHubRef("share", "last", "s-9")
	path, err := s.requestPath("/api/v1/versions/{version:raw}/content")
	if err != nil || path != "/api/v1/versions/v-123/content" {
		t.Errorf("requestPath = %q, %v", path, err)
	}
	body := string(s.requestBody(&godog.DocString{Content: `{ "version": "{version:raw}" }`}))
	if body != `{ "version": "v-123" }` {
		t.Errorf("requestBody = %q", body)
	}
	if got := s.expandArg("{share:last}"); got != "s-9" {
		t.Errorf("expandArg = %q", got)
	}
	if got := s.World.Subst(`{ "id": "{version:raw}" }`); got != `{ "id": "{version:raw}" }` {
		t.Errorf("預期端不應展開：%q", got)
	}
}
