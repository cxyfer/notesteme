package steps

import (
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/faketool"
)

// recordedWrite 是 flushConfig 送出的一個請求。
type recordedWrite struct {
	method, path string
	body         any
	want         int
}

// recordingWriter 記錄 flushConfig 送出的請求；prompt 的回應帶上 version（建立是 1，新增版本是 2）。
func recordingWriter(calls *[]recordedWrite) configWriter {
	return func(method, path string, body any, want int, what string) (*response, error) {
		*calls = append(*calls, recordedWrite{method, path, body, want})
		version := 1
		if strings.HasSuffix(path, "/versions") {
			version = 2
		}
		return &response{status: want, body: []byte(`{"version":` + strconv.Itoa(version) + `}`)}, nil
	}
}

func declareAll(t *testing.T, s *Scenario) {
	t.Helper()
	// 故意不照寫入順序宣告：頻道、預設處理方案、處理方案、prompt。
	steps := []error{
		s.channelHasOverrides("yt:talk01xxxxx", &godog.DocString{Content: "language: { targets: [ja] }"}),
		s.defaultProfileIs("standard"),
		s.profileIs("standard", &godog.DocString{Content: "timing: { split_timing: forbid }"}),
		s.profileIs("second", &godog.DocString{Content: "language: { targets: [en] }"}),
		s.promptVersionIs("my-correct", 1, &godog.DocString{Content: "校對 {{glossary}}"}),
		s.promptVersionIs("my-correct", 2, &godog.DocString{Content: "校對 v2"}),
	}
	for i, err := range steps {
		if err != nil {
			t.Fatalf("宣告步驟 %d：%v", i, err)
		}
	}
}

// 步驟只累積，不送出請求；寫入順序是 prompt → 處理方案 → 預設處理方案 → 頻道覆寫。
func TestFlushConfig_Order(t *testing.T) {
	s := newTestScenario(t)
	declareAll(t, s)
	if s.server != nil || s.last != nil {
		t.Fatal("宣告步驟不能啟動 server 或送出請求")
	}
	if !s.hasPendingConfig() {
		t.Fatal("宣告後應該有尚未寫入的設定")
	}

	var calls []recordedWrite
	if err := s.flushConfigWith(recordingWriter(&calls)); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range calls {
		got = append(got, c.method+" "+c.path)
	}
	want := []string{
		"POST /api/prompts",
		"POST /api/prompts/my-correct/versions",
		"POST /api/profiles",
		"POST /api/profiles",
		"POST /api/profiles/standard/default",
		"PATCH /api/channels/youtube:UCfixture000000000000000/overrides",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("寫入順序 = %q\n預期 %q", got, want)
	}
	// 處理方案依宣告順序；prompt v1 帶 id、kind、body。
	if n := calls[2].body.(map[string]any)["name"]; n != "standard" {
		t.Errorf("第一個處理方案 = %v", n)
	}
	if n := calls[3].body.(map[string]any)["name"]; n != "second" {
		t.Errorf("第二個處理方案 = %v", n)
	}
	p1 := calls[0].body.(map[string]any)
	if p1["id"] != "my-correct" || p1["kind"] != "correct" || p1["body"] != "校對 {{glossary}}" {
		t.Errorf("prompt v1 的 body = %v", p1)
	}
	if calls[0].want != http.StatusCreated || calls[4].want != http.StatusOK || calls[5].want != http.StatusOK {
		t.Errorf("預期的狀態碼 = %d、%d、%d", calls[0].want, calls[4].want, calls[5].want)
	}
	// 寫入後清空，不會重複寫入。
	if s.hasPendingConfig() {
		t.Error("寫入後不應該還有累積的設定")
	}
	calls = nil
	if err := s.flushConfigWith(recordingWriter(&calls)); err != nil || len(calls) != 0 {
		t.Errorf("第二次 flush 不應該送出請求：%v、%d", err, len(calls))
	}
}

// 回應的 version 和宣告的版本不同時失敗。
func TestFlushConfig_PromptVersionMismatch(t *testing.T) {
	s := newTestScenario(t)
	if err := s.promptVersionIs("my-correct", 3, &godog.DocString{Content: "x"}); err != nil {
		t.Fatal(err)
	}
	var calls []recordedWrite
	err := s.flushConfigWith(recordingWriter(&calls))
	if err == nil || errors.Is(err, godog.ErrPending) || !strings.Contains(err.Error(), "version") {
		t.Errorf("err = %v", err)
	}
}

// 501 轉成 pending（不是 failed），而且之後的寫入不再送出。
func TestFlushConfig_501Pending(t *testing.T) {
	s := newTestScenario(t)
	declareAll(t, s)
	notImpl := &response{status: http.StatusNotImplemented, body: []byte(`{"code":"not_implemented","message":"x"}`)}
	var calls []recordedWrite
	rec := recordingWriter(&calls)
	// 第 3 個請求（第一個處理方案）回 501。
	write := func(method, path string, body any, want int, what string) (*response, error) {
		resp, err := rec(method, path, body, want, what)
		if err == nil && len(calls) == 3 {
			return checkConfigResponse(notImpl, method, path, want, what)
		}
		return resp, err
	}
	err := s.flushConfigWith(write)
	if !errors.Is(err, godog.ErrPending) {
		t.Fatalf("err = %v，預期 pending", err)
	}
	if len(calls) != 3 {
		t.Errorf("501 之後不應該再送出請求：送出了 %d 個", len(calls))
	}

	// checkConfigResponse 的分類：只有 not_implemented 的 501 是 pending，其他非預期的狀態碼都是 failed。
	for _, c := range []struct {
		name            string
		resp            *response
		pending, failed bool
	}{
		{"501 not_implemented", notImpl, true, false},
		{"501 但不是 not_implemented", &response{status: 501, body: []byte(`{"code":"other"}`)}, false, true},
		{"422", &response{status: 422, body: []byte(`{"code":"invalid_request","message":"bad"}`)}, false, true},
		{"預期的狀態碼", &response{status: 201, body: []byte(`{}`)}, false, false},
	} {
		_, err := checkConfigResponse(c.resp, "POST", "/api/x", 201, "測試")
		if got := errors.Is(err, godog.ErrPending); got != c.pending {
			t.Errorf("%s：pending = %v", c.name, got)
		}
		if got := err != nil && !errors.Is(err, godog.ErrPending); got != c.failed {
			t.Errorf("%s：failed = %v（%v）", c.name, got, err)
		}
		if c.failed && !strings.Contains(err.Error(), "HTTP "+strconv.Itoa(c.resp.status)) {
			t.Errorf("%s：失敗訊息要附上狀態碼：%v", c.name, err)
		}
	}
}

// 寫入設定用 s.do，不覆寫 s.last（previousRequestNotImplemented 依賴它）。
// 對真的 server 執行：Wave 0 的 stub 回 501（pending），路由實作後回 200；兩種結果 s.last 都不能變。
func TestFlushConfig_DoesNotTouchLast(t *testing.T) {
	s := newTestScenario(t)
	if err := s.startServer(); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureOwner(); err != nil {
		t.Fatal(err)
	}
	sentinel := &response{status: http.StatusTeapot, step: 7}
	s.last = sentinel
	s.step = 8
	if err := s.defaultProfileIs("default"); err != nil {
		t.Fatal(err)
	}
	if err := s.flushConfig(); err != nil && !errors.Is(err, godog.ErrPending) {
		t.Fatalf("flushConfig：%v", err)
	}
	if s.last != sentinel {
		t.Error("flushConfig 不應該改動 s.last")
	}
}

func TestPromptKindFromID(t *testing.T) {
	for id, want := range map[string]string{
		"my-correct":          "correct",
		"localize-zh-hant-tw": "translate",
		"my-poem-translate":   "translate",
		"segment-default":     "segment",
		"my.summary":          "summary",
	} {
		if got, err := promptKindFromID(id); err != nil || got != want {
			t.Errorf("promptKindFromID(%q) = %q, %v；預期 %q", id, got, err, want)
		}
	}
	for _, id := range []string{"foo", "", "my-poem"} {
		if got, err := promptKindFromID(id); err == nil || errors.Is(err, godog.ErrPending) {
			t.Errorf("promptKindFromID(%q) = %q, %v；預期失敗", id, got, err)
		}
	}
	// 推斷不出類型的宣告，在步驟中就失敗，不累積。
	s := newTestScenario(t)
	if err := s.promptVersionIs("foo", 1, &godog.DocString{Content: "x"}); err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("promptVersionIs(foo) = %v；預期失敗", err)
	}
	if s.hasPendingConfig() {
		t.Error("失敗的宣告不應該累積")
	}
}

func TestChannelKeyFor(t *testing.T) {
	s := newTestScenario(t)
	// yt：沒有登記的影片是預設頻道；登記了 channel_id 就用它。
	if got, err := s.channelKeyFor("yt:talk01xxxxx"); err != nil || got != "youtube:UCfixture000000000000000" {
		t.Errorf("yt 預設 = %q, %v", got, err)
	}
	s.World.Video("talk02xxxxx").ChannelID = "UCother000000000000000"
	if got, err := s.channelKeyFor("yt:talk02xxxxx"); err != nil || got != "youtube:UCother000000000000000" {
		t.Errorf("yt 登記的頻道 = %q, %v", got, err)
	}
	// bili：沒有登記 channel_id 時用 fake yt-dlp 推導的 uploader_id（與分P 無關）；登記了就用它。
	want := "bilibili:" + faketool.UploaderIDFor(nil, "BV1ai411c7mD")
	if got, err := s.channelKeyFor("bili:BV1ai411c7mD/p2"); err != nil || got != want {
		t.Errorf("bili 預設 = %q, %v；預期 %q", got, err, want)
	}
	if want == "bilibili:" || strings.Contains(want, "BV1ai411c7mD") {
		t.Errorf("uploader_id 應該是推導出來的數字：%q", want)
	}
	s.World.Video("BV1ai411c7mD").ChannelID = "123456"
	if got, err := s.channelKeyFor("bili:BV1ai411c7mD/p1"); err != nil || got != "bilibili:123456" {
		t.Errorf("bili 登記的 uploader = %q, %v", got, err)
	}
	// pod：feed 名稱。
	if got, err := s.channelKeyFor("pod:show/ep-1"); err != nil || got != "podcast:show" {
		t.Errorf("pod = %q, %v", got, err)
	}
	// local 沒有頻道；其他格式也失敗。這些都是失敗，不是 pending。
	for _, ref := range []string{"local:lecture.m4a", "pod:show", "bili:BV1", "nope"} {
		if got, err := s.channelKeyFor(ref); err == nil || errors.Is(err, godog.ErrPending) {
			t.Errorf("channelKeyFor(%q) = %q, %v；預期失敗", ref, got, err)
		}
	}
	// 宣告 local 的頻道覆寫在步驟中就失敗。
	if err := s.channelHasOverrides("local:lecture.m4a", &godog.DocString{Content: "language: { targets: [ja] }"}); err == nil {
		t.Error("local 的頻道覆寫應該失敗")
	}
}

// 頻道的 natural key 在寫入時才算：宣告之後才登記的 channel_id 也要生效。
func TestFlushConfig_ChannelKeyResolvedAtFlush(t *testing.T) {
	s := newTestScenario(t)
	if err := s.channelHasOverrides("yt:talk01xxxxx", &godog.DocString{Content: "language: { targets: [ja] }"}); err != nil {
		t.Fatal(err)
	}
	s.World.Video("talk01xxxxx").ChannelID = "UCchanged00000000000000"
	var calls []recordedWrite
	if err := s.flushConfigWith(recordingWriter(&calls)); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].path != "/api/channels/youtube:UCchanged00000000000000/overrides" || calls[0].method != http.MethodPatch {
		t.Errorf("calls = %+v", calls)
	}
}

// 空的預期生效設定會讓子集比對空洞通過：步驟要失敗。
func TestEffectiveConfigContainsRejectsEmpty(t *testing.T) {
	s := newTestScenario(t)
	err := s.effectiveConfigContains("yt:abc123xxxxx", "transcript", &godog.DocString{Content: "\n"})
	if err == nil || errors.Is(err, godog.ErrPending) || !strings.Contains(err.Error(), "空的") {
		t.Errorf("err = %v", err)
	}
}
