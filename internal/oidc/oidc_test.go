package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	keyOnce          sync.Once
	rsaKey, rsaOther *rsa.PrivateKey
	ecKey            *ecdsa.PrivateKey
)

func keys(t testing.TB) {
	t.Helper()
	keyOnce.Do(func() {
		var err error
		if rsaKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			panic(err)
		}
		if rsaOther, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			panic(err)
		}
		if ecKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
			panic(err)
		}
	})
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func rsaJWK(kid string, k *rsa.PublicKey) map[string]any {
	return map[string]any{"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
		"n": b64(k.N.Bytes()), "e": b64(big.NewInt(int64(k.E)).Bytes())}
}

func ecJWK(kid string, k *ecdsa.PublicKey) map[string]any {
	x, y := make([]byte, 32), make([]byte, 32)
	k.X.FillBytes(x)
	k.Y.FillBytes(y)
	return map[string]any{"kty": "EC", "kid": kid, "crv": "P-256", "alg": "ES256", "x": b64(x), "y": b64(y)}
}

// sign 產生 JWS；alg 決定簽章方式（測試對抗輸入用，不檢查合理性）。
func sign(t testing.TB, header, claims map[string]any, key any) string {
	t.Helper()
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	in := b64(hb) + "." + b64(cb)
	sum := sha256.Sum256([]byte(in))
	var sig []byte
	switch k := key.(type) {
	case *rsa.PrivateKey:
		var err error
		if sig, err = rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, sum[:]); err != nil {
			t.Fatal(err)
		}
	case *ecdsa.PrivateKey:
		r, s, err := ecdsa.Sign(rand.Reader, k, sum[:])
		if err != nil {
			t.Fatal(err)
		}
		sig = make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
	case []byte:
		m := hmac.New(sha256.New, k)
		m.Write([]byte(in))
		sig = m.Sum(nil)
	case nil:
	}
	return in + "." + b64(sig)
}

// idp 是測試用的 IdP（TLS）。
type idp struct {
	srv        *httptest.Server
	jwks       []map[string]any
	disc       map[string]any
	discHits   atomic.Int32
	jwksHits   atomic.Int32
	tokenReply func(w http.ResponseWriter, r *http.Request)
	lastForm   url.Values
	lastUser   string
	lastPass   string
	mu         sync.Mutex
}

func newIdP(t *testing.T) *idp {
	t.Helper()
	keys(t)
	p := &idp{jwks: []map[string]any{rsaJWK("rsa1", &rsaKey.PublicKey), ecJWK("ec1", &ecKey.PublicKey)}}
	p.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			p.discHits.Add(1)
			p.mu.Lock()
			d := p.disc
			p.mu.Unlock()
			_ = json.NewEncoder(w).Encode(d)
		case "/jwks":
			p.jwksHits.Add(1)
			p.mu.Lock()
			ks := p.jwks
			p.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": ks})
		case "/token":
			_ = r.ParseForm()
			p.mu.Lock()
			p.lastForm = r.PostForm
			p.lastUser, p.lastPass, _ = r.BasicAuth()
			reply := p.tokenReply
			p.mu.Unlock()
			if reply != nil {
				reply(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id_token":"x.y.z","access_token":"secret-access"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(p.srv.Close)
	u := p.srv.URL
	p.disc = map[string]any{"issuer": u, "authorization_endpoint": u + "/authorize", "token_endpoint": u + "/token", "jwks_uri": u + "/jwks"}
	return p
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newProvider(t *testing.T, p *idp, secret string) (*Provider, *fakeClock) {
	t.Helper()
	clk := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	prov, err := New(Config{Issuer: p.srv.URL, ClientID: "client-1", ClientSecret: secret,
		RedirectURL: "https://notes.example/api/auth/oidc/callback", HTTP: p.srv.Client(), Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	return prov, clk
}

func goodClaims(iss string, now time.Time) map[string]any {
	return map[string]any{"iss": iss, "sub": "user-1", "aud": "client-1", "nonce": "n-1",
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix()}
}

func TestNewValidates(t *testing.T) {
	for _, c := range []Config{
		{}, {Issuer: "http://x", ClientID: "a", ClientSecret: "b", RedirectURL: "c"},
		{Issuer: "https://x", ClientID: "", ClientSecret: "b", RedirectURL: "c"},
	} {
		if _, err := New(c); err == nil {
			t.Errorf("New(%+v) should fail", c)
		}
	}
}

func TestAuthURL(t *testing.T) {
	p := newIdP(t)
	prov, _ := newProvider(t, p, "s")
	u, err := prov.AuthURL(context.Background(), AuthRequest{State: "st", Nonce: "no", Verifier: "ver"})
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	q := pu.Query()
	want := map[string]string{"response_type": "code", "response_mode": "query", "client_id": "client-1",
		"redirect_uri": "https://notes.example/api/auth/oidc/callback", "scope": "openid", "state": "st", "nonce": "no",
		"code_challenge": S256("ver"), "code_challenge_method": "S256"}
	for k, v := range want {
		if got := q[k]; len(got) != 1 || got[0] != v {
			t.Errorf("%s = %v, want %q", k, got, v)
		}
	}
	if q.Has("code_verifier") || strings.Contains(u, "ver&") {
		t.Error("code verifier must not appear in the URL")
	}
	// RFC 7636 附錄 B 的範例
	if S256("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk") != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Error("S256 does not match RFC 7636")
	}
}

func TestDiscoveryCacheAndValidation(t *testing.T) {
	p := newIdP(t)
	prov, clk := newProvider(t, p, "s")
	ctx := context.Background()
	for range 3 {
		if _, err := prov.AuthURL(ctx, AuthRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	if n := p.discHits.Load(); n != 1 {
		t.Fatalf("discovery fetched %d times, want 1 (cached)", n)
	}
	clk.add(CacheTTL)
	if _, err := prov.AuthURL(ctx, AuthRequest{}); err != nil {
		t.Fatal(err)
	}
	if n := p.discHits.Load(); n != 2 {
		t.Fatalf("discovery fetched %d times after TTL, want 2", n)
	}

	bad := []map[string]any{
		{"issuer": p.srv.URL + "/", "authorization_endpoint": p.srv.URL + "/a", "token_endpoint": p.srv.URL + "/t", "jwks_uri": p.srv.URL + "/j"},
		{"issuer": p.srv.URL, "authorization_endpoint": "http://evil/a", "token_endpoint": p.srv.URL + "/t", "jwks_uri": p.srv.URL + "/j"},
		{"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/a", "token_endpoint": "javascript:alert(1)", "jwks_uri": p.srv.URL + "/j"},
		{"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/a", "token_endpoint": p.srv.URL + "/t"},
	}
	for i, d := range bad {
		p.mu.Lock()
		p.disc = d
		p.mu.Unlock()
		prov2, _ := newProvider(t, p, "s")
		if _, err := prov2.AuthURL(ctx, AuthRequest{}); !errors.Is(err, ErrDiscovery) {
			t.Errorf("case %d: err = %v, want ErrDiscovery", i, err)
		}
	}
}

func TestDiscoveryDoesNotFollowRedirectsOrReadHugeBodies(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/big/.well-known/openid-configuration" {
			_, _ = w.Write([]byte(`{"issuer":"` + strings.Repeat("a", MaxResponseBytes) + `"}`))
			return
		}
		http.Redirect(w, r, "https://elsewhere.example/", http.StatusFound)
	}))
	defer srv.Close()
	for _, iss := range []string{srv.URL, srv.URL + "/big"} {
		prov, err := New(Config{Issuer: iss, ClientID: "c", ClientSecret: "s", RedirectURL: "https://r/cb", HTTP: srv.Client()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := prov.AuthURL(context.Background(), AuthRequest{}); !errors.Is(err, ErrDiscovery) {
			t.Errorf("issuer %s: err = %v, want ErrDiscovery", iss, err)
		}
	}
}

func TestExchange(t *testing.T) {
	p := newIdP(t)
	// secret 含有需要 form 編碼的字元（RFC 6749 §2.3.1）
	prov, _ := newProvider(t, p, "a b:c%+/")
	tok, err := prov.Exchange(context.Background(), "code-1", "verifier-1")
	if err != nil || tok != "x.y.z" {
		t.Fatalf("Exchange = %q, %v", tok, err)
	}
	if p.lastUser != "client-1" || p.lastPass != url.QueryEscape("a b:c%+/") {
		t.Errorf("basic auth = %q:%q", p.lastUser, p.lastPass)
	}
	f := p.lastForm
	if f.Get("grant_type") != "authorization_code" || f.Get("code") != "code-1" || f.Get("code_verifier") != "verifier-1" ||
		f.Get("redirect_uri") != "https://notes.example/api/auth/oidc/callback" || f.Has("client_secret") {
		t.Errorf("form = %v", f)
	}

	for name, reply := range map[string]func(http.ResponseWriter, *http.Request){
		"status": func(w http.ResponseWriter, _ *http.Request) { http.Error(w, `{"error":"invalid_grant"}`, 400) },
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/token2", http.StatusTemporaryRedirect)
		},
		"html": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`{"id_token":"a.b.c"}`))
		},
		"no id_token": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"x"}`))
		},
		"huge": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id_token":"` + strings.Repeat("a", MaxResponseBytes) + `"}`))
		},
	} {
		p.mu.Lock()
		p.tokenReply = reply
		p.mu.Unlock()
		if _, err := prov.Exchange(context.Background(), "c", "v"); !errors.Is(err, ErrExchange) {
			t.Errorf("%s: err = %v, want ErrExchange", name, err)
		} else if strings.Contains(err.Error(), "a b:c") || strings.Contains(err.Error(), "secret-access") {
			t.Errorf("%s: error leaks a secret: %v", name, err)
		}
	}
}

func TestVerify(t *testing.T) {
	p := newIdP(t)
	prov, clk := newProvider(t, p, "the-secret")
	ctx := context.Background()
	now := clk.now()
	iss := p.srv.URL
	rs := map[string]any{"alg": "RS256", "kid": "rsa1", "typ": "JWT"}
	es := map[string]any{"alg": "ES256", "kid": "ec1"}
	with := func(mod func(map[string]any)) map[string]any {
		c := goodClaims(iss, now)
		mod(c)
		return c
	}
	cases := []struct {
		name string
		tok  string
		want error
	}{
		{"rs256 ok", sign(t, rs, goodClaims(iss, now), rsaKey), nil},
		{"es256 ok", sign(t, es, goodClaims(iss, now), ecKey), nil},
		{"multi aud with azp", sign(t, rs, with(func(c map[string]any) { c["aud"] = []string{"x", "client-1"}; c["azp"] = "client-1" }), rsaKey), nil},
		{"exp 59s ago", sign(t, rs, with(func(c map[string]any) { c["exp"] = now.Add(-59 * time.Second).Unix() }), rsaKey), nil},
		{"iat 59s ahead", sign(t, rs, with(func(c map[string]any) { c["iat"] = now.Add(59 * time.Second).Unix() }), rsaKey), nil},

		{"bad signature", func() string { s := sign(t, rs, goodClaims(iss, now), rsaKey); return s[:len(s)-2] + "AA" }(), ErrSignature},
		{"other key same kid", sign(t, rs, goodClaims(iss, now), rsaOther), ErrSignature},
		{"unknown kid", sign(t, map[string]any{"alg": "RS256", "kid": "nope"}, goodClaims(iss, now), rsaOther), ErrUnknownKey},
		{"missing kid", sign(t, map[string]any{"alg": "RS256"}, goodClaims(iss, now), rsaKey), ErrUnknownKey},
		{"alg none", sign(t, map[string]any{"alg": "none", "kid": "rsa1"}, goodClaims(iss, now), nil), ErrAlgorithm},
		{"alg none with junk sig", sign(t, map[string]any{"alg": "none", "kid": "rsa1"}, goodClaims(iss, now), []byte("k")), ErrAlgorithm},
		{"hs256 with secret", sign(t, map[string]any{"alg": "HS256", "kid": "rsa1"}, goodClaims(iss, now), []byte("the-secret")), ErrAlgorithm},
		{"hs256 with rsa n", sign(t, map[string]any{"alg": "HS256", "kid": "rsa1"}, goodClaims(iss, now), rsaKey.N.Bytes()), ErrAlgorithm},
		{"lowercase alg", sign(t, map[string]any{"alg": "rs256", "kid": "rsa1"}, goodClaims(iss, now), rsaKey), ErrAlgorithm},
		{"PS256", sign(t, map[string]any{"alg": "PS256", "kid": "rsa1"}, goodClaims(iss, now), rsaKey), ErrAlgorithm},
		{"rs256 on ec key", sign(t, map[string]any{"alg": "RS256", "kid": "ec1"}, goodClaims(iss, now), rsaKey), ErrAlgorithm},
		{"es256 on rsa key", sign(t, map[string]any{"alg": "ES256", "kid": "rsa1"}, goodClaims(iss, now), ecKey), ErrAlgorithm},
		{"wrong issuer", sign(t, rs, with(func(c map[string]any) { c["iss"] = "https://evil.example" }), rsaKey), ErrIssuer},
		{"issuer trailing slash", sign(t, rs, with(func(c map[string]any) { c["iss"] = iss + "/" }), rsaKey), ErrIssuer},
		{"wrong audience", sign(t, rs, with(func(c map[string]any) { c["aud"] = "someone-else" }), rsaKey), ErrAudience},
		{"empty audience", sign(t, rs, with(func(c map[string]any) { c["aud"] = []string{} }), rsaKey), ErrAudience},
		{"multi aud no azp", sign(t, rs, with(func(c map[string]any) { c["aud"] = []string{"client-1", "x"} }), rsaKey), ErrAZP},
		{"multi aud wrong azp", sign(t, rs, with(func(c map[string]any) { c["aud"] = []string{"client-1", "x"}; c["azp"] = "x" }), rsaKey), ErrAZP},
		{"single aud wrong azp", sign(t, rs, with(func(c map[string]any) { c["azp"] = "x" }), rsaKey), ErrAZP},
		{"wrong nonce", sign(t, rs, with(func(c map[string]any) { c["nonce"] = "n-2" }), rsaKey), ErrNonce},
		{"missing nonce", sign(t, rs, with(func(c map[string]any) { delete(c, "nonce") }), rsaKey), ErrNonce},
		{"exp 60s ago", sign(t, rs, with(func(c map[string]any) { c["exp"] = now.Add(-60 * time.Second).Unix() }), rsaKey), ErrExpired},
		{"exp 120s ago", sign(t, rs, with(func(c map[string]any) { c["exp"] = now.Add(-120 * time.Second).Unix() }), rsaKey), ErrExpired},
		{"iat 60s ahead", sign(t, rs, with(func(c map[string]any) { c["iat"] = now.Add(60 * time.Second).Unix() }), rsaKey), ErrNotYet},
		{"missing exp", sign(t, rs, with(func(c map[string]any) { delete(c, "exp") }), rsaKey), ErrMalformed},
		{"string exp", sign(t, rs, with(func(c map[string]any) { c["exp"] = "9999999999" }), rsaKey), ErrMalformed},
		{"missing iat", sign(t, rs, with(func(c map[string]any) { delete(c, "iat") }), rsaKey), ErrMalformed},
		{"missing sub", sign(t, rs, with(func(c map[string]any) { delete(c, "sub") }), rsaKey), ErrMalformed},
		{"two parts", "a.b", ErrMalformed},
		{"four parts", sign(t, rs, goodClaims(iss, now), rsaKey) + ".x", ErrMalformed},
		{"padding", func() string { s := sign(t, rs, goodClaims(iss, now), rsaKey); return s + "==" }(), ErrMalformed},
		{"too large", strings.Repeat("a", MaxTokenBytes+1), ErrMalformed},
		{"empty", "", ErrMalformed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := prov.Verify(ctx, c.tok, "n-1")
			if c.want == nil {
				if err != nil || got.Subject != "user-1" || got.Issuer != iss {
					t.Fatalf("Verify = %+v, %v", got, err)
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
	// 呼叫端沒有 nonce 時一律拒絕
	if _, err := prov.Verify(ctx, sign(t, rs, with(func(c map[string]any) { c["nonce"] = "" }), rsaKey), ""); !errors.Is(err, ErrNonce) {
		t.Errorf("empty expected nonce: err = %v", err)
	}
	// ES256 的 DER 簽章（不是 r‖s）要拒絕
	tok := sign(t, es, goodClaims(iss, now), ecKey)
	parts := strings.Split(tok, ".")
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	der, _ := ecdsa.SignASN1(rand.Reader, ecKey, sum[:])
	if _, err := prov.Verify(ctx, parts[0]+"."+parts[1]+"."+b64(der), "n-1"); !errors.Is(err, ErrSignature) {
		t.Errorf("DER signature: err = %v", err)
	}
}

func TestJWKSRefreshIsRateLimited(t *testing.T) {
	p := newIdP(t)
	prov, clk := newProvider(t, p, "s")
	ctx := context.Background()
	now := clk.now()
	unknown := sign(t, map[string]any{"alg": "RS256", "kid": "later"}, goodClaims(p.srv.URL, now), rsaOther)
	for range 5 {
		if _, err := prov.Verify(ctx, unknown, "n-1"); !errors.Is(err, ErrUnknownKey) {
			t.Fatalf("err = %v", err)
		}
	}
	if n := p.jwksHits.Load(); n != 1 {
		t.Fatalf("JWKS fetched %d times within a minute, want 1", n)
	}
	// 金鑰輪替：一分鐘後可以再取得一次，新的 kid 就能用了。
	p.mu.Lock()
	p.jwks = append(p.jwks, rsaJWK("later", &rsaOther.PublicKey))
	p.mu.Unlock()
	clk.add(MinJWKSRefresh)
	unknown = sign(t, map[string]any{"alg": "RS256", "kid": "later"}, goodClaims(p.srv.URL, clk.now()), rsaOther)
	if _, err := prov.Verify(ctx, unknown, "n-1"); err != nil {
		t.Fatalf("after refresh: %v", err)
	}
	if n := p.jwksHits.Load(); n != 2 {
		t.Fatalf("JWKS fetched %d times, want 2", n)
	}
	// 已知的 kid 在快取期間不重新取得。
	good := sign(t, map[string]any{"alg": "RS256", "kid": "rsa1"}, goodClaims(p.srv.URL, clk.now()), rsaKey)
	for range 3 {
		if _, err := prov.Verify(ctx, good, "n-1"); err != nil {
			t.Fatal(err)
		}
	}
	if n := p.jwksHits.Load(); n != 2 {
		t.Fatalf("JWKS fetched %d times for a cached kid, want 2", n)
	}
	clk.add(CacheTTL)
	good = sign(t, map[string]any{"alg": "RS256", "kid": "rsa1"}, goodClaims(p.srv.URL, clk.now()), rsaKey)
	if _, err := prov.Verify(ctx, good, "n-1"); err != nil {
		t.Fatal(err)
	}
	if n := p.jwksHits.Load(); n != 3 {
		t.Fatalf("JWKS fetched %d times after TTL, want 3", n)
	}
}

func TestParseJWKS(t *testing.T) {
	keys(t)
	small, _ := rsa.GenerateKey(rand.Reader, 1024)
	offCurve := ecJWK("off", &ecKey.PublicKey)
	offCurve["y"] = b64(make([]byte, 32))
	encSet := rsaJWK("enc", &rsaKey.PublicKey)
	encSet["use"] = "enc"
	wrongAlg := rsaJWK("walg", &rsaKey.PublicKey)
	wrongAlg["alg"] = "RS512"
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	x, y := make([]byte, 48), make([]byte, 48)
	p384.X.FillBytes(x)
	p384.Y.FillBytes(y)
	set := map[string]any{"keys": []map[string]any{
		rsaJWK("ok", &rsaKey.PublicKey), ecJWK("ec", &ecKey.PublicKey),
		rsaJWK("small", &small.PublicKey), offCurve, encSet, wrongAlg,
		{"kty": "EC", "kid": "p384", "crv": "P-384", "x": b64(x), "y": b64(y)},
		{"kty": "oct", "kid": "hmac", "k": b64([]byte("secret"))},
		rsaJWK("dup", &rsaKey.PublicKey), rsaJWK("dup", &rsaOther.PublicKey),
		rsaJWK("", &rsaKey.PublicKey),
	}}
	b, _ := json.Marshal(set)
	got, err := ParseJWKS(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["ok"].rsa == nil || got["ec"].ec == nil {
		t.Fatalf("ParseJWKS kept %v", got)
	}
	if _, err := ParseJWKS([]byte("not json")); !errors.Is(err, ErrJWKS) {
		t.Errorf("bad JSON: %v", err)
	}
}

func FuzzParseJWT(f *testing.F) {
	keys(f)
	f.Add(sign(f, map[string]any{"alg": "RS256", "kid": "k"}, map[string]any{"sub": "x"}, rsaKey))
	f.Add("eyJhbGciOiJub25lIn0.e30.")
	f.Add("a.b.c")
	f.Fuzz(func(t *testing.T, raw string) {
		tok, err := parseJWT(raw)
		if err != nil {
			return
		}
		if tok.header.Alg != algRS256 && tok.header.Alg != algES256 {
			t.Fatalf("accepted alg %q", tok.header.Alg)
		}
		if tok.header.Kid == "" || len(tok.signature) == 0 {
			t.Fatal("accepted a token without kid or signature")
		}
		// 不能 panic；claim 檢查也不能 panic。
		_, _ = tok.claims.check(Expect{Issuer: "i", ClientID: "c", Nonce: "n", Now: time.Unix(0, 0)})
	})
}

func FuzzParseJWKS(f *testing.F) {
	keys(f)
	b, _ := json.Marshal(map[string]any{"keys": []map[string]any{rsaJWK("a", &rsaKey.PublicKey), ecJWK("b", &ecKey.PublicKey)}})
	f.Add(b)
	f.Add([]byte(`{"keys":[{"kty":"EC","crv":"P-256","kid":"x","x":"","y":""}]}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		ks, err := ParseJWKS(b)
		if err != nil {
			return
		}
		for kid, k := range ks {
			if kid == "" || (k.rsa == nil) == (k.ec == nil) {
				t.Fatalf("bad key %q: %+v", kid, k)
			}
			if k.rsa != nil && k.rsa.N.BitLen() < minRSABits {
				t.Fatal("accepted a small RSA key")
			}
		}
	})
}
