package api

import (
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestDialSSHWithTimeout_TCPAcceptsNoHandshake 验证：当目标 TCP 端口可接受连接、
// 但从不回应 SSH 版本交换握手时，dialSSHWithTimeout 会在超时后返回错误，
// 而不是像原 ssh.Dial 那样无限阻塞（导致 WebSSH 永远卡在 preparing）。
func TestDialSSHWithTimeout_TCPAcceptsNoHandshake(t *testing.T) {
	// 起一个只 accept、不读写、不回应的假 SSH 服务。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// 什么都不做，让连接挂起但想要握手的客户端读不到任何字节。
			defer conn.Close()
		}
	}()

	sshConfig := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.Password("dummy")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	addr := ln.Addr().String()
	start := time.Now()
	client, err := dialSSHWithTimeout(addr, sshConfig, 2*time.Second)
	elapsed := time.Since(start)

	// 必须在合理时间内返回（超时应在 2s + 少量余量内触发）。
	if elapsed > 6*time.Second {
		t.Fatalf("dialSSHWithTimeout took %.2fs; expected an early timeout, likely stuck", elapsed.Seconds())
	}
	if client != nil {
		client.Close()
		t.Fatal("expected no client since handshake never completes")
	}
	if err == nil {
		t.Fatal("expected a handshake timeout error")
	}
	t.Logf("received handshake error in %.2fs: %v", elapsed.Seconds(), err)

	_ = done
}