package secrets

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/chacha20poly1305"
)

func testKeyring(t *testing.T) *Keyring {
	t.Helper()
	k, err := Open(context.Background(), openStore(t), "key-one", TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	k := testKeyring(t)
	plain := []byte("SESSDATA=abc; bili_jct=def")
	nonce, ct, err := k.Seal("cookie", "bilibili", plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(nonce) != chacha20poly1305.NonceSizeX {
		t.Errorf("nonce 長度 = %d", len(nonce))
	}
	if bytes.Contains(ct, plain) {
		t.Error("密文含有明文")
	}
	got, err := k.Open("cookie", "bilibili", nonce, ct)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("Open = %q, %v", got, err)
	}
	// 每次加密用新的 nonce。
	nonce2, ct2, _ := k.Seal("cookie", "bilibili", plain)
	if bytes.Equal(nonce, nonce2) || bytes.Equal(ct, ct2) {
		t.Error("相同明文兩次加密的 nonce 或密文相同")
	}
	// 空明文也可以。
	n, c, err := k.Seal("cookie", "empty", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := k.Open("cookie", "empty", n, c); err != nil || len(got) != 0 {
		t.Errorf("空明文 Open = %q, %v", got, err)
	}
}

// AAD 綁定 kind/name：換成別的 kind 或 name 就解不開，避免密文被搬到另一筆 secret 使用。
func TestOpenFailsWithDifferentAAD(t *testing.T) {
	k := testKeyring(t)
	nonce, ct, _ := k.Seal("cookie", "bilibili", []byte("secret"))
	for _, c := range [][2]string{{"cookie", "youtube"}, {"apikey", "bilibili"}, {"cookie", ""}, {"", "bilibili"}} {
		if _, err := k.Open(c[0], c[1], nonce, ct); !errors.Is(err, ErrDecrypt) {
			t.Errorf("Open(%q, %q) err = %v，預期 ErrDecrypt", c[0], c[1], err)
		}
	}
}

func TestOpenFailsWhenTampered(t *testing.T) {
	k := testKeyring(t)
	nonce, ct, _ := k.Seal("cookie", "bilibili", []byte("secret"))

	flip := func(b []byte, i int) []byte {
		out := bytes.Clone(b)
		out[i] ^= 0x01
		return out
	}
	cases := map[string]struct{ nonce, ct []byte }{
		"密文被改":     {nonce, flip(ct, 0)},
		"tag 被改":   {nonce, flip(ct, len(ct)-1)},
		"nonce 被改": {flip(nonce, 0), ct},
		"密文被截斷":    {nonce, ct[:len(ct)-1]},
		"nonce 太短": {nonce[:8], ct},
		"空的 nonce": {nil, ct},
		"空的密文":     {nonce, nil},
	}
	for name, c := range cases {
		if _, err := k.Open("cookie", "bilibili", c.nonce, c.ct); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s：err = %v，預期 ErrDecrypt", name, err)
		}
	}
	// 另一把資料金鑰也解不開。
	other := testKeyring(t)
	if _, err := other.Open("cookie", "bilibili", nonce, ct); !errors.Is(err, ErrDecrypt) {
		t.Errorf("不同資料金鑰：err = %v", err)
	}
}

// 錯誤訊息不得出現資料金鑰（NFR-SEC-3），也不含明文或密文。
func TestSealOpenErrorsDoNotLeakKey(t *testing.T) {
	k := testKeyring(t)
	nonce, ct, _ := k.Seal("cookie", "bilibili", []byte("PLAINTEXT-VALUE"))
	_, err := k.Open("cookie", "other", nonce, ct)
	if err == nil {
		t.Fatal("預期失敗")
	}
	_, err2 := k.Open("cookie", "bilibili", nonce[:3], ct)
	if err2 == nil {
		t.Fatal("預期失敗")
	}
	for _, e := range []error{err, err2} {
		msg := e.Error()
		for _, leak := range []string{
			string(k.dataKey), hex.EncodeToString(k.dataKey),
			base64.StdEncoding.EncodeToString(k.dataKey), base64.RawStdEncoding.EncodeToString(k.dataKey),
			"PLAINTEXT-VALUE", hex.EncodeToString(ct), base64.StdEncoding.EncodeToString(ct),
		} {
			if strings.Contains(msg, leak) {
				t.Errorf("錯誤訊息洩漏了 %q：%s", leak, msg)
			}
		}
	}
}
