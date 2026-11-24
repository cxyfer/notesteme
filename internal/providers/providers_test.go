package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/source/supadata"
)

const testKey = "sk-test-supersecret-9876"

// newLLM 建立 LLM registry：所有 provider 都指到 base，環境變數有 key（keys 為 nil 時沒有）。
func newLLM(base string, keys map[string]string) *llm.Registry {
	reg := llm.NewRegistry(map[string]llm.ProviderConfig{
		"anthropic": {Type: llm.TypeAnthropic, BaseURL: base},
		"openai":    {Type: llm.TypeOpenAI, BaseURL: base + "/v1"},
		"gemini":    {Type: llm.TypeGemini, BaseURL: base},
		"local":     {Type: llm.TypeOpenAI, BaseURL: base + "/v1"},
	}, nil)
	reg.Env = keys
	return reg
}

func needsAction(t *testing.T, err error) *errclass.Error {
	t.Helper()
	var ce *errclass.Error
	if !errors.As(err, &ce) || ce.Class != errclass.ClassNeedsAction {
		t.Fatalf("err = %v，預期 needs_action", err)
	}
	return ce
}

func retryable(t *testing.T, err error) *errclass.Error {
	t.Helper()
	var ce *errclass.Error
	if !errors.As(err, &ce) || ce.Class != errclass.ClassRetryable {
		t.Fatalf("err = %v，預期 retryable", err)
	}
	return ce
}

func TestListIncludesBuiltinsAndConfig(t *testing.T) {
	c := &Catalog{
		LLM:           llm.NewRegistry(map[string]llm.ProviderConfig{"local": {Type: llm.TypeOpenAI, BaseURL: "http://x/v1"}}, nil),
		LLMConfigured: map[string]bool{"local": true},
		ASR: map[string]config.ASRProvider{
			"whisperx": {ID: "whisperx", Type: config.ASRTypeCommand, Command: []string{"whisperx"}},
			"voxtral":  {ID: "voxtral", Type: config.ASRTypeMistral},
		},
	}
	var got []string
	for _, in := range c.List() {
		got = append(got, in.ID+"/"+in.Kind+"/"+in.DefinedIn)
		if in.Deprecations == nil {
			t.Errorf("%s：deprecations 是 nil", in.ID)
		}
		if in.ID == "voxtral" && (in.Testable || in.UntestableReason == "") {
			t.Errorf("voxtral 不該提供測試：%+v", in)
		}
		if in.ID == "whisperx" && !in.Testable {
			t.Errorf("command provider 應該可以測試：%+v", in)
		}
	}
	want := "anthropic/llm/builtin gemini/llm/builtin groq/asr/builtin local/llm/config_file openai/llm/builtin supadata/transcript/builtin voxtral/asr/config_file whisperx/asr/config_file"
	if strings.Join(got, " ") != want {
		t.Fatalf("List = %v\n預期 %s", got, want)
	}
}

// 同一個 id 同時是 asr.providers 與 LLM provider 時，兩個都列出；單一 id 的查詢先取 asr，可以用 kind 指定。
func TestSameIDInSeveralKinds(t *testing.T) {
	c := &Catalog{
		LLM: llm.NewRegistry(nil, nil),
		ASR: map[string]config.ASRProvider{
			"openai": {ID: "openai", Type: config.ASRTypeCommand, Command: []string{"true"}},
			"groq":   {ID: "groq", Type: config.ASRTypeOpenAICompatible},
		},
	}
	var openai, groqs int
	for _, in := range c.List() {
		switch in.ID {
		case "openai":
			openai++
		case "groq":
			groqs++
		}
	}
	if openai != 2 || groqs != 1 {
		t.Fatalf("openai %d 個、groq %d 個，預期 2 與 1（設定檔的 groq 取代內建的）", openai, groqs)
	}
	if in, _ := c.Get("openai", ""); in.Kind != KindASR {
		t.Errorf("Get(openai) = %+v，預期 asr", in)
	}
	if in, _ := c.Get("openai", KindLLM); in.Kind != KindLLM {
		t.Errorf("Get(openai, llm) = %+v", in)
	}
	if in, _ := c.Get("groq", ""); in.DefinedIn != DefinedInConfigFile {
		t.Errorf("Get(groq) = %+v，預期設定檔的", in)
	}
}

// 停用公告的機制：目錄有資料時帶出，沒有的 provider 是空陣列。
func TestDeprecationsAreAttached(t *testing.T) {
	c := &Catalog{
		LLM: llm.NewRegistry(nil, nil),
		Deprecations: map[string][]Deprecation{
			"anthropic": {{Model: "fake-model-1", After: "2099-01-31", Replacement: "fake-model-2", Message: "測試用的假公告"}},
		},
	}
	in, ok := c.Get("anthropic", "")
	if !ok || len(in.Deprecations) != 1 {
		t.Fatalf("anthropic = %+v", in)
	}
	d := in.Deprecations[0]
	if d.Model != "fake-model-1" || d.After != "2099-01-31" || d.Replacement != "fake-model-2" || d.Message != "測試用的假公告" {
		t.Errorf("deprecation = %+v", d)
	}
	if in, _ := c.Get("openai", ""); in.Deprecations == nil || len(in.Deprecations) != 0 {
		t.Errorf("openai 的 deprecations = %#v，預期空陣列", in.Deprecations)
	}
	// 回傳的是複本：改它不會改到目錄。
	in.Deprecations[0].Model = "changed"
	if again, _ := c.Get("anthropic", ""); again.Deprecations[0].Model != "fake-model-1" {
		t.Error("Get 回傳的 deprecations 與目錄共用")
	}
}

// 每種 LLM 都送不產生費用的 GET 列表請求，帶各自的認證標頭。
func TestLLMTestSendsAuthenticatedListRequest(t *testing.T) {
	type seen struct{ method, path, query, xkey, auth, goog, version string }
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("x-api-key"), r.Header.Get("Authorization"), r.Header.Get("x-goog-api-key"), r.Header.Get("anthropic-version")})
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	keys := map[string]string{
		llm.KeyEnvVar("anthropic"): testKey, llm.KeyEnvVar("openai"): testKey,
		llm.KeyEnvVar("gemini"): testKey, llm.KeyEnvVar("local"): testKey,
	}
	c := &Catalog{LLM: newLLM(srv.URL, keys), HTTP: srv.Client()}
	for _, id := range []string{"anthropic", "openai", "gemini", "local"} {
		res, err := c.Test(context.Background(), id, "")
		if err != nil {
			t.Fatalf("%s：%v", id, err)
		}
		if res.Message != "連線成功。" || res.Latency < 0 {
			t.Errorf("%s：%+v", id, res)
		}
	}
	want := []seen{
		{"GET", "/v1/models", "limit=1", testKey, "", "", llm.AnthropicVersion},
		{"GET", "/v1/models", "", "", "Bearer " + testKey, "", ""},
		{"GET", "/v1beta/models", "pageSize=1", "", "", testKey, ""},
		{"GET", "/v1/models", "", "", "Bearer " + testKey, "", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("請求 = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("請求 %d = %+v，預期 %+v", i, got[i], want[i])
		}
	}
}

// 沒有 API key 時不發出請求，訊息與正式執行相同（LLM-R12）。
func TestLLMTestWithoutKeyIsNeedsAction(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	c := &Catalog{LLM: newLLM(srv.URL, nil), HTTP: srv.Client()}
	_, err := c.Test(context.Background(), "anthropic", "")
	ce := needsAction(t, err)
	if ce.Message != "LLM provider anthropic 沒有設定 API key。" || strings.Join(ce.Actions, ",") != ActionSetAPIKey {
		t.Errorf("err = %+v", ce)
	}
	if calls != 0 {
		t.Errorf("沒有 key 不該發出請求，收到 %d 個", calls)
	}
}

// 狀態碼的分類；訊息不含 key、網址與 provider 回應的內容。
func TestStatusMapping(t *testing.T) {
	for _, c := range []struct {
		status  int
		needs   bool
		message string
	}{
		{401, true, "provider openai 拒絕了 API key（HTTP 401）。"},
		{403, true, "provider openai 拒絕了 API key（HTTP 403）。"},
		{429, false, "provider openai 暫時無法使用（HTTP 429），請稍後再試。"},
		{503, false, "provider openai 暫時無法使用（HTTP 503），請稍後再試。"},
		{404, false, "provider openai 拒絕了測試請求（HTTP 404），請確認設定檔的 base_url。"},
		{302, false, "provider openai 拒絕了測試請求（HTTP 302），請確認設定檔的 base_url。"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c.status == 302 {
				// 不跟隨轉址：標頭帶有 API key。
				w.Header().Set("Location", "http://"+r.Host+"/elsewhere")
			}
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(`{"error":{"message":"echo ` + testKey + `"}}`))
		}))
		cat := &Catalog{LLM: newLLM(srv.URL, map[string]string{llm.KeyEnvVar("openai"): testKey}), HTTP: srv.Client()}
		_, err := cat.Test(context.Background(), "openai", "")
		srv.Close()
		var ce *errclass.Error
		if c.needs {
			ce = needsAction(t, err)
			if strings.Join(ce.Actions, ",") != ActionSetAPIKey {
				t.Errorf("%d：actions = %v", c.status, ce.Actions)
			}
		} else {
			ce = retryable(t, err)
		}
		if ce.Message != c.message {
			t.Errorf("%d：message = %q", c.status, ce.Message)
		}
		if strings.Contains(err.Error(), testKey) || strings.Contains(ce.Message, srv.URL) {
			t.Errorf("%d：錯誤含有 key 或網址：%v", c.status, err)
		}
	}
}

func TestTimeoutMessage(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(done)
	c := &Catalog{LLM: newLLM(srv.URL, map[string]string{llm.KeyEnvVar("openai"): testKey}), HTTP: srv.Client(), Timeout: 50 * time.Millisecond}
	_, err := c.Test(context.Background(), "openai", "")
	if ce := retryable(t, err); ce.Message != "provider openai 在 1 秒內沒有回應。" {
		t.Errorf("message = %q", ce.Message)
	}
}

// 測試請求經過 netguard：預設封鎖本機位址。
func TestNetguardBlocksPrivateAddress(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	c := &Catalog{LLM: newLLM(srv.URL, map[string]string{llm.KeyEnvVar("local"): testKey}), HTTP: netguard.NewClient(netguard.SystemDeps())}
	_, err := c.Test(context.Background(), "local", "")
	ce := retryable(t, err)
	if !strings.Contains(ce.Message, "對外連線的保護擋下") {
		t.Errorf("message = %q", ce.Message)
	}
	if calls != 0 {
		t.Errorf("被封鎖的位址收到 %d 個請求", calls)
	}
}

func TestGroqAndSupadata(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+r.Header.Get("x-api-key"))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := &Catalog{
		HTTP:     srv.Client(),
		Groq:     &groq.Client{BaseURL: srv.URL + "/openai/v1", APIKey: testKey},
		Supadata: &supadata.Client{BaseURL: srv.URL, APIKey: testKey},
	}
	for _, id := range []string{"groq", "supadata"} {
		if _, err := c.Test(context.Background(), id, ""); err != nil {
			t.Fatalf("%s：%v", id, err)
		}
	}
	want := []string{"GET /openai/v1/models Bearer " + testKey, "GET /v1/me " + testKey}
	if strings.Join(paths, "|") != strings.Join(want, "|") {
		t.Errorf("請求 = %v", paths)
	}

	// 沒有 client 或沒有 key：needs_action，不發出請求。
	paths = nil
	for _, c := range []struct {
		cat *Catalog
		id  string
		msg string
	}{
		{&Catalog{HTTP: srv.Client()}, "groq", "Groq 沒有設定 API key。"},
		{&Catalog{HTTP: srv.Client()}, "supadata", "Supadata 沒有設定 API key。"},
		{&Catalog{HTTP: srv.Client(), Groq: &groq.Client{BaseURL: srv.URL}}, "groq", "Groq 沒有設定 API key。"},
		{&Catalog{HTTP: srv.Client(), Supadata: &supadata.Client{BaseURL: srv.URL}}, "supadata", "Supadata 沒有設定 API key。"},
	} {
		_, err := c.cat.Test(context.Background(), c.id, "")
		if ce := needsAction(t, err); ce.Message != c.msg || strings.Join(ce.Actions, ",") != ActionSetAPIKey {
			t.Errorf("%s：%+v", c.id, ce)
		}
	}
	if len(paths) != 0 {
		t.Errorf("沒有 key 時發出了請求：%v", paths)
	}
}

// command provider 只檢查指令存在且可以執行，不執行它；訊息不含路徑。
func TestCommandIsCheckedButNotRun(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	script := filepath.Join(dir, "asr.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &Catalog{
		ASR: map[string]config.ASRProvider{
			"abs":      {Type: config.ASRTypeCommand, Command: []string{script, "{audio}"}},
			"onpath":   {Type: config.ASRTypeCommand, Command: []string{"whisperx", "{audio}"}},
			"missing":  {Type: config.ASRTypeCommand, Command: []string{"no-such-asr"}},
			"noexec":   {Type: config.ASRTypeCommand, Command: []string{plain}},
			"gone":     {Type: config.ASRTypeCommand, Command: []string{filepath.Join(dir, "gone")}},
			"template": {Type: config.ASRTypeCommand, Command: []string{"{audio}"}},
		},
		LookPath: func(name string) (string, error) {
			if name == "whisperx" {
				return "/usr/local/bin/whisperx", nil
			}
			return "", errors.New("not found")
		},
	}
	for _, id := range []string{"abs", "onpath"} {
		res, err := c.Test(context.Background(), id, "")
		if err != nil || res.Message != "找到指令且可以執行；沒有實際執行。" {
			t.Errorf("%s：%+v %v", id, res, err)
		}
	}
	for _, id := range []string{"missing", "noexec", "gone", "template"} {
		_, err := c.Test(context.Background(), id, "")
		ce := needsAction(t, err)
		if ce.Message != "找不到 command provider "+id+" 的指令，或沒有執行權限。" || strings.Join(ce.Actions, ",") != ActionEditConfig {
			t.Errorf("%s：%+v", id, ce)
		}
		if strings.Contains(ce.Message, dir) {
			t.Errorf("%s：訊息含有路徑", id)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("測試連線執行了 command")
	}
}

func TestNotFoundAndNotTestable(t *testing.T) {
	c := &Catalog{ASR: map[string]config.ASRProvider{"voxtral": {Type: config.ASRTypeMistral}}}
	if _, err := c.Test(context.Background(), "nope", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("nope：%v", err)
	}
	if _, err := c.Test(context.Background(), "voxtral", ""); !errors.Is(err, ErrNotTestable) {
		t.Errorf("voxtral：%v", err)
	}
	if _, err := c.Test(context.Background(), "voxtral", KindLLM); !errors.Is(err, ErrNotFound) {
		t.Errorf("voxtral 指定 llm：%v", err)
	}
}
