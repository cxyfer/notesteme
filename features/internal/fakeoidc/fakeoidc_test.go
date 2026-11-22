package fakeoidc

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	testIssuer = "https://id.example.com"
	testClient = "notesteme"
	testSecret = "fake-oidc-client-secret-7c1e"
	testSkew   = 60 * time.Second
)

// fixedClock 是可以前進的假時鐘。
type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func newTestServer(t *testing.T) (*Server, *httptest.Server, *fixedClock) {
	t.Helper()
	clk := &fixedClock{t: time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)}
	s, err := New(Config{Issuer: testIssuer, ClientID: testClient, ClientSecret: testSecret, Subject: "user-7f3a", Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return s, ts, clk
}

func getJSON(t *testing.T, u string, v any) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s：%d", u, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

// verify 是最小的 id_token 驗證器，規則照 AUTH-R18；只用來證明假 IdP 的缺陷真的會被抓到。
func verify(token string, keys map[string]*rsa.PublicKey, nonce string, now time.Time) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("格式")
	}
	var header struct{ Alg, Kid string }
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(hb, &header) != nil {
		return errors.New("header")
	}
	if header.Alg != "RS256" {
		return fmt.Errorf("alg %s", header.Alg)
	}
	key := keys[header.Kid]
	if key == nil {
		return fmt.Errorf("kid %s", header.Kid)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return errors.New("簽章編碼")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return errors.New("簽章")
	}
	var c struct {
		Iss, Sub, Aud, Nonce string
		Exp, Iat             int64
	}
	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(cb, &c) != nil {
		return errors.New("claims")
	}
	switch {
	case c.Iss != testIssuer:
		return errors.New("iss")
	case c.Aud != testClient:
		return errors.New("aud")
	case c.Nonce != nonce:
		return errors.New("nonce")
	case !time.Unix(c.Exp, 0).After(now.Add(-testSkew)):
		return errors.New("exp")
	case !time.Unix(c.Iat, 0).Before(now.Add(testSkew)):
		return errors.New("iat")
	}
	return nil
}

func fetchKeys(t *testing.T, base string) map[string]*rsa.PublicKey {
	t.Helper()
	var set struct {
		Keys []struct{ Kid, N, E string }
	}
	getJSON(t, base+"/jwks", &set)
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		n, _ := base64.RawURLEncoding.DecodeString(k.N)
		e, _ := base64.RawURLEncoding.DecodeString(k.E)
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	return keys
}

// login 走一次 authorize → token，回傳 token 端點的回應。
func login(t *testing.T, base, method, verifier, secret string) (int, map[string]any) {
	t.Helper()
	challenge := verifier
	if method == "S256" {
		challenge = S256(verifier)
	}
	q := url.Values{
		"response_type": {"code"}, "client_id": {testClient}, "scope": {"openid"},
		"redirect_uri": {"https://notes.example.com/api/auth/oidc/callback"},
		"state":        {"st-1"}, "nonce": {"n-1"}, "code_challenge": {challenge}, "code_challenge_method": {method},
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(base + "/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize：%d", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("state") != "st-1" {
		t.Fatalf("state 沒有原樣轉回：%s", loc)
	}
	code := loc.Query().Get("code")
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier},
		"redirect_uri": {"https://notes.example.com/api/auth/oidc/callback"}}
	req, _ := http.NewRequest(http.MethodPost, base+"/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(testClient, secret)
	tr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(tr.Body).Decode(&body)
	return tr.StatusCode, body
}

func TestDiscoveryUsesConfiguredIssuer(t *testing.T) {
	_, ts, _ := newTestServer(t)
	var d map[string]any
	getJSON(t, ts.URL+"/.well-known/openid-configuration", &d)
	if d["issuer"] != testIssuer || d["authorization_endpoint"] != testIssuer+"/authorize" {
		t.Fatalf("discovery = %v", d)
	}
}

func TestNormalLoginVerifies(t *testing.T) {
	s, ts, clk := newTestServer(t)
	status, body := login(t, ts.URL, "S256", "verifier-0123456789-abcdefghijklmnopqrstuvwxyz", testSecret)
	if status != http.StatusOK {
		t.Fatalf("token：%d %v", status, body)
	}
	if err := verify(body["id_token"].(string), fetchKeys(t, ts.URL), "n-1", clk.Now()); err != nil {
		t.Fatalf("正常的 id_token 驗證失敗：%v", err)
	}
	reqs := s.TokenRequests()
	if len(reqs) != 1 || !reqs[0].PKCEOK || !reqs[0].ClientAuthOK || reqs[0].PKCEMethod != "S256" {
		t.Fatalf("token 請求紀錄 = %+v", reqs)
	}
}

func TestFaultsFailVerification(t *testing.T) {
	s, ts, clk := newTestServer(t)
	keys := fetchKeys(t, ts.URL)
	accepted := map[Fault]bool{FaultExpired30s: true, FaultFutureIat30s: true}
	for _, f := range Faults {
		tok, err := s.IDToken("user-7f3a", "n-1", f)
		if err != nil {
			t.Fatal(err)
		}
		err = verify(tok, keys, "n-1", clk.Now())
		if accepted[f] && err != nil {
			t.Errorf("%q 應該在 60 秒的時鐘偏差內被接受：%v", f, err)
		}
		if !accepted[f] && err == nil {
			t.Errorf("%q 應該驗證失敗", f)
		}
	}
}

func TestFaultAppliesToTokenEndpoint(t *testing.T) {
	s, ts, clk := newTestServer(t)
	s.SetFault(FaultWrongNonce)
	_, body := login(t, ts.URL, "S256", "verifier-0123456789-abcdefghijklmnopqrstuvwxyz", testSecret)
	if err := verify(body["id_token"].(string), fetchKeys(t, ts.URL), "n-1", clk.Now()); err == nil {
		t.Fatal("錯誤的 nonce 應該驗證失敗")
	}
}

func TestPlainPKCEAndWrongSecretAreRefused(t *testing.T) {
	s, ts, _ := newTestServer(t)
	if status, _ := login(t, ts.URL, "plain", "verifier-0123456789-abcdefghijklmnopqrstuvwxyz", testSecret); status != http.StatusBadRequest {
		t.Errorf("plain PKCE：%d，want 400", status)
	}
	if status, _ := login(t, ts.URL, "S256", "verifier-0123456789-abcdefghijklmnopqrstuvwxyz", "wrong"); status != http.StatusUnauthorized {
		t.Errorf("錯誤的 client secret：%d，want 401", status)
	}
	reqs := s.TokenRequests()
	if len(reqs) != 2 || reqs[0].PKCEOK || reqs[0].PKCEMethod != "plain" || reqs[1].ClientAuthOK {
		t.Fatalf("token 請求紀錄 = %+v", reqs)
	}
}

func TestCodeIsSingleUse(t *testing.T) {
	_, ts, _ := newTestServer(t)
	q := url.Values{
		"response_type": {"code"}, "client_id": {testClient}, "scope": {"openid"},
		"redirect_uri": {"https://n.example/cb"}, "state": {"s"}, "nonce": {"n"},
		"code_challenge": {S256("v-0123456789-0123456789-0123456789-0123")}, "code_challenge_method": {"S256"},
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(ts.URL + "/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	exchange := func() int {
		form := url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")},
			"code_verifier": {"v-0123456789-0123456789-0123456789-0123"}, "redirect_uri": {"https://n.example/cb"}}
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth(testClient, testSecret)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if got := exchange(); got != http.StatusOK {
		t.Fatalf("第一次：%d", got)
	}
	if got := exchange(); got != http.StatusBadRequest {
		t.Fatalf("重複使用 code：%d，want 400", got)
	}
}

func TestDenyRedirectsWithError(t *testing.T) {
	s, ts, _ := newTestServer(t)
	s.Deny("access_denied")
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	q := url.Values{"response_type": {"code"}, "client_id": {testClient}, "scope": {"openid"},
		"redirect_uri": {"https://n.example/cb"}, "state": {"s-9"}, "code_challenge": {"x"}, "code_challenge_method": {"S256"}}
	resp, err := noFollow.Get(ts.URL + "/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("error") != "access_denied" || loc.Query().Get("state") != "s-9" || loc.Query().Get("code") != "" {
		t.Fatalf("Location = %s", loc)
	}
}

func TestParseFault(t *testing.T) {
	if f, err := ParseFault("an HMAC signature"); err != nil || f != FaultHMAC {
		t.Fatalf("ParseFault = %q, %v", f, err)
	}
	if _, err := ParseFault("bogus"); err == nil {
		t.Fatal("不認得的缺陷應該回傳錯誤")
	}
}
