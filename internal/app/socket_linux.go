//go:build linux

package app

import (
	"errors"
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// sockPathMax 是 Unix socket 路徑的長度上限：sockaddr_un.sun_path 有 108 位元組，含結尾的 NUL。
const sockPathMax = 107

// checkPeer 以 SO_PEERCRED 取得對端的 uid，只接受與本程序相同的使用者或 root。
func checkPeer(c net.Conn) error {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return errors.New("不是 Unix socket 連線")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return fmt.Errorf("取得連線的 fd 失敗：%w", err)
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return fmt.Errorf("取得連線的 fd 失敗：%w", err)
	}
	if credErr != nil {
		return fmt.Errorf("讀取對端身分失敗：%w", credErr)
	}
	if !peerAllowed(cred.Uid, uint32(os.Geteuid())) {
		return fmt.Errorf("對端 uid %d 不是 %d 也不是 root", cred.Uid, os.Geteuid())
	}
	return nil
}

// peerAllowed 回報對端的 uid 能不能使用本機 socket：與 self 相同或是 root（0）。
func peerAllowed(peer, self uint32) bool { return peer == self || peer == 0 }
