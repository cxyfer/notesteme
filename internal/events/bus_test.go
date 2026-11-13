package events

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func ev(n int) Event {
	return Event{Type: TypeStageStatus, Item: "yt:axxxxxxxxxx", ItemID: "i", Stage: "metadata", Status: fmt.Sprint(n)}
}

func recv(t *testing.T, s *Subscription) Event {
	t.Helper()
	select {
	case e := <-s.C:
		return e
	case <-time.After(2 * time.Second):
		t.Error("等不到事件")
		return Event{}
	}
}

func isDone(s *Subscription) bool {
	select {
	case <-s.Done():
		return true
	default:
		return false
	}
}

func TestFanOutToAllSubscribers(t *testing.T) {
	b := New(4)
	s1, _ := b.Subscribe()
	s2, _ := b.Subscribe()
	b.Publish(ev(1))
	b.Publish(ev(2))
	for _, s := range []*Subscription{s1, s2} {
		if got := recv(t, s).Status; got != "1" {
			t.Errorf("第一個事件 = %s", got)
		}
		if got := recv(t, s).Status; got != "2" {
			t.Errorf("第二個事件 = %s", got)
		}
	}
}

func TestPublishWithoutSubscribers(t *testing.T) {
	New(1).Publish(ev(1)) // 不應阻塞或 panic
}

func TestSlowSubscriberIsDroppedWithoutBlocking(t *testing.T) {
	b := New(2)
	slow, _ := b.Subscribe()
	fast, _ := b.Subscribe()

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for i := 0; i < 10; i++ {
			b.Publish(ev(i))
			recv(t, fast) // fast 一直有在讀
		}
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("慢的訂閱者拖住了發布端")
	}
	if !isDone(slow) {
		t.Error("buffer 滿了的訂閱者應該被斷開")
	}
	if isDone(fast) {
		t.Error("跟得上的訂閱者不應被斷開")
	}
	// 被斷開的訂閱者仍可讀到斷開前已緩衝的事件。
	if got := recv(t, slow).Status; got != "0" {
		t.Errorf("被斷開前的第一個事件 = %s", got)
	}
}

func TestCloseSubscription(t *testing.T) {
	b := New(1)
	s, _ := b.Subscribe()
	s.Close()
	s.Close() // 可重複呼叫
	if !isDone(s) {
		t.Fatal("Close 後 Done 應該關閉")
	}
	b.Publish(ev(1)) // 已退訂，不應 panic 或阻塞
}

func TestCloseBus(t *testing.T) {
	b := New(1)
	s, _ := b.Subscribe()
	b.Close()
	b.Close()
	if !isDone(s) {
		t.Fatal("Bus 關閉後訂閱應該結束")
	}
	if _, ok := b.Subscribe(); ok {
		t.Error("Bus 關閉後不應再接受訂閱")
	}
	b.Publish(ev(1))
}

func TestConcurrentPublishSubscribeClose(t *testing.T) {
	b := New(8)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for p := 0; p < 4; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
					b.Publish(ev(i))
				}
			}
		}()
	}
	var subs sync.WaitGroup
	for c := 0; c < 8; c++ {
		subs.Add(1)
		go func() {
			defer subs.Done()
			for i := 0; i < 200; i++ {
				s, ok := b.Subscribe()
				if !ok {
					return
				}
				// 有時讀、有時不讀，製造被斷開的情況。
				if i%2 == 0 {
					select {
					case <-s.C:
					case <-s.Done():
					case <-time.After(time.Second):
					}
				}
				s.Close()
			}
		}()
	}
	subs.Wait()
	b.Close()
	close(stop)
	wg.Wait()
}
