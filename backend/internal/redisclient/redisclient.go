// Package redisclient 实现面板 P4「无状态 API」协作件所需的最小 Redis 客户端。
// 只覆盖 PING/GET/SET(EX)/DEL/EVAL 五条命令（RESP2），不引入第三方依赖树；
// 连接按需建立、空闲复用、出错即弃，命令级超时防止 Redis 抖动拖死请求路径。
package redisclient

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	dialTimeout = 2 * time.Second
	ioTimeout   = 2 * time.Second
	maxIdle     = 8
)

// IncrWindowScript 原子「自增 + 首次自增时设 TTL」的固定窗口计数器。
// 不用 EXPIRE NX 选项（Redis 7+），兼容发行版自带的 Redis 6.x。
const IncrWindowScript = `local v=redis.call('INCR',KEYS[1]) if v==1 then redis.call('EXPIRE',KEYS[1],ARGV[1]) end return v`

// Client 是并发安全的极简 Redis 客户端。
type Client struct {
	addr     string
	password string

	mu   sync.Mutex
	idle []*conn
}

type conn struct {
	nc net.Conn
	br *bufio.Reader
	bw *bufio.Writer
}

// Open 惰性建连；addr 形如 host:port。不做连通性检查——启动不依赖 Redis 在线。
func Open(addr, password string) *Client {
	return &Client{addr: addr, password: password}
}

// FromEnv 读 EYVESCLOUD_REDIS_ADDR（host:port）与可选 EYVESCLOUD_REDIS_PASSWORD；
// ADDR 未设置返回 nil，调用方以此判定「未启用分布式模式」。
func FromEnv() *Client {
	addr := strings.TrimSpace(os.Getenv("EYVESCLOUD_REDIS_ADDR"))
	if addr == "" {
		return nil
	}
	return Open(addr, strings.TrimSpace(os.Getenv("EYVESCLOUD_REDIS_PASSWORD")))
}

// Close 关闭空闲连接。
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cn := range c.idle {
		cn.nc.Close()
	}
	c.idle = nil
}

func (c *Client) acquire() (*conn, error) {
	c.mu.Lock()
	if n := len(c.idle); n > 0 {
		cn := c.idle[n-1]
		c.idle = c.idle[:n-1]
		c.mu.Unlock()
		return cn, nil
	}
	c.mu.Unlock()

	nc, err := net.DialTimeout("tcp", c.addr, dialTimeout)
	if err != nil {
		return nil, err
	}
	cn := &conn{nc: nc, br: bufio.NewReader(nc), bw: bufio.NewWriter(nc)}
	if c.password != "" {
		if err := writeCommand(cn.bw, "AUTH", c.password); err != nil {
			nc.Close()
			return nil, err
		}
		if err := cn.bw.Flush(); err != nil {
			nc.Close()
			return nil, err
		}
		if _, err := readReply(cn.br); err != nil {
			nc.Close()
			return nil, err
		}
	}
	return cn, nil
}

func (c *Client) release(cn *conn) {
	c.mu.Lock()
	if len(c.idle) < maxIdle {
		c.idle = append(c.idle, cn)
		cn = nil
	}
	c.mu.Unlock()
	if cn != nil {
		cn.nc.Close()
	}
}

func (c *Client) exec(args ...string) (any, error) {
	cn, err := c.acquire()
	if err != nil {
		return nil, err
	}
	cn.nc.SetDeadline(time.Now().Add(ioTimeout))
	if err := writeCommand(cn.bw, args...); err == nil {
		err = cn.bw.Flush()
	}
	var reply any
	if err == nil {
		reply, err = readReply(cn.br)
	}
	if err != nil {
		cn.nc.Close()
		return nil, err
	}
	c.release(cn)
	return reply, nil
}

// Ping 探活。
func (c *Client) Ping() error {
	_, err := c.exec("PING")
	return err
}

// Get 返回键值；ok=false 表示键不存在。
func (c *Client) Get(key string) (string, bool, error) {
	reply, err := c.exec("GET", key)
	if err != nil {
		return "", false, err
	}
	s, ok := reply.(string)
	return s, ok, nil
}

// SetEx 带秒级 TTL 写入。
func (c *Client) SetEx(key, val string, ttlSeconds int64) error {
	_, err := c.exec("SET", key, val, "EX", strconv.FormatInt(ttlSeconds, 10))
	return err
}

// Del 删除键（幂等）。
func (c *Client) Del(keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	_, err := c.exec(append([]string{"DEL"}, keys...)...)
	return err
}

// IncrWindow 自增并仅在首次自增时设置 TTL（固定窗口计数器），返回自增后的值。
func (c *Client) IncrWindow(key string, ttlSeconds int64) (int64, error) {
	reply, err := c.exec("EVAL", IncrWindowScript, "1", key, strconv.FormatInt(ttlSeconds, 10))
	if err != nil {
		return 0, err
	}
	n, ok := reply.(int64)
	if !ok {
		return 0, fmt.Errorf("redisclient: EVAL 意外回复 %T", reply)
	}
	return n, nil
}

// Eval 执行 Lua 脚本，返回整型回复（多副本协作件的原子操作统一走它）。
func (c *Client) Eval(script string, keys []string, args ...string) (int64, error) {
	cmd := make([]string, 0, 3+len(keys)+len(args))
	cmd = append(cmd, "EVAL", script, strconv.Itoa(len(keys)))
	cmd = append(cmd, keys...)
	cmd = append(cmd, args...)
	reply, err := c.exec(cmd...)
	if err != nil {
		return 0, err
	}
	n, ok := reply.(int64)
	if !ok {
		return 0, fmt.Errorf("redisclient: EVAL 意外回复 %T", reply)
	}
	return n, nil
}

func writeCommand(bw *bufio.Writer, args ...string) error {
	if _, err := fmt.Fprintf(bw, "*%d\r\n", len(args)); err != nil {
		return err
	}
	for _, a := range args {
		// RESP2 批量串按长度成帧，值内含 \r\n 也安全（len 为字节数）。
		if _, err := fmt.Fprintf(bw, "$%d\r\n%s\r\n", len(a), a); err != nil {
			return err
		}
	}
	return nil
}

func readReply(br *bufio.Reader) (any, error) {
	line, err := readLine(br)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, errors.New("redisclient: 空回复")
	}
	switch line[0] {
	case '+':
		return string(line[1:]), nil
	case '-':
		return nil, errors.New(string(line[1:]))
	case ':':
		n, err := strconv.ParseInt(string(line[1:]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("redisclient: 非法整数回复 %q", line)
		}
		return n, nil
	case '$':
		n, err := strconv.Atoi(string(line[1:]))
		if err != nil {
			return nil, fmt.Errorf("redisclient: 非法批量回复 %q", line)
		}
		if n < 0 {
			return nil, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(string(line[1:]))
		if err != nil {
			return nil, fmt.Errorf("redisclient: 非法数组回复 %q", line)
		}
		if n < 0 {
			return nil, nil
		}
		arr := make([]any, n)
		for i := range arr {
			if arr[i], err = readReply(br); err != nil {
				return nil, err
			}
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("redisclient: 未知回复类型 %q", line[0])
	}
}

func readLine(br *bufio.Reader) ([]byte, error) {
	line, err := br.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, fmt.Errorf("redisclient: 非法行结尾 %q", line)
	}
	return line[:len(line)-2], nil
}
