package redisclient

import (
	"bufio"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// stubServer 是一个只按顺序吐预设回复的最小 Redis 协议桩，
// 用于离线验证 writeCommand/readReply 的成帧与解析。
type stubServer struct {
	ln net.Listener
}

func newStubServer(t *testing.T, handler func(br *bufio.Reader, bw *bufio.Writer)) *stubServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &stubServer{ln: ln}
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer nc.Close()
				br := bufio.NewReader(nc)
				bw := bufio.NewWriter(nc)
				handler(br, bw)
			}()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *stubServer) addr() string { return s.ln.Addr().String() }

// readCommand 读回一条完整命令（数量、每段长度、内容）。
func readCommand(br *bufio.Reader) ([]string, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "*") {
		return nil, nil
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		l, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		l = strings.TrimRight(l, "\r\n")
		if !strings.HasPrefix(l, "$") {
			return nil, nil
		}
		ln, err := strconv.Atoi(l[1:])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, ln+2)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:ln]))
	}
	return args, nil
}

func TestProtocolRoundTrip(t *testing.T) {
	var want [][]string
	getCount := 0
	s := newStubServer(t, func(br *bufio.Reader, bw *bufio.Writer) {
		for {
			args, err := readCommand(br)
			if err != nil {
				return
			}
			if args == nil {
				return
			}
			want = append(want, args)
			var reply string
			switch {
			case len(args) > 0 && args[0] == "PING":
				reply = "+OK\r\n"
			case len(args) > 0 && args[0] == "GET":
				// 第一次 miss，第二次 hit：覆盖 $-1 与 $n。
				getCount++
				if getCount%2 == 1 {
					reply = "$-1\r\n"
				} else {
					reply = "$5\r\nhello\r\n"
				}
			case len(args) > 0 && args[0] == "SET":
				reply = "+OK\r\n"
			case len(args) > 0 && args[0] == "DEL":
				reply = ":1\r\n"
			case len(args) > 0 && args[0] == "EVAL":
				reply = ":3\r\n"
			default:
				reply = "-ERR unknown\r\n"
			}
			if _, err := bw.WriteString(reply); err != nil {
				return
			}
			bw.Flush()
		}
	})

	c := Open(s.addr(), "")
	defer c.Close()

	if err := c.Ping(); err != nil {
		t.Fatalf("PING: %v", err)
	}
	if _, ok, err := c.Get("k"); err != nil || ok {
		t.Fatalf("GET miss: ok=%v err=%v", ok, err)
	}
	v, ok, err := c.Get("k")
	if err != nil || !ok || v != "hello" {
		t.Fatalf("GET hit: v=%q ok=%v err=%v", v, ok, err)
	}
	if err := c.SetEx("k", "v\r\nwith-crlf", 60); err != nil {
		t.Fatalf("SET EX: %v", err)
	}
	if err := c.Del("k"); err != nil {
		t.Fatalf("DEL: %v", err)
	}
	if n, err := c.IncrWindow("k", 30); err != nil || n != 3 {
		t.Fatalf("EVAL: n=%d err=%v", n, err)
	}

	// 校验成帧：命令名、参数数量、含 CRLF 的值长度按字节数传输。
	if len(want) != 6 {
		t.Fatalf("收到 %d 条命令，想 6 条", len(want))
	}
	if want[3][0] != "SET" || want[3][2] != "v\r\nwith-crlf" {
		t.Fatalf("SET 帧不符: %q", want[3])
	}
	// EVAL 帧为 [script, numkeys, key, ttl]。
	if want[5][0] != "EVAL" || want[5][3] != "k" || want[5][4] != "30" {
		t.Fatalf("EVAL 帧不符: %q", want[5])
	}
}

func TestReadReplyErrors(t *testing.T) {
	// 错误回复（-）转 error；非法行结尾报错。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				nc.Write([]byte("-ERR nope\r\n"))
				time.Sleep(50 * time.Millisecond)
				nc.Close()
			}()
		}
	}()
	c2 := Open(ln.Addr().String(), "")
	defer c2.Close()
	if err := c2.Ping(); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("期待 -ERR 转 error，got %v", err)
	}
}

func TestFromEnvNilWhenUnset(t *testing.T) {
	t.Setenv("EYVESCLOUD_REDIS_ADDR", "")
	if FromEnv() != nil {
		t.Fatal("ADDR 为空必须返回 nil")
	}
}

func TestDialFailureFailsFast(t *testing.T) {
	c := Open("127.0.0.1:1", "")
	defer c.Close()
	start := time.Now()
	if err := c.Ping(); err == nil {
		t.Fatal("连接拒绝必须报错")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("连接拒绝应立即失败，耗时 %v", d)
	}
}

// TestRealRedis 是真 Redis 集成测试，仅当 EYVESCLOUD_REDIS_TEST_ADDR 设置时运行。
func TestRealRedis(t *testing.T) {
	addr := strings.TrimSpace(testRedisAddr())
	if addr == "" {
		t.Skip("EYVESCLOUD_REDIS_TEST_ADDR 未设置，跳过真 Redis 集成测试")
	}
	c := Open(addr, "")
	defer c.Close()
	if err := c.Ping(); err != nil {
		t.Fatalf("PING: %v", err)
	}
	key := "eyves:test:" + strconv.FormatInt(time.Now().UnixNano(), 10)
	defer c.Del(key)

	if _, ok, err := c.Get(key); err != nil || ok {
		t.Fatalf("初始 miss: ok=%v err=%v", ok, err)
	}
	if err := c.SetEx(key, "1", 2); err != nil {
		t.Fatalf("SET EX: %v", err)
	}
	if v, ok, err := c.Get(key); err != nil || !ok || v != "1" {
		t.Fatalf("GET: v=%q ok=%v err=%v", v, ok, err)
	}
	n1, err := c.IncrWindow(key, 2)
	if err != nil || n1 != 2 {
		t.Fatalf("INCR 后计数应为 2: %d %v", n1, err)
	}
	if err := c.Del(key); err != nil {
		t.Fatalf("DEL: %v", err)
	}
	if _, ok, _ := c.Get(key); ok {
		t.Fatal("DEL 后键应不存在")
	}
}

func testRedisAddr() string {
	return strings.TrimSpace(os.Getenv("EYVESCLOUD_REDIS_TEST_ADDR"))
}
