// Package manage —— 面板本机管理通道（Unix socket）。
//
// 问题背景（实测复现）：CLI 改账号/密码是"另一个进程直接写 SQLite"，
// 而运行中的面板持有自己的内存配置并**周期性回写数据库**——CLI 写入的
// 修改在几十秒内被静默覆盖（呈现为"vm 改密码是假的/改完登录失败"）。
//
// 解法：面板启动时监听 /root/.eyvescloud/manage.sock（root-only 0600），
// CLI 写类操作（account rename/reset 等）优先走 socket 让**运行中的面板**
// 在自己的内存+数据库上原子执行；面板未运行时才回退直改数据库。
package manage

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/version"
)

// SocketPath 是管理通道套接字的固定位置。
// 数据目录可能被 systemd 单元用 EYVESCLOUD_DATA_DIR 定制到 CLI 不知情的位置，
// 因此放固定路径（/root/.eyvescloud/），文件权限 root-only 即可保证安全。
var SocketPath = "/root/.eyvescloud/manage.sock"

type Request struct {
	Action string   `json:"action"`
	Args   []string `json:"args,omitempty"`
}

type Response struct {
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// StartServer 在面板/agent 进程里启动管理监听（失败仅记日志，不阻断启动）。
func StartServer() {
	go func() {
		_ = os.MkdirAll(filepath.Dir(SocketPath), 0700)
		_ = os.Remove(SocketPath) // 清理残留
		ln, err := net.Listen("unix", SocketPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "管理通道启动失败（不影响面板运行）: %v\n", err)
			return
		}
		// root-only：非 root 进程无法连接（vm 本身也要求 root）。
		_ = os.Chmod(SocketPath, 0600)
		for {
			conn, err := ln.Accept()
			if err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "closed") || os.IsPermission(err) {
					return
				}
				time.Sleep(time.Second)
				continue
			}
			go handleConn(conn)
		}
	}()
}

func handleConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	var req Request
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		write(conn, Response{OK: false, Error: "请求解析失败"})
		return
	}
	resp := dispatch(req)
	write(conn, resp)
}

func dispatch(req Request) Response {
	cfg := config.AppConfig
	if cfg == nil {
		return Response{OK: false, Error: "面板未初始化"}
	}
	switch req.Action {
	case "account.show":
		cfg := config.AppConfig
		var b strings.Builder
		b.WriteString("管理员账号： " + cfg.AdminUser + "\n")
		if cfg.AdminTOTPEnabled {
			b.WriteString("两步验证：  已启用\n")
		} else if cfg.AdminTOTPSecret != "" {
			b.WriteString("两步验证：  已生成密钥但未启用\n")
		} else {
			b.WriteString("两步验证：  未启用\n")
		}
		adminPath := config.CurrentAdminPath()
		b.WriteString(fmt.Sprintf("管理员路径： %s\n", adminPath))
		b.WriteString(fmt.Sprintf("监听端口：  %d\n", cfg.Port))
		b.WriteString(fmt.Sprintf("数据目录：  %s\n", cfg.DataDir))
		b.WriteString(fmt.Sprintf("版本：      %s\n", version.Current()))
		return Response{OK: true, Output: b.String()}

	case "account.rename":
		newUser := strings.TrimSpace(strings.Join(req.Args, " "))
		if len(req.Args) > 0 {
			newUser = strings.TrimSpace(req.Args[0])
		}
		if len(newUser) < 3 {
			return Response{OK: false, Error: "账号长度至少 3 位"}
		}
		if strings.ContainsAny(newUser, " \t\"'\\/<>") {
			return Response{OK: false, Error: "账号含有非法字符"}
		}
		if newUser == cfg.AdminUser {
			return Response{OK: true, Output: "新账号与当前账号相同，无需更改。\n"}
		}
		old := cfg.AdminUser
		if err := config.MutateGlobalMetaOnly(func(c *config.EyvescloudConfig) {
			c.AdminUser = newUser
			c.AdminTokenVersion++
		}); err != nil {
			return Response{OK: false, Error: "更改失败：" + err.Error()}
		}
		auditManage(req.Action, old+" → "+newUser)
		return Response{OK: true, Output: fmt.Sprintf("管理员账号已更改：%s → %s\n所有已登录的管理员会话已失效，需重新登录。\n", old, newUser)}

	case "account.reset":
		// CLI 侧在本地生成强密码后明文传入（面板只落哈希，无法回显明文）。
		newPassword := ""
		if len(req.Args) > 0 {
			newPassword = strings.TrimSpace(req.Args[0])
		}
		if newPassword == "" {
			newPassword = randomStrongPasswordShared()
		}
		if err := config.ResetAdminPassword(newPassword); err != nil {
			return Response{OK: false, Error: "重设失败：" + err.Error()}
		}
		config.AppConfigMu.RLock()
		user := config.AppConfig.AdminUser
		totpOn := config.AppConfig.AdminTOTPEnabled
		config.AppConfigMu.RUnlock()
		auditManage(req.Action, user)
		out := fmt.Sprintf("管理员密码已重设（仅显示一次，请妥善保存）\n账号：  %s\n密码：  %s\n", user, newPassword)
		if totpOn {
			out += "提醒：该账号启用了两步验证，登录时还需动态口令。\n"
		}
		return Response{OK: true, Output: out}

	default:
		return Response{OK: false, Error: "未知管理动作：" + req.Action}
	}
}

func auditManage(action, detail string) {
	fmt.Fprintf(os.Stderr, "[manage] %s %s\n", action, detail)
}

// randomStrongPasswordShared 与 cli.randomStrongPassword 同算法（18 位，四类字符各至少一）。
// 放在 manage 包避免 cli ↔ manage 循环依赖。
func randomStrongPasswordShared() string {
	const upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	const lower = "abcdefghijklmnopqrstuvwxyz"
	const digits = "0123456789"
	const symbols = "!@#$%^&*-_=+"
	all := upper + lower + digits + symbols
	set := func(s string) byte { return s[randomIndex(len(s))] }
	var sb strings.Builder
	sb.WriteByte(set(upper))
	sb.WriteByte(set(lower))
	sb.WriteByte(set(digits))
	sb.WriteByte(set(symbols))
	for i := 0; i < 18-sb.Len(); i++ {
		sb.WriteByte(all[randomIndex(len(all))])
	}
	return sb.String()
}

func randomIndex(n int) int {
	if n <= 0 {
		return 0
	}
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return 0
	}
	val := int(buf[0])<<24 | int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
	return val % n
}

func write(conn net.Conn, resp Response) {
	_ = json.NewEncoder(conn).Encode(resp)
}

// Call 是 CLI 侧客户端：连接管理 socket 执行动作。
// 返回 (执行成功, 输出文本, 是否连上了面板)。
func Call(action string, args []string) (bool, string, bool) {
	conn, err := net.DialTimeout("unix", SocketPath, 2*time.Second)
	if err != nil {
		return false, "", false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	req, _ := json.Marshal(Request{Action: action, Args: args})
	_, _ = conn.Write(req)
	resp, err := io_ReadAll(conn)
	if err != nil {
		return false, "", true
	}
	if !resp.OK {
		return false, resp.Error, true
	}
	return true, resp.Output, true
}

func io_ReadAll(conn net.Conn) (Response, error) {
	dec := json.NewDecoder(conn)
	var resp Response
	if err := dec.Decode(&resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}
