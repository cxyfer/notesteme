package idtoken

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var (
	testKey  = mustKey()
	otherKey = mustKey()
	now      = time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
)

func mustKey() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
}

func b64(v any) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

func sign(key *rsa.PrivateKey, hdr map[string]any, claims map[string]any) string {
	in := b64(hdr) + "." + b64(claims)
	sum := sha256.Sum256([]byte(in))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func goodClaims() map[string]any {
	return map[string]any{
		"iss": "https://accounts.google.com", "aud": "client-1", "sub": "s-1", "email": "a@example.com",
		"email_verified": true, "nonce": "n-1", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
}

func goodHeader() map[string]any { return map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"} }

var want = Expect{Issuers: []string{"https://accounts.google.com", "accounts.google.com"}, Audience: "client-1", Nonce: "n-1", Now: now}

func verify(raw string, key *rsa.PublicKey, w Expect) (Claims, error) {
	p, err := Parse(raw)
	if err != nil {
		return Claims{}, err
	}
	return p.VerifyWith(key, w)
}

func TestVerifyAccepts(t *testing.T) {
	c, err := verify(sign(testKey, goodHeader(), goodClaims()), &testKey.PublicKey, want)
	if err != nil || c.Subject != "s-1" || !c.EmailVerified || c.Email != "a@example.com" || c.HostedDomain != "" {
		t.Fatalf("Verify = %+v, %v", c, err)
	}
	withHD := goodClaims()
	withHD["hd"] = "example.com"
	if c, err := verify(sign(testKey, goodHeader(), withHD), &testKey.PublicKey, want); err != nil || c.HostedDomain != "example.com" {
		t.Fatalf("hd = %+v, %v", c, err)
	}
	// aud 是陣列、iss 沒有 https://、email_verified 是字串，都接受。
	cl := goodClaims()
	cl["aud"], cl["iss"], cl["email_verified"] = []string{"other", "client-1"}, "accounts.google.com", "true"
	if c, err := verify(sign(testKey, goodHeader(), cl), &testKey.PublicKey, want); err != nil || !c.EmailVerified {
		t.Fatalf("陣列 aud = %+v, %v", c, err)
	}
}

func TestVerifyRejects(t *testing.T) {
	mod := func(f func(c map[string]any)) string {
		c := goodClaims()
		f(c)
		return sign(testKey, goodHeader(), c)
	}
	good := sign(testKey, goodHeader(), goodClaims())
	parts := strings.Split(good, ".")
	tampered := goodClaims()
	tampered["sub"] = "attacker"
	cases := []struct {
		name string
		raw  string
		key  *rsa.PublicKey
		w    Expect
		err  error
	}{
		{"其他金鑰的簽章", sign(otherKey, goodHeader(), goodClaims()), &testKey.PublicKey, want, ErrSignature},
		{"竄改 claims", parts[0] + "." + b64(tampered) + "." + parts[2], &testKey.PublicKey, want, ErrSignature},
		{"alg none", b64(map[string]any{"alg": "none"}) + "." + parts[1] + ".x", &testKey.PublicKey, want, ErrAlgorithm},
		{"alg HS256", b64(map[string]any{"alg": "HS256", "kid": "k1"}) + "." + parts[1] + "." + parts[2], &testKey.PublicKey, want, ErrAlgorithm},
		{"空簽章", parts[0] + "." + parts[1] + ".", &testKey.PublicKey, want, ErrMalformed},
		{"兩段", parts[0] + "." + parts[1], &testKey.PublicKey, want, ErrMalformed},
		{"iss 不符", mod(func(c map[string]any) { c["iss"] = "https://evil.example" }), &testKey.PublicKey, want, ErrIssuer},
		{"aud 不符", mod(func(c map[string]any) { c["aud"] = "other" }), &testKey.PublicKey, want, ErrAudience},
		{"沒有 aud", mod(func(c map[string]any) { delete(c, "aud") }), &testKey.PublicKey, want, ErrAudience},
		{"過期", mod(func(c map[string]any) { c["exp"] = now.Add(-2 * time.Minute).Unix() }), &testKey.PublicKey, want, ErrExpired},
		{"沒有 exp", mod(func(c map[string]any) { delete(c, "exp") }), &testKey.PublicKey, want, ErrExpired},
		{"iat 在未來", mod(func(c map[string]any) { c["iat"] = now.Add(time.Hour).Unix() }), &testKey.PublicKey, want, ErrNotYet},
		{"nonce 不符", mod(func(c map[string]any) { c["nonce"] = "n-2" }), &testKey.PublicKey, want, ErrNonce},
		{"沒有 nonce", mod(func(c map[string]any) { delete(c, "nonce") }), &testKey.PublicKey, want, ErrNonce},
		{"預期的 nonce 是空的", good, &testKey.PublicKey, Expect{Issuers: want.Issuers, Audience: "client-1", Now: now}, ErrNonce},
		{"沒有 sub", mod(func(c map[string]any) { delete(c, "sub") }), &testKey.PublicKey, want, ErrMalformed},
		{"沒有金鑰", good, nil, want, ErrUnknownKey},
		{"太大", strings.Repeat("a", MaxTokenBytes+1), &testKey.PublicKey, want, ErrMalformed},
	}
	for _, c := range cases {
		if _, err := verify(c.raw, c.key, c.w); !errors.Is(err, c.err) {
			t.Errorf("%s：err = %v，預期 %v", c.name, err, c.err)
		}
	}
}

func jwksJSON(keys map[string]*rsa.PublicKey) []byte {
	var out []map[string]string
	for kid, k := range keys {
		out = append(out, map[string]string{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": kid,
			"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
		})
	}
	b, _ := json.Marshal(map[string]any{"keys": out})
	return b
}

func TestParseJWKSSkipsWeakOrForeignKeys(t *testing.T) {
	small, _ := rsa.GenerateKey(rand.Reader, 1024)
	b := jwksJSON(map[string]*rsa.PublicKey{"good": &testKey.PublicKey, "small": &small.PublicKey})
	var set map[string][]map[string]string
	_ = json.Unmarshal(b, &set)
	set["keys"] = append(set["keys"], map[string]string{"kty": "EC", "kid": "ec"}, map[string]string{"kty": "RSA", "kid": "enc", "use": "enc", "n": set["keys"][0]["n"], "e": "AQAB"})
	b, _ = json.Marshal(set)
	keys, err := ParseJWKS(b)
	if err != nil || len(keys) != 1 || keys["good"] == nil {
		t.Fatalf("ParseJWKS = %v, %v，預期只有 good", keys, err)
	}
	if _, err := ParseJWKS([]byte("{")); !errors.Is(err, ErrJWKS) {
		t.Fatalf("壞掉的 JWKS = %v", err)
	}
}

func TestKeysCacheAndRefresh(t *testing.T) {
	var hits atomic.Int32
	current := map[string]*rsa.PublicKey{"k1": &testKey.PublicKey}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(jwksJSON(current))
	}))
	defer srv.Close()
	clk := now
	k := &Keys{URL: srv.URL, Client: srv.Client(), Now: func() time.Time { return clk }}
	ctx := context.Background()
	if _, err := k.Verify(ctx, sign(testKey, goodHeader(), goodClaims()), want); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Key(ctx, "k1"); err != nil || hits.Load() != 1 {
		t.Fatalf("快取命中時不應重新取得：hits = %d, %v", hits.Load(), err)
	}
	// 未知的 kid：一分鐘內不重新取得。
	if _, err := k.Key(ctx, "k2"); !errors.Is(err, ErrUnknownKey) || hits.Load() != 1 {
		t.Fatalf("未知的 kid = %v, hits = %d", err, hits.Load())
	}
	// 輪替金鑰：一分鐘後找得到新的 kid。
	current = map[string]*rsa.PublicKey{"k2": &otherKey.PublicKey}
	clk = clk.Add(2 * time.Minute)
	if _, err := k.Key(ctx, "k2"); err != nil || hits.Load() != 2 {
		t.Fatalf("輪替後 = %v, hits = %d", err, hits.Load())
	}
	// 快取過期後重新取得。
	clk = clk.Add(2 * time.Hour)
	if _, err := k.Key(ctx, "k2"); err != nil || hits.Load() != 3 {
		t.Fatalf("過期後 = %v, hits = %d", err, hits.Load())
	}
	// 只接受 https。
	plain := &Keys{URL: "http://example.org/certs", Client: srv.Client(), Now: func() time.Time { return clk }}
	if _, err := plain.Key(ctx, "k1"); !errors.Is(err, ErrJWKS) {
		t.Fatalf("http 的 JWKS = %v", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(sign(testKey, goodHeader(), goodClaims()))
	f.Add("a.b.c")
	f.Add(b64(map[string]any{"alg": "RS256"}) + "." + b64(map[string]any{"aud": []any{1}}) + ".AA")
	f.Fuzz(func(t *testing.T, raw string) {
		p, err := Parse(raw)
		if err != nil {
			return
		}
		// 拆得開的輸入不能在驗證時 panic，也不能被任意金鑰接受。
		if _, err := p.VerifyWith(&otherKey.PublicKey, want); err == nil && strings.Count(raw, ".") == 2 {
			t.Fatalf("未以 otherKey 簽章的 token 被接受：%q", raw)
		}
	})
}

func FuzzParseJWKS(f *testing.F) {
	f.Add(jwksJSON(map[string]*rsa.PublicKey{"k": &testKey.PublicKey}))
	f.Add([]byte(`{"keys":[{"kty":"RSA","kid":"x","n":"AA","e":"AAAAAAAA"}]}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		keys, err := ParseJWKS(b)
		if err != nil {
			return
		}
		for _, k := range keys {
			if k.N.BitLen() < minRSABits {
				t.Fatalf("接受了太短的金鑰")
			}
		}
	})
}
