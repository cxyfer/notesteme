package fakegemini

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var repoFixtures = filepath.Join("..", "..", "fixtures", "llm")

func body(uri string) string {
	return `{"contents":[{"role":"user","parts":[{"file_data":{"file_uri":"` + uri + `"}},{"text":"write the note"}]}],
	"generationConfig":{"responseMimeType":"application/json","responseSchema":{"type":"object"}}}`
}

func post(t *testing.T, srv *httptest.Server, key, b string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1beta/models/fixture-gemini:generateContent", strings.NewReader(b))
	req.Header.Set("x-goog-api-key", key)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func setup(t *testing.T) (*Server, *httptest.Server) {
	fake := New(repoFixtures, "test-key")
	fake.SetMaxDuration(45 * time.Minute)
	fake.SetVideo("gem001xxxxx", Video{Duration: 10 * time.Minute, Public: true})
	fake.SetVideo("gem002xxxxx", Video{Duration: 10 * time.Minute})
	fake.SetVideo("gem004xxxxx", Video{Duration: 50 * time.Minute, Public: true})
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return fake, srv
}

func TestAnswersWithFixtureAsStructuredText(t *testing.T) {
	fake, srv := setup(t)
	fake.Answer("gemini-note-two-sections")
	resp, out := post(t, srv, "test-key", body("https://www.youtube.com/watch?v=gem001xxxxx"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	text := out["candidates"].([]any)[0].(map[string]any)["content"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
	var note struct {
		Sections []struct{ Start string } `json:"sections"`
	}
	if err := json.Unmarshal([]byte(text), &note); err != nil || len(note.Sections) != 2 || note.Sections[0].Start != "01:30" {
		t.Errorf("text = %s (%v)", text, err)
	}
	if n := len(fake.Requests()); n != 1 {
		t.Errorf("requests = %d", n)
	}
}

func TestLimitsAndErrors(t *testing.T) {
	fake, srv := setup(t)
	fake.Answer("gemini-note-two-sections")
	cases := []struct {
		name, key, body string
		want            int
	}{
		{"錯的 key", "nope", body("https://www.youtube.com/watch?v=gem001xxxxx"), http.StatusUnauthorized},
		{"不公開", "test-key", body("https://www.youtube.com/watch?v=gem002xxxxx"), http.StatusForbidden},
		{"太長", "test-key", body("https://www.youtube.com/watch?v=gem004xxxxx"), http.StatusBadRequest},
		{"不是 YouTube", "test-key", body("https://www.bilibili.com/video/BV1gm4y1c7mD"), http.StatusBadRequest},
		{"沒有 schema", "test-key", `{"contents":[]}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		if resp, _ := post(t, srv, c.key, c.body); resp.StatusCode != c.want {
			t.Errorf("%s: status %d，預期 %d", c.name, resp.StatusCode, c.want)
		}
	}
	fake.Fail(http.StatusTooManyRequests, true)
	if resp, _ := post(t, srv, "test-key", body("https://www.youtube.com/watch?v=gem001xxxxx")); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Fail once = %d", resp.StatusCode)
	}
	if resp, _ := post(t, srv, "test-key", body("https://www.youtube.com/watch?v=gem001xxxxx")); resp.StatusCode != http.StatusOK {
		t.Errorf("Fail once 之後 = %d", resp.StatusCode)
	}
}
