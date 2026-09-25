package notify

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// SMTPSender 通过真实 SMTP 服务器发送邮件（notify 包的 email 渠道实现）。
//
// 安全设计：
//   - 正文仅 text/plain（RenderEmailBody 已禁止 HTML）；
//   - 标题/收件人/发件人剥离 CR/LF，防 SMTP 头注入；
//   - smtps 模式（465）隐式 TLS；starttls 模式（587）强制 STARTTLS 失败即中止；
//   - 连接与发送整体受超时控制，避免阻塞 Dispatcher 重试循环。
type SMTPSender struct {
	// Config 每次 Send 时读取快照，允许运行期热更新。
	Config func() SMTPConfig
	// DialTimeout 单次连接超时（默认 10s）。
	DialTimeout time.Duration
	// SendTimeout 整个发送流程超时（默认 30s）。
	SendTimeout time.Duration
}

// SMTPConfig 是 SMTPSender 需要的连接参数（由 api 层从 config 提供）。
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLSMode  string // "starttls" | "smtps"
}

// Kind implements Sender.
func (s *SMTPSender) Kind() ChannelKind { return ChannelEmail }

// sanitizeHeader 剥离邮件头字段中的 CR/LF，防 SMTP 头注入。
func sanitizeHeader(v string) string {
	v = strings.ReplaceAll(v, "\r", "")
	v = strings.ReplaceAll(v, "\n", "")
	return strings.TrimSpace(v)
}

func (s *SMTPSender) timeoutsWithDefaults() (dial, send time.Duration) {
	dial, send = s.DialTimeout, s.SendTimeout
	if dial <= 0 {
		dial = 10 * time.Second
	}
	if send <= 0 {
		send = 30 * time.Second
	}
	return
}

// Send implements Sender：同步发送一封 text/plain 邮件。
func (s *SMTPSender) Send(msg Message) error {
	cfg := s.Config()
	if strings.TrimSpace(cfg.Host) == "" || cfg.Port <= 0 {
		return fmt.Errorf("notify/smtp: host/port not configured")
	}
	to := sanitizeHeader(msg.Recipient)
	if to == "" || !strings.Contains(to, "@") {
		return fmt.Errorf("notify/smtp: invalid recipient %q", msg.Recipient)
	}
	from := sanitizeHeader(cfg.From)
	if from == "" {
		return fmt.Errorf("notify/smtp: from address not configured")
	}
	subject := sanitizeHeader(msg.Subject)
	dialTimeout, sendTimeout := s.timeoutsWithDefaults()

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	done := make(chan error, 1)
	go func() { done <- s.deliver(addr, cfg, from, to, subject, msg) }()
	select {
	case err := <-done:
		return err
	case <-time.After(sendTimeout + dialTimeout):
		return fmt.Errorf("notify/smtp: send timeout after %s", sendTimeout+dialTimeout)
	}
}

func (s *SMTPSender) deliver(addr string, cfg SMTPConfig, from, to, subject string, msg Message) error {
	dialTimeout, _ := s.timeoutsWithDefaults()

	if strings.EqualFold(cfg.TLSMode, "smtps") {
		return s.deliverSMTPS(addr, cfg, from, to, subject, msg)
	}
	// 默认 starttls（587）
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return fmt.Errorf("notify/smtp: dial: %w", err)
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("notify/smtp: client: %w", err)
	}
	defer c.Close()

	// 强制 STARTTLS：明文提交凭据/内容不可接受
	if ok, _ := c.Extension("STARTTLS"); !ok {
		return fmt.Errorf("notify/smtp: server %s does not support STARTTLS", addr)
	}
	tlsCfg := &tls.Config{ServerName: cfg.Host}
	if err := c.StartTLS(tlsCfg); err != nil {
		return fmt.Errorf("notify/smtp: starttls: %w", err)
	}
	return s.authAndSend(c, cfg, from, to, subject, msg)
}

func (s *SMTPSender) deliverSMTPS(addr string, cfg SMTPConfig, from, to, subject string, msg Message) error {
	dialTimeout, _ := s.timeoutsWithDefaults()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: dialTimeout}, "tcp", addr, &tls.Config{ServerName: cfg.Host})
	if err != nil {
		return fmt.Errorf("notify/smtp: tls dial: %w", err)
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("notify/smtp: client: %w", err)
	}
	defer c.Close()
	return s.authAndSend(c, cfg, from, to, subject, msg)
}

func (s *SMTPSender) authAndSend(c *smtp.Client, cfg SMTPConfig, from, to, subject string, msg Message) error {
	if cfg.Username != "" {
		if ok, mech := c.Extension("AUTH"); ok {
			var a smtp.Auth
			switch {
			case strings.Contains(mech, "PLAIN"):
				a = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
			case strings.Contains(mech, "CRAM-MD5"):
				a = smtp.CRAMMD5Auth(cfg.Username, cfg.Password)
			case strings.Contains(mech, "LOGIN"):
				a = &loginAuth{cfg.Username, cfg.Password}
			}
			if a != nil {
				if err := c.Auth(a); err != nil {
					return fmt.Errorf("notify/smtp: auth: %w", err)
				}
			}
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("notify/smtp: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("notify/smtp: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("notify/smtp: DATA: %w", err)
	}
	headers := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n", from, to, subject)
	if _, err := w.Write([]byte(headers + msg.Body)); err != nil {
		w.Close()
		return fmt.Errorf("notify/smtp: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("notify/smtp: close data: %w", err)
	}
	return c.Quit()
}

// loginAuth 实现 SMTP LOGIN 认证（net/smtp 未内置，部分国内邮件服务商只用它）。
type loginAuth struct{ username, password string }

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", []byte(a.username), nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:":
		return []byte(a.username), nil
	case "password:":
		return []byte(a.password), nil
	}
	return []byte(a.password), nil
}
