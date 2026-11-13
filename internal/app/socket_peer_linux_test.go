//go:build linux

package app

import (
	"io"
	"log/slog"
	"net"
	"testing"
)

// scriptListener 依序回傳預先準備的連線，用完後回傳 net.ErrClosed。
type scriptListener struct {
	net.Listener
	conns []net.Conn
}

func (l *scriptListener) Accept() (net.Conn, error) {
	if len(l.conns) == 0 {
		return nil, net.ErrClosed
	}
	c := l.conns[0]
	l.conns = l.conns[1:]
	return c, nil
}

// 對端檢查不通過的連線在 Accept 內就被關閉，不會交給 http.Server。
func TestPeerListenerDropsRejectedConn(t *testing.T) {
	rejected, other := net.Pipe() // 不是 *net.UnixConn：checkPeer 一律拒絕
	defer other.Close()
	l := &peerListener{
		Listener: &scriptListener{conns: []net.Conn{rejected}},
		log:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
	if c, err := l.Accept(); err == nil {
		_ = c.Close()
		t.Fatal("被拒絕的連線不應該被 Accept 回傳")
	}
	if _, err := rejected.Write([]byte("x")); err == nil {
		t.Error("被拒絕的連線應該已經關閉")
	}
}
