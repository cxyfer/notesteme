package fakenet

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"sync"
	"time"
)

// 測試 CA 在程序內只產生一次，私鑰不落地，也不會寫入 repo。
var (
	caOnce sync.Once
	caCert *x509.Certificate
	caKey  *ecdsa.PrivateKey
	caErr  error

	leafMu    sync.Mutex
	leafCache = map[string]*tls.Certificate{}
)

func initCA() {
	caOnce.Do(func() {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			caErr = err
			return
		}
		tmpl := &x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: "notesteme fakenet test CA"},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
			IsCA:                  true,
			BasicConstraintsValid: true,
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			caErr = err
			return
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			caErr = err
			return
		}
		caCert, caKey = cert, key
	})
}

// issueLeaf 依主機名稱（或 IP 字面值）簽發伺服器憑證，結果會快取。
// 用真實時間作為憑證有效期，因為 TLS 驗證使用的是系統時間，與 fake clock 無關。
func issueLeaf(name string, extraIPs []net.IP) (*tls.Certificate, error) {
	initCA()
	if caErr != nil {
		return nil, caErr
	}
	leafMu.Lock()
	defer leafMu.Unlock()
	if c, ok := leafCache[name]; ok {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(name); ip != nil {
		tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
	} else {
		tmpl.DNSNames = []string{name}
	}
	tmpl.IPAddresses = append(tmpl.IPAddresses, extraIPs...)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	leafCache[name] = c
	return c, nil
}

// RootCAs 回傳信任測試 CA 的憑證池，用於 netguard.Deps.RootCAs。
func RootCAs() *x509.CertPool {
	initCA()
	pool := x509.NewCertPool()
	if caCert != nil {
		pool.AddCert(caCert)
	}
	return pool
}
