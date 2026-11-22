package groq

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// COR-R7：詞彙表組成的 prompt 以 multipart 欄位 prompt 送出；不帶時沒有這個欄位。
func TestTranscribeSendsGlossaryPrompt(t *testing.T) {
	var fields map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		fields = r.MultipartForm.Value
		io.WriteString(w, okBody)
	}))
	defer srv.Close()

	if _, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "en", WithPrompt("KRaft, Confluent")); err != nil {
		t.Fatal(err)
	}
	if got := fields["prompt"]; len(got) != 1 || got[0] != "KRaft, Confluent" {
		t.Errorf("prompt = %v", got)
	}
	if _, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "en", WithPrompt("")); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["prompt"]; ok {
		t.Errorf("空的 prompt 不該送出：%v", fields)
	}
}

func TestGlossaryPrompt(t *testing.T) {
	if got := GlossaryPrompt([]string{" KRaft ", "", "Confluent"}); got != "KRaft, Confluent" {
		t.Errorf("= %q", got)
	}
	if got := GlossaryPrompt(nil); got != "" {
		t.Errorf("空 = %q", got)
	}
	// 放不下的詞整個丟棄，不從中間截斷；總長度不超過上限。
	long := strings.Repeat("字", 120)
	got := GlossaryPrompt([]string{long, long, "短"})
	if got != long || utf8.RuneCountInString(got) > MaxPromptRunes {
		t.Errorf("= %d 字元", utf8.RuneCountInString(got))
	}
	// 一個詞就超過上限：不送出任何提示。
	if got := GlossaryPrompt([]string{strings.Repeat("a", MaxPromptRunes+1), "b"}); got != "" {
		t.Errorf("過長的第一個詞 = %q", got)
	}
}

func FuzzGlossaryPrompt(f *testing.F) {
	f.Add("KRaft", "\xff")
	f.Fuzz(func(t *testing.T, a, b string) {
		got := GlossaryPrompt([]string{a, b})
		if utf8.RuneCountInString(got) > MaxPromptRunes {
			t.Fatalf("超過上限：%d", utf8.RuneCountInString(got))
		}
	})
}
