package fakenet

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
)

// 全域的違規紀錄：繞過 netguard、直接使用預設 transport 或 resolver 的連線都會記在這裡。
var (
	trapMu         sync.Mutex
	trapViolations []string
)

func recordTrap(msg string) {
	trapMu.Lock()
	trapViolations = append(trapViolations, msg)
	trapMu.Unlock()
}

// TrapViolations 回傳目前累積的違規（複本）。
func TrapViolations() []string {
	trapMu.Lock()
	defer trapMu.Unlock()
	return append([]string(nil), trapViolations...)
}

// DrainTrapViolations 回傳累積的違規並清空，供每個場景結束時檢查使用。
func DrainTrapViolations() []string {
	trapMu.Lock()
	defer trapMu.Unlock()
	v := trapViolations
	trapViolations = nil
	return v
}

type trapTransport struct{}

func (trapTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	msg := fmt.Sprintf("uncontrolled outbound HTTP request via http.DefaultTransport: %s %s", req.Method, req.URL.Redacted())
	recordTrap(msg)
	return nil, fmt.Errorf("fakenet trap: %s", msg)
}

// InstallTrap 把 http.DefaultTransport 換成一律失敗並記錄違規的版本，
// 並把 net.DefaultResolver 換成 PreferGo 且 Dial 一律失敗的版本。
// 回傳的 restore 還原原本的設定。
//
// 這會修改全域變數，只能在 TestMain 之類的單一位置呼叫；
// 場景之間不要重複安裝或還原。
func InstallTrap() (restore func()) {
	oldTransport := http.DefaultTransport
	oldResolver := net.DefaultResolver
	http.DefaultTransport = trapTransport{}
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(_ context.Context, network, address string) (net.Conn, error) {
			msg := fmt.Sprintf("uncontrolled DNS lookup via net.DefaultResolver: %s %s", network, address)
			recordTrap(msg)
			return nil, fmt.Errorf("fakenet trap: %s", msg)
		},
	}
	return func() {
		http.DefaultTransport = oldTransport
		net.DefaultResolver = oldResolver
	}
}
