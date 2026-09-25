package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"eyvescloud/internal/config"
)

// 异地备份：把实例备份归档通过 SSH/SCP 额外复制到运营方自备的一台备份服务器，
// 用于应对本机磁盘损坏、整机丢失等灾难场景。
//
// 安全设计：
//   - 仅支持免密（公钥）认证；私钥必须位于面板数据目录内，默认由面板用 ssh-keygen 生成，
//     管理员只需把接口返回的 public_key 装到远端 ~/.ssh/authorized_keys 即可；
//   - 远端目录 / 文件名在拼命令前做严格白名单校验（仅 [A-Za-z0-9._-] 与 /），杜绝命令注入；
//   - 采用 known_hosts TOFU（首次信任并持久化，之后指纹变化即拒绝），避免中间人。
const remoteBackupDefaultPort = 22

var remoteBackupSafeSegment = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// remoteBackupKeyPath 返回实际使用的私钥路径（KeyPath 为空则用数据目录内的默认路径）。
func remoteBackupKeyPath(s config.RemoteBackupSettings) string {
	if p := strings.TrimSpace(s.KeyPath); p != "" {
		return p
	}
	return filepath.Join(config.AppConfig.DataDir, "ssh", "backup_ed25519")
}

func remoteBackupKnownHostsPath() string {
	return filepath.Join(config.AppConfig.DataDir, "ssh", "backup_known_hosts")
}

// ensureRemoteBackupKey 确保私钥存在（不存在则生成无口令 ed25519 密钥）。
func ensureRemoteBackupKey(keyPath string) error {
	if _, err := os.Stat(keyPath); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return err
	}
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", "eyvescloud-remote-backup", "-f", keyPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ssh-keygen failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return os.Chmod(keyPath, 0600)
}

// remoteBackupPublicKey 返回公钥字符串，供管理员安装到异地服务器。
func remoteBackupPublicKey(keyPath string) (string, error) {
	if err := ensureRemoteBackupKey(keyPath); err != nil {
		return "", err
	}
	data, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// remoteBackupHostKeyCallback 构造带 TOFU 的 host key 校验回调：
// 首次连接记录并信任，之后指纹变化直接拒绝（防中间人）。
func remoteBackupHostKeyCallback(khPath string) (ssh.HostKeyCallback, error) {
	if err := os.MkdirAll(filepath.Dir(khPath), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(khPath, os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	base, err := knownhosts.New(khPath)
	if err != nil {
		return nil, err
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := base(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
			line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
			af, aerr := os.OpenFile(khPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if aerr != nil {
				return aerr
			}
			defer af.Close()
			if _, werr := af.WriteString(line + "\n"); werr != nil {
				return werr
			}
			return nil
		}
		return err
	}, nil
}

// validateRemoteDir 校验远端目录：必须是绝对路径，且每段仅允许 [A-Za-z0-9._-]。
func validateRemoteDir(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("remote_dir is required")
	}
	if !strings.HasPrefix(dir, "/") {
		return errors.New("remote_dir must be an absolute path")
	}
	trimmed := strings.Trim(dir, "/")
	if trimmed == "" {
		return errors.New("remote_dir cannot be the filesystem root")
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == "." || seg == ".." {
			return errors.New("remote_dir must not contain '.' or '..' segments")
		}
		if !remoteBackupSafeSegment.MatchString(seg) {
			return fmt.Errorf("remote_dir contains unsupported characters: %q", seg)
		}
	}
	return nil
}

// remoteBackupDirFor 返回某个备份在远端的目录（按容器 ID 分目录，避免同名覆盖）。
func remoteBackupDirFor(s config.RemoteBackupSettings, b config.InstanceBackup) string {
	dir := strings.TrimRight(strings.TrimSpace(s.RemoteDir), "/")
	return dir + "/" + strconv.Itoa(b.ContainerID)
}

// remoteBackupFilePath 返回某个备份在远端的完整文件路径，并做安全校验。
func remoteBackupFilePath(s config.RemoteBackupSettings, b config.InstanceBackup) (string, error) {
	if err := validateRemoteDir(s.RemoteDir); err != nil {
		return "", err
	}
	name := filepath.Base(strings.TrimSpace(b.Path))
	if name == "" || name == "." || name == "/" || !remoteBackupSafeSegment.MatchString(name) {
		return "", fmt.Errorf("unsafe backup file name: %q", name)
	}
	return remoteBackupDirFor(s, b) + "/" + name, nil
}

// shellQuote 用单引号包裹参数（路径已白名单校验，此处为纵深防御）。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// dialRemoteBackup 建立到异地备份服务器的 SSH 连接。
func dialRemoteBackup(s config.RemoteBackupSettings) (*ssh.Client, error) {
	host := strings.TrimSpace(s.Host)
	if host == "" {
		return nil, errors.New("remote backup host is required")
	}
	if strings.ContainsAny(host, " \t\r\n'\"`;|&$") {
		return nil, fmt.Errorf("invalid remote backup host: %q", host)
	}
	user := strings.TrimSpace(s.User)
	if user == "" {
		user = "root"
	}
	port := s.Port
	if port <= 0 || port > 65535 {
		port = remoteBackupDefaultPort
	}
	keyPath := remoteBackupKeyPath(s)
	if err := safePathUnder(keyPath, config.AppConfig.DataDir); err != nil {
		return nil, fmt.Errorf("remote backup private key must live inside the panel data directory: %w", err)
	}
	if err := ensureRemoteBackupKey(keyPath); err != nil {
		return nil, err
	}
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read remote backup key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse remote backup key: %w", err)
	}
	hostKeyCB, err := remoteBackupHostKeyCallback(remoteBackupKnownHostsPath())
	if err != nil {
		return nil, fmt.Errorf("init known_hosts: %w", err)
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCB,
		Timeout:         15 * time.Second,
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	return dialSSHWithTimeout(addr, cfg, 15*time.Second)
}

// runRemoteCommand 在远端执行一条命令，失败时回传 stderr 便于定位。
func runRemoteCommand(client *ssh.Client, command string) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	out, err := session.CombinedOutput(command)
	if err != nil {
		return fmt.Errorf("%s: %v: %s", command, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// awaitSCPAck 读取 SCP 协议的一个确认字节；非 0 即为错误行。
func awaitSCPAck(r *bufio.Reader) error {
	b, err := r.ReadByte()
	if err != nil {
		return err
	}
	if b == 0 {
		return nil
	}
	line, _ := r.ReadString('\n')
	return fmt.Errorf("scp protocol error: %s", strings.TrimSpace(string(b)+line))
}

// scpUploadFile 用 SCP 协议（scp -t 接收端）把本地文件上传到远端指定路径。
func scpUploadFile(client *ssh.Client, localPath, remoteFilePath string) error {
	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	if err := session.Start("scp -t " + shellQuote(path.Dir(remoteFilePath))); err != nil {
		return err
	}
	reader := bufio.NewReader(stdout)
	if err := awaitSCPAck(reader); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdin, "C%04o %d %s\n", info.Mode().Perm(), info.Size(), path.Base(remoteFilePath)); err != nil {
		return err
	}
	if err := awaitSCPAck(reader); err != nil {
		return err
	}
	if _, err := io.Copy(stdin, src); err != nil {
		return err
	}
	if _, err := stdin.Write([]byte{0}); err != nil {
		return err
	}
	if err := awaitSCPAck(reader); err != nil {
		return err
	}
	return session.Wait()
}

// scpDownloadFile 用 SCP 协议（scp -f 发送端）把远端文件下载到本地路径。
func scpDownloadFile(client *ssh.Client, remoteFilePath, localPath string) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	if err := session.Start("scp -f " + shellQuote(remoteFilePath)); err != nil {
		return err
	}
	reader := bufio.NewReader(stdout)
	if _, err := stdin.Write([]byte{0}); err != nil {
		return err
	}
	header, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	var mode string
	var size int64
	var name string
	if _, err := fmt.Sscanf(header, "%s %d %s", &mode, &size, &name); err != nil {
		return fmt.Errorf("invalid scp header %q: %w", strings.TrimSpace(header), err)
	}
	if !strings.HasPrefix(mode, "C") {
		return fmt.Errorf("unexpected scp header: %q", strings.TrimSpace(header))
	}
	if _, err := stdin.Write([]byte{0}); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0700); err != nil {
		return err
	}
	dst, err := os.OpenFile(localPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := io.CopyN(dst, reader, size); err != nil {
		_ = dst.Close()
		_ = os.Remove(localPath)
		return err
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(localPath)
		return err
	}
	// 读取文件结束标记字节并确认。
	if _, err := reader.ReadByte(); err != nil {
		return err
	}
	if _, err := stdin.Write([]byte{0}); err != nil {
		return err
	}
	return session.Wait()
}

// syncBackupToRemote 把单个备份上传到异地目标。
func syncBackupToRemote(backupID string) error {
	settings := config.GetRemoteBackupSettings()
	if !settings.Enabled {
		return nil
	}
	backup := config.FindInstanceBackup(backupID)
	if backup == nil {
		return fmt.Errorf("backup not found: %s", backupID)
	}
	if backup.Path == "" || safeInstanceBackupStorePath(backup.Path) != nil {
		return errors.New("backup archive path is not safe")
	}
	if _, err := os.Stat(backup.Path); err != nil {
		err = fmt.Errorf("backup archive not found: %w", err)
		config.SetInstanceBackupRemoteStatus(backupID, false, err.Error())
		return err
	}
	remoteFile, err := remoteBackupFilePath(settings, *backup)
	if err != nil {
		config.SetInstanceBackupRemoteStatus(backupID, false, err.Error())
		config.RecordRemoteBackupResult(false, err.Error())
		return err
	}

	client, err := dialRemoteBackup(settings)
	if err != nil {
		config.SetInstanceBackupRemoteStatus(backupID, false, err.Error())
		config.RecordRemoteBackupResult(false, err.Error())
		return err
	}
	defer client.Close()

	if err := runRemoteCommand(client, "mkdir -p "+shellQuote(remoteBackupDirFor(settings, *backup))); err != nil {
		config.SetInstanceBackupRemoteStatus(backupID, false, err.Error())
		config.RecordRemoteBackupResult(false, err.Error())
		return err
	}
	if err := scpUploadFile(client, backup.Path, remoteFile); err != nil {
		config.SetInstanceBackupRemoteStatus(backupID, false, err.Error())
		config.RecordRemoteBackupResult(false, err.Error())
		return err
	}
	config.SetInstanceBackupRemoteStatus(backupID, true, "")
	config.RecordRemoteBackupResult(true, "")
	return nil
}

// fetchBackupFromRemote 从异地目标把归档拉回本地路径（用于本地副本丢失后的还原）。
func fetchBackupFromRemote(backupID string) error {
	settings := config.GetRemoteBackupSettings()
	if !settings.Enabled {
		return errors.New("off-site backup is not enabled")
	}
	backup := config.FindInstanceBackup(backupID)
	if backup == nil {
		return fmt.Errorf("backup not found: %s", backupID)
	}
	if backup.Path == "" || safeInstanceBackupStorePath(backup.Path) != nil {
		return errors.New("backup archive path is not safe")
	}
	remoteFile, err := remoteBackupFilePath(settings, *backup)
	if err != nil {
		return err
	}
	client, err := dialRemoteBackup(settings)
	if err != nil {
		return err
	}
	defer client.Close()
	return scpDownloadFile(client, remoteFile, backup.Path)
}

// removeRemoteBackupBestEffort 尽力删除异地副本；失败只告警，不影响本地流程。
func removeRemoteBackupBestEffort(backup config.InstanceBackup) {
	settings := config.GetRemoteBackupSettings()
	if !settings.Enabled || !backup.RemoteUploaded {
		return
	}
	remoteFile, err := remoteBackupFilePath(settings, backup)
	if err != nil {
		fmt.Printf("Warning: remote backup delete skipped for %s: %v\n", backup.ID, err)
		return
	}
	client, err := dialRemoteBackup(settings)
	if err != nil {
		fmt.Printf("Warning: remote backup delete skipped for %s: %v\n", backup.ID, err)
		return
	}
	defer client.Close()
	if err := runRemoteCommand(client, "rm -f "+shellQuote(remoteFile)); err != nil {
		fmt.Printf("Warning: remote backup delete failed for %s: %v\n", backup.ID, err)
	}
}

// TestRemoteBackupConnection 验证连通性：连接 → 建目录 → 写探测文件 → 删除。
func TestRemoteBackupConnection(settings config.RemoteBackupSettings) error {
	if strings.TrimSpace(settings.Host) == "" {
		return errors.New("host is required")
	}
	if strings.TrimSpace(settings.User) == "" {
		return errors.New("user is required")
	}
	if err := validateRemoteDir(settings.RemoteDir); err != nil {
		return err
	}
	client, err := dialRemoteBackup(settings)
	if err != nil {
		return err
	}
	defer client.Close()
	probeDir := strings.TrimRight(strings.TrimSpace(settings.RemoteDir), "/")
	if err := runRemoteCommand(client, "mkdir -p "+shellQuote(probeDir)); err != nil {
		return err
	}
	probe := probeDir + "/.eyvescloud-probe-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := runRemoteCommand(client, "touch "+shellQuote(probe)); err != nil {
		return err
	}
	if err := runRemoteCommand(client, "rm -f "+shellQuote(probe)); err != nil {
		return err
	}
	return nil
}

// ---- HTTP 路由 ----

type remoteBackupSettingsView struct {
	config.RemoteBackupSettings
	EffectiveKeyPath string `json:"effective_key_path"`
	PublicKey        string `json:"public_key,omitempty"`
}

func remoteBackupSettingsViewData() remoteBackupSettingsView {
	s := config.GetRemoteBackupSettings()
	kp := remoteBackupKeyPath(s)
	pub, _ := remoteBackupPublicKey(kp)
	return remoteBackupSettingsView{
		RemoteBackupSettings: s,
		EffectiveKeyPath:     kp,
		PublicKey:            pub,
	}
}

// HandleRemoteBackupSettings 读取/更新异地备份目标设置。
func HandleRemoteBackupSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "backup:read") {
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: remoteBackupSettingsViewData()})
	case http.MethodPut:
		if !requireScope(w, r, "backup:write") {
			return
		}
		var req config.RemoteBackupSettings
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		req.Host = strings.TrimSpace(req.Host)
		req.User = strings.TrimSpace(req.User)
		req.RemoteDir = strings.TrimSpace(req.RemoteDir)
		req.KeyPath = strings.TrimSpace(req.KeyPath)

		if req.Enabled {
			if req.Host == "" || req.User == "" {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "host and user are required"})
				return
			}
			if err := validateRemoteDir(req.RemoteDir); err != nil {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
				return
			}
			if req.KeyPath != "" {
				if err := safePathUnder(req.KeyPath, config.AppConfig.DataDir); err != nil {
					jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "key_path must be inside the panel data directory"})
					return
				}
			}
			if _, err := remoteBackupPublicKey(remoteBackupKeyPath(req)); err != nil {
				jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
				return
			}
		}

		// 保留上一次运行结果，避免更新设置把状态清零。
		existing := config.GetRemoteBackupSettings()
		req.LastResult = existing.LastResult
		req.LastError = existing.LastError
		req.LastRunAt = existing.LastRunAt
		config.UpdateRemoteBackupSettings(req)
		if err := config.SaveConfig(); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		auditRequest(r, "remote_backup.settings", "settings",
			fmt.Sprintf("enabled=%v host=%s user=%s remote_dir=%s", req.Enabled, req.Host, req.User, req.RemoteDir), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: remoteBackupSettingsViewData()})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleRemoteBackupTest 测试异地备份目标连通性与可写性。
func HandleRemoteBackupTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "backup:write") {
		return
	}
	var req config.RemoteBackupSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	if err := TestRemoteBackupConnection(req); err != nil {
		auditRequest(r, "remote_backup.test", "settings", err.Error(), false, err.Error())
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: err.Error()})
		return
	}
	auditRequest(r, "remote_backup.test", "settings", "connection ok", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Connection OK"})
}
