package netguard

import (
	"testing"
	"time"
)

// GRD-R5：連線之後的各階段也要有逾時，否則惡意伺服器可以停在 TLS 握手或不送回應標頭而佔住連線。
func TestTransportHasHandshakeAndHeaderTimeouts(t *testing.T) {
	tr := Transport(Deps{})
	if tr.TLSHandshakeTimeout != 10*time.Second {
		t.Errorf("TLSHandshakeTimeout = %v，應為 10s", tr.TLSHandshakeTimeout)
	}
	if tr.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v，應為 30s", tr.ResponseHeaderTimeout)
	}
}
