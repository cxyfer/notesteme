package oidc

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"
)

// MaxTokenBytes 是 id_token 的大小上限。
const MaxTokenBytes = 16 << 10

// Leeway 是 exp、iat 的時鐘偏差容許值（AUTH-R18、待決問題 A4）。
const Leeway = 60 * time.Second

// minRSABits 是接受的 RSA 金鑰長度下限。
const minRSABits = 2048

// 接受的簽章演算法（AUTH-R18）：none 與 HS* 一律拒絕，不碰任何金鑰。
const (
	algRS256 = "RS256"
	algES256 = "ES256"
)

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type jwt struct {
	header       jwtHeader
	claims       rawClaims
	signingInput []byte
	signature    []byte
}

// audience 接受字串或字串陣列（RFC 7519 4.1.3）。
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*a = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

type rawClaims struct {
	Iss   string          `json:"iss"`
	Sub   string          `json:"sub"`
	Aud   audience        `json:"aud"`
	Azp   *string         `json:"azp"`
	Exp   json.RawMessage `json:"exp"`
	Iat   json.RawMessage `json:"iat"`
	Nonce string          `json:"nonce"`
}

func decodeSegment(s string) ([]byte, error) {
	// RFC 7515：base64url，不帶 padding。
	if strings.ContainsAny(s, "=+/") {
		return nil, ErrMalformed
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrMalformed
	}
	return b, nil
}

func strictJSON(b []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return ErrMalformed
	}
	if dec.More() {
		return ErrMalformed
	}
	return nil
}

// parseJWT 拆開 JWS compact 格式並檢查演算法；不驗證簽章。
func parseJWT(raw string) (*jwt, error) {
	if len(raw) == 0 || len(raw) > MaxTokenBytes {
		return nil, ErrMalformed
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return nil, ErrMalformed
	}
	hb, err := decodeSegment(parts[0])
	if err != nil {
		return nil, err
	}
	var h jwtHeader
	if err := strictJSON(hb, &h); err != nil {
		return nil, err
	}
	// 演算法先檢查：alg: none 的簽章是空的，在這裡就拒絕。
	if h.Alg != algRS256 && h.Alg != algES256 {
		return nil, ErrAlgorithm
	}
	if h.Kid == "" {
		return nil, ErrUnknownKey
	}
	if parts[2] == "" {
		return nil, ErrMalformed
	}
	cb, err := decodeSegment(parts[1])
	if err != nil {
		return nil, err
	}
	var c rawClaims
	if err := strictJSON(cb, &c); err != nil {
		return nil, err
	}
	sig, err := decodeSegment(parts[2])
	if err != nil {
		return nil, err
	}
	return &jwt{header: h, claims: c, signingInput: []byte(parts[0] + "." + parts[1]), signature: sig}, nil
}

// verifySignature 以金鑰驗證簽章；演算法必須和金鑰的種類一致（RS256 配 RSA、ES256 配 P-256）。
func (t *jwt) verifySignature(k jwk) error {
	if k.alg != "" && k.alg != t.header.Alg {
		return ErrAlgorithm
	}
	sum := sha256.Sum256(t.signingInput)
	switch t.header.Alg {
	case algRS256:
		if k.rsa == nil {
			return ErrAlgorithm
		}
		if rsa.VerifyPKCS1v15(k.rsa, crypto.SHA256, sum[:], t.signature) != nil {
			return ErrSignature
		}
	case algES256:
		if k.ec == nil {
			return ErrAlgorithm
		}
		// JWS 的 ES256 簽章是固定 64 bytes 的 r‖s（RFC 7518 3.4），不是 ASN.1 DER。
		if len(t.signature) != 64 {
			return ErrSignature
		}
		r := new(big.Int).SetBytes(t.signature[:32])
		s := new(big.Int).SetBytes(t.signature[32:])
		if !ecdsa.Verify(k.ec, sum[:], r, s) {
			return ErrSignature
		}
	default:
		return ErrAlgorithm
	}
	return nil
}

// Expect 是 claim 的驗證條件。
type Expect struct {
	Issuer   string
	ClientID string
	Nonce    string
	Now      time.Time
}

// numericDate 解析 NumericDate（秒，可以有小數）；必須是 JSON 數字（不接受字串、null），而且是有限的非負值。
func numericDate(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) {
		return time.Time{}, false
	}
	f, err := json.Number(raw).Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1e11 {
		return time.Time{}, false
	}
	sec, frac := math.Modf(f)
	return time.Unix(int64(sec), int64(frac*1e9)), true
}

// check 檢查 claim（AUTH-R18）。
func (c rawClaims) check(want Expect) (Claims, error) {
	if c.Iss != want.Issuer {
		return Claims{}, ErrIssuer
	}
	if want.ClientID == "" || len(c.Aud) == 0 {
		return Claims{}, ErrAudience
	}
	found := false
	for _, a := range c.Aud {
		if a == want.ClientID {
			found = true
		}
	}
	if !found {
		return Claims{}, ErrAudience
	}
	// 有多個 aud 時 azp 必須是 client_id；只有一個時，azp 出現也必須相同。
	if len(c.Aud) > 1 && (c.Azp == nil || *c.Azp != want.ClientID) {
		return Claims{}, ErrAZP
	}
	if c.Azp != nil && *c.Azp != want.ClientID {
		return Claims{}, ErrAZP
	}
	if want.Nonce == "" || subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(want.Nonce)) != 1 {
		return Claims{}, ErrNonce
	}
	exp, ok := numericDate(c.Exp)
	if !ok {
		return Claims{}, fmt.Errorf("%w（缺少 exp）", ErrMalformed)
	}
	// exp 必須晚於「現在減 60 秒」。
	if !exp.After(want.Now.Add(-Leeway)) {
		return Claims{}, ErrExpired
	}
	iat, ok := numericDate(c.Iat)
	if !ok {
		return Claims{}, fmt.Errorf("%w（缺少 iat）", ErrMalformed)
	}
	// iat 必須早於「現在加 60 秒」。
	if !iat.Before(want.Now.Add(Leeway)) {
		return Claims{}, ErrNotYet
	}
	if c.Sub == "" {
		return Claims{}, fmt.Errorf("%w（缺少 sub）", ErrMalformed)
	}
	return Claims{Issuer: c.Iss, Subject: c.Sub}, nil
}

// ---- JWKS ----

// jwk 是一把可以用來驗證簽章的公鑰；rsa 與 ec 只有一個不是 nil。
type jwk struct {
	alg string
	rsa *rsa.PublicKey
	ec  *ecdsa.PublicKey
}

type rawJWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// ParseJWKS 解析 JWKS，回傳 kid 對應的金鑰；略過不能用來驗證 RS256／ES256 簽章的金鑰。
// 同一個 kid 出現多次時整個 kid 不採用（無法判斷該用哪一把）。
func ParseJWKS(b []byte) (map[string]jwk, error) {
	var set struct {
		Keys []rawJWK `json:"keys"`
	}
	if err := json.Unmarshal(b, &set); err != nil {
		return nil, ErrJWKS
	}
	out := map[string]jwk{}
	dup := map[string]bool{}
	for _, k := range set.Keys {
		if k.Kid == "" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		var key jwk
		var ok bool
		switch k.Kty {
		case "RSA":
			key, ok = parseRSA(k)
		case "EC":
			key, ok = parseEC(k)
		}
		if !ok {
			continue
		}
		if _, seen := out[k.Kid]; seen || dup[k.Kid] {
			dup[k.Kid] = true
			delete(out, k.Kid)
			continue
		}
		out[k.Kid] = key
	}
	return out, nil
}

func parseRSA(k rawJWK) (jwk, bool) {
	if k.Alg != "" && k.Alg != algRS256 {
		return jwk{}, false
	}
	nb, err1 := base64.RawURLEncoding.DecodeString(k.N)
	eb, err2 := base64.RawURLEncoding.DecodeString(k.E)
	if err1 != nil || err2 != nil || len(eb) == 0 || len(eb) > 4 || len(nb) > 2048 {
		return jwk{}, false
	}
	n := new(big.Int).SetBytes(nb)
	e := int(new(big.Int).SetBytes(eb).Int64())
	if n.BitLen() < minRSABits || e < 3 || e%2 == 0 {
		return jwk{}, false
	}
	return jwk{alg: k.Alg, rsa: &rsa.PublicKey{N: n, E: e}}, true
}

func parseEC(k rawJWK) (jwk, bool) {
	if (k.Alg != "" && k.Alg != algES256) || k.Crv != "P-256" {
		return jwk{}, false
	}
	x, err1 := base64.RawURLEncoding.DecodeString(k.X)
	y, err2 := base64.RawURLEncoding.DecodeString(k.Y)
	if err1 != nil || err2 != nil || len(x) != 32 || len(y) != 32 {
		return jwk{}, false
	}
	// ParseUncompressedPublicKey 會檢查點在曲線上。
	pub, err := ecdsa.ParseUncompressedPublicKey(ellipticP256(), append(append([]byte{4}, x...), y...))
	if err != nil {
		return jwk{}, false
	}
	return jwk{alg: k.Alg, ec: pub}, true
}

func ellipticP256() elliptic.Curve { return elliptic.P256() }
