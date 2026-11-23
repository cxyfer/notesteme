package websession

import (
	"errors"
	"testing"
)

// CSRF 的推導值固定：改了 domain separator 會讓 Client 既有 session 的 CSRF 全部失效。
func TestCSRFForIsStable(t *testing.T) {
	if got := CSRFFor("abc"); got != "iTawM64A3gkwFxu6sxCIUA29m_aBRTynmHpQkN42Wl0" {
		t.Fatalf("CSRFFor(abc) = %q", got)
	}
}

func TestCheckCSRF(t *testing.T) {
	tok, err := RandomToken(32)
	if err != nil || len(tok) != 43 {
		t.Fatalf("RandomToken = %q, %v", tok, err)
	}
	good := CSRFFor(tok)
	if err := CheckCSRF(tok, good, good); err != nil {
		t.Fatalf("合法的 CSRF 被拒絕：%v", err)
	}
	for name, c := range map[string][3]string{
		"空 session": {"", good, good}, "空 cookie": {tok, "", good}, "空標頭": {tok, good, ""},
		"標頭與 cookie 不同": {tok, good, good + "x"}, "cookie 與標頭相同但不是推導值": {tok, "forged", "forged"},
	} {
		if err := CheckCSRF(c[0], c[1], c[2]); !errors.Is(err, ErrCSRF) {
			t.Errorf("%s：err = %v，預期 ErrCSRF", name, err)
		}
	}
}

func TestHashToken(t *testing.T) {
	if HashToken("a") == HashToken("b") || len(HashToken("a")) != 64 {
		t.Fatal("HashToken 應是 SHA-256 的 hex")
	}
}
