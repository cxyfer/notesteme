// Package websession 是 Client（internal/auth）與 Hub（internal/hub/hubauth）共用的瀏覽器 session 工具：
// 隨機 token、token 的 hash 與由 session token 推導的 CSRF token（AUTH-R5、HACC-R8）。
//
// 這個套件不能 import 任何 domain 套件（store、pipeline 等），Hub 才能使用（ADR-0030 第 1 點）。
package websession

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// CSRFHeader 是非 GET 的 session 請求帶 CSRF token 的標頭（Client 與 Hub 相同）。
const CSRFHeader = "X-CSRF-Token"

// ErrCSRF 表示 session 請求缺少或帶了不正確的 CSRF token。
var ErrCSRF = errors.New("CSRF token 缺少或不正確。")

// RandomToken 產生 n bytes 的隨機值，以 base64url（無 padding）編碼。
func RandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken 回傳 token 的 SHA-256（hex）。token 本身是高熵隨機值，不需要慢雜湊。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CSRFFor 由 session token 推導 CSRF token。domain separator 不能改：改了之後既有 session 的 CSRF 全部失效。
func CSRFFor(sessionToken string) string {
	sum := sha256.Sum256([]byte("notesteme-csrf\x00" + sessionToken))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// CheckCSRF 檢查非 GET 的 session 請求：標頭必須等於 cookie，且等於由 session token 推導的值。
// 推導值讓攻擊者就算能寫入 cookie（例如子網域），沒有 HttpOnly 的 session token 也無法偽造。
func CheckCSRF(sessionToken, cookieValue, headerValue string) error {
	if sessionToken == "" || cookieValue == "" || headerValue == "" {
		return ErrCSRF
	}
	want := CSRFFor(sessionToken)
	if subtle.ConstantTimeCompare([]byte(headerValue), []byte(cookieValue)) != 1 ||
		subtle.ConstantTimeCompare([]byte(headerValue), []byte(want)) != 1 {
		return ErrCSRF
	}
	return nil
}
