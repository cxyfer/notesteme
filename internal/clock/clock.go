// Package clock 提供可注入的時鐘。
//
// production 程式碼一律透過 Clock 取得時間與計時器，不直接呼叫 time.Now；
// 測試改用 Fake，讓重試、租約、鎖定等行為不需要真實等待（NFR-TEST-2）。
package clock

import "time"

// Clock 是程式取得時間的唯一入口。
type Clock interface {
	// Now 回傳目前時間。
	Now() time.Time
	// NewTimer 建立一個 d 之後到期的計時器。
	NewTimer(d time.Duration) Timer
}

// Timer 是一次性的計時器，語意與 time.Timer 相近。
type Timer interface {
	// C 回傳到期時會收到時間的 channel。
	C() <-chan time.Time
	// Stop 停止計時器；回傳 true 代表停止前尚未到期。
	Stop() bool
}

// Real 是 production 使用的實作，直接委派給 time 套件。
type Real struct{}

// Now 回傳系統時間。
func (Real) Now() time.Time { return time.Now() }

// NewTimer 建立以系統時間運作的計時器。
func (Real) NewTimer(d time.Duration) Timer { return realTimer{t: time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time { return r.t.C }
func (r realTimer) Stop() bool          { return r.t.Stop() }
