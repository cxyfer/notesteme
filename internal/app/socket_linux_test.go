//go:build linux

package app

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestPeerAllowed(t *testing.T) {
	tests := []struct {
		peer, self uint32
		want       bool
	}{
		{1000, 1000, true}, // 同一個使用者
		{0, 1000, true},    // root
		{1001, 1000, false},
		{65534, 1000, false},
		{1000, 0, false}, // 以 root 執行時，其他使用者仍然不行
		{0, 0, true},
	}
	for _, tt := range tests {
		if got := peerAllowed(tt.peer, tt.self); got != tt.want {
			t.Errorf("peerAllowed(%d, %d) = %v，預期 %v", tt.peer, tt.self, got, tt.want)
		}
	}
}

// 同一個使用者的連線通過 SO_PEERCRED 檢查；不是 Unix socket 的連線被拒絕。
func TestCheckPeerSameUser(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "s.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		done <- c
	}()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-done
	defer server.Close()

	if err := checkPeer(server); err != nil {
		t.Errorf("同一個使用者（uid %d）應該通過：%v", os.Geteuid(), err)
	}
	if err := checkPeer(&net.TCPConn{}); err == nil {
		t.Error("不是 Unix socket 的連線應該被拒絕")
	}
}
