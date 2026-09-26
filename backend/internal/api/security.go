package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"eyvescloud/internal/config"
)

// SecurityAlert represents a detected abuse event.
type SecurityAlert struct {
	ID            string `json:"id"`
	ContainerName string `json:"container_name"`
	// Kind 为容器类型（lxc / kvm），用于前端展示"xxx容器（LXC）"。
	Kind string `json:"kind,omitempty"`
	// Tenant / Owner 记录滥用行为的归属：Tenant 为容器所属租户，Owner 为绑定了该容器的
	// 子用户（若存在）。便于管理端按用户/租户归因与统计，而不只是按容器。
	Tenant     string `json:"tenant,omitempty"`
	Owner      string `json:"owner,omitempty"`
	Type       string `json:"type"`     // 见 abuseBehaviorLabels
	Severity   string `json:"severity"` // low, medium, high, critical
	SourceIP   string `json:"source_ip"`
	TargetIP   string `json:"target_ip"`
	TargetPort int    `json:"target_port"`
	Detail     string `json:"detail"`
	LogLine    string `json:"log_line"`
	Timestamp  string `json:"timestamp"`
	Count      int    `json:"count"`
}

// abuseBehaviorLabels 把告警类型映射为面向管理员的"行为"名称，
// 用于统一生成「xxx容器（LXC）可能存在 xxx 行为：证据」这类描述。
var abuseBehaviorLabels = map[string]string{
	"port_scan":           "端口扫描",
	"horizontal_scan":     "横向端口扫描",
	"brute_force":         "暴力破解",
	"inbound_brute_force": "被暴力破解",
	"inbound_ddos":        "被 DDoS 攻击",
	"inbound_scan":        "被端口扫描",
	"ddos":                "大规模对外攻击/扫描",
	"cc":                  "CC/HTTP 洪水",
	"p2p":                 "BT/PT 下载",
	"spam":                "垃圾邮件发送",
	"malware":             "恶意软件/C2 外联",
	"mining":              "挖矿",
	"proxy":               "代理/VPN/Tor",
	"reflection":          "UDP 反射放大",
	"arp_spoof":           "ARP 欺骗/地址冲突",
	"lateral_movement":    "内网横向移动",
	"backdoor":            "后门/远控监听",
}

// compromiseAlertType 是"疑似被入侵"的复合告警类型。
const compromiseAlertType = "compromise"

// kindLabel 返回容器类型的中文展示标签。
func kindLabel(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case config.VirtualizationKVM:
		return "KVM"
	case config.VirtualizationLXC:
		return "LXC"
	default:
		return "容器"
	}
}

// abuseDetail 生成统一格式的行为告警描述："{容器}（LXC）可能存在{行为}行为：{证据}"。
// 对 compromise 类型生成"{容器}（LXC）疑似被入侵：{证据}"。
func abuseDetail(container, kind, alertType, evidence string) string {
	label := kindLabel(kind)
	behavior := abuseBehaviorLabels[alertType]
	if alertType == compromiseAlertType {
		return fmt.Sprintf("%s（%s）疑似被入侵：%s", container, label, evidence)
	}
	if behavior == "" {
		behavior = alertType
	}
	return fmt.Sprintf("%s（%s）可能存在%s行为：%s", container, label, behavior, evidence)
}

// SecurityScanner monitors container network activity for abuse patterns.
type SecurityScanner struct {
	mu        sync.Mutex
	alerts    []SecurityAlert
	nextID    int
	scanCount map[string]int
	stopChan  chan struct{}
}

type connEntry struct {
	dstIP   string
	dstPort int
	proto   string
	state   string
	line    string
}

// inboundStats 汇总容器作为目的端的入站连接（被扫描 / 被爆破 / 被攻击 / 后门监听）。
type inboundStats struct {
	total        int
	portTotals   map[int]int            // 容器本地端口 -> 入站连接数
	portPeers    map[int]map[string]int // 容器本地端口 -> 对端 IP -> 次数
	portSynRecv  map[int]int            // 容器本地端口 -> 半开(SYN_RECV)连接数
	portSynPeers map[int]map[string]int // 容器本地端口 -> 半开连接对端 IP
	peerPorts    map[string]map[int]int // 对端 IP -> 容器本地端口 -> 次数
}

func newInboundStats() *inboundStats {
	return &inboundStats{
		portTotals:   make(map[int]int),
		portPeers:    make(map[int]map[string]int),
		portSynRecv:  make(map[int]int),
		portSynPeers: make(map[int]map[string]int),
		peerPorts:    make(map[string]map[int]int),
	}
}

func (is *inboundStats) add(peerIP string, localPort int, proto, state string) {
	is.total++
	if localPort <= 0 {
		return
	}
	is.portTotals[localPort]++
	if is.portPeers[localPort] == nil {
		is.portPeers[localPort] = make(map[string]int)
	}
	is.portPeers[localPort][peerIP]++
	if is.peerPorts[peerIP] == nil {
		is.peerPorts[peerIP] = make(map[int]int)
	}
	is.peerPorts[peerIP][localPort]++
	if strings.EqualFold(state, "SYN_RECV") {
		is.portSynRecv[localPort]++
		if is.portSynPeers[localPort] == nil {
			is.portSynPeers[localPort] = make(map[string]int)
		}
		is.portSynPeers[localPort][peerIP]++
	}
}

type trafficStats struct {
	total                 int
	totalSynSent          int
	destCounts            map[string]int
	destPorts             map[string]map[int]int
	portDestCounts        map[int]map[string]int
	portTotalCounts       map[int]int
	udpDestCounts         map[int]map[string]int
	udpTotalCounts        map[int]int
	udpDestTotalCounts    map[string]int
	synSentByDst          map[string]int
	tcpSynDestPorts       map[string]map[int]int
	tcpSynPortDestCounts  map[int]map[string]int
	tcpSynPortTotalCounts map[int]int
}

var scanner *SecurityScanner
var scannerStarted bool

var bruteForcePorts = map[int]string{
	21:    "FTP",
	22:    "SSH",
	23:    "Telnet",
	135:   "MS-RPC",
	139:   "NetBIOS",
	445:   "SMB",
	3306:  "MySQL",
	3389:  "RDP",
	5432:  "PostgreSQL",
	5900:  "VNC",
	5901:  "VNC",
	5985:  "WinRM",
	5986:  "WinRM",
	6379:  "Redis",
	9200:  "Elasticsearch",
	27017: "MongoDB",
}

var smtpPorts = map[int]string{
	25:   "SMTP",
	465:  "SMTPS",
	587:  "SMTP submission",
	2525: "SMTP alternate",
}

var reflectionPorts = map[int]string{
	17:    "QOTD",
	19:    "Chargen",
	53:    "DNS",
	69:    "TFTP",
	111:   "Portmap",
	123:   "NTP",
	137:   "NetBIOS",
	161:   "SNMP",
	389:   "CLDAP",
	500:   "IKE",
	1900:  "SSDP",
	3702:  "WS-Discovery",
	4500:  "IPsec NAT-T",
	5353:  "mDNS",
	11211: "Memcached",
}

var miningPorts = map[int]string{
	3333:  "Stratum",
	3334:  "Stratum",
	3335:  "Stratum",
	4444:  "Stratum",
	5555:  "Stratum",
	7777:  "Stratum",
	8888:  "Stratum",
	9999:  "Stratum",
	14433: "Stratum",
	14444: "Stratum",
}

var proxyPorts = map[int]string{
	1080:  "SOCKS",
	3128:  "HTTP proxy",
	8118:  "Privoxy",
	9001:  "Tor OR",
	9030:  "Tor directory",
	9050:  "Tor SOCKS",
	1194:  "OpenVPN",
	51820: "WireGuard",
}

var malwarePorts = map[int]string{
	1337:  "common backdoor",
	1243:  "Sub7 trojan",
	12345: "NetBus/RAT",
	12346: "NetBus trojan",
	20034: "NetBus trojan",
	27374: "Sub7 trojan",
	31337: "Back Orifice",
	31338: "Back Orifice",
	4444:  "Metasploit/reverse shell",
	54321: "reverse shell/RAT",
	5555:  "Android debug/reverse shell",
	6666:  "IRC botnet",
	6667:  "IRC botnet",
	6697:  "IRC over TLS",
	9050:  "Tor/C2 proxy",
}

// backdoorPorts 是几乎不可能承载正常公网服务的后门/远控（RAT）监听端口。
// 容器在这些端口上收到入站连接，强烈提示存在后门或已被植入远控。
var backdoorPorts = map[int]string{
	1337:  "后门",
	1243:  "Sub7 木马",
	12345: "NetBus/RAT",
	12346: "NetBus 木马",
	20034: "NetBus 木马",
	27374: "Sub7 木马",
	31337: "Back Orifice",
	31338: "Back Orifice",
	4444:  "反弹 Shell/Metasploit",
	54321: "反弹 Shell/RAT",
	5555:  "远控/ADB 后门",
	6666:  "IRC 僵尸网络",
	6667:  "IRC 僵尸网络",
}

// lateralSensitivePorts 是内网横向移动常被利用的高价值服务端口。
// 容器主动连接多个内网主机上的这些端口，通常意味着已被入侵并在横向渗透。
var lateralSensitivePorts = map[int]string{
	22:    "SSH",
	23:    "Telnet",
	135:   "MS-RPC",
	139:   "NetBIOS",
	445:   "SMB",
	1433:  "MSSQL",
	1521:  "Oracle",
	2375:  "Docker API",
	2376:  "Docker API(TLS)",
	3306:  "MySQL",
	3389:  "RDP",
	5432:  "PostgreSQL",
	5900:  "VNC",
	5985:  "WinRM",
	5986:  "WinRM(TLS)",
	6379:  "Redis",
	6443:  "Kubernetes API",
	9200:  "Elasticsearch",
	10250: "Kubelet",
	11211: "Memcached",
	27017: "MongoDB",
}

// privateCIDRs 是需要关注"内网横向"的私有/保留网段。
var privateCIDRs = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"100.64.0.0/10",
	"169.254.0.0/16",
}

var privateNets []*net.IPNet

func init() {
	for _, cidr := range privateCIDRs {
		if _, block, err := net.ParseCIDR(cidr); err == nil {
			privateNets = append(privateNets, block)
		}
	}
}

func isPrivateIP(ip string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	for _, block := range privateNets {
		if block.Contains(parsed) {
			return true
		}
	}
	return false
}

// webPorts 是 CC / HTTP 洪水检测关注的常见 Web 服务端口。
var webPorts = map[int]string{
	80:   "HTTP",
	443:  "HTTPS",
	8000: "HTTP alt",
	8080: "HTTP alt",
	8443: "HTTPS alt",
	8888: "HTTP alt",
	9000: "HTTP alt",
	9443: "HTTPS alt",
}

// btPorts 是 BitTorrent / PT 客户端、DHT 与 Tracker 的常用端口。
// 命中这些端口的持续连接通常意味着容器在跑 BT/PT（含私有 Tracker）下载或做种。
var btPorts = map[int]string{
	6881:  "BitTorrent",
	6882:  "BitTorrent",
	6883:  "BitTorrent",
	6884:  "BitTorrent",
	6885:  "BitTorrent",
	6886:  "BitTorrent",
	6887:  "BitTorrent",
	6888:  "BitTorrent",
	6889:  "BitTorrent",
	6890:  "BitTorrent",
	6969:  "BT tracker",
	2710:  "BitTorrent",
	51413: "Transmission",
	16881: "BitComet",
}

func InitScanner() {
	if scannerStarted {
		return
	}
	scannerStarted = true
	scanner = newSecurityScanner()
	go scanner.monitorLoop()
}

func newSecurityScanner() *SecurityScanner {
	return &SecurityScanner{
		alerts:    make([]SecurityAlert, 0),
		scanCount: make(map[string]int),
		stopChan:  make(chan struct{}),
	}
}

func ensureScanner() *SecurityScanner {
	if scanner == nil {
		scanner = newSecurityScanner()
	}
	return scanner
}

func (ss *SecurityScanner) monitorLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ss.stopChan:
			return
		case <-ticker.C:
			ss.checkAllContainers()
			// 同步维护 IP 防伪规则（仅在开启时生效；关闭时清理自有链）。
			reconcileIPAntiSpoof()
		}
	}
}

func (ss *SecurityScanner) alertCount() int {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return len(ss.alerts)
}

func (ss *SecurityScanner) checkAllContainers() {
	config.AppConfigMu.RLock()
	arpEnabled := config.AppConfig.ARPProtectionEnabled
	abuseEnabled := config.AppConfig.AbuseDetectionEnabled
	config.AppConfigMu.RUnlock()

	// GetContainers 自带读锁，必须在释放上面的读锁之后再调用，
	// 否则与写者并发时构成递归读锁（Go RWMutex 会死锁）。
	containers := config.GetContainers()
	for _, c := range containers {
		if c.Status != "running" {
			continue
		}
		if arpEnabled {
			ss.checkARPConflicts(c)
		}
		// 滥用行为检测总开关：关闭后不再做任何出站/入站连接分析。
		if !abuseEnabled {
			continue
		}
		// macvlan（LAN IPv4 模式）容器的出站流量不经过宿主机协议栈，
		// 需要进入容器网络命名空间读取 conntrack；NAT/桥接容器仍用宿主机 conntrack。
		netnsPID := containerConntrackNetnsPID(c)
		// 逐一对容器的所有地址检查连接：容器可能只持有公网 IP（macvlan），
		// 只监控 c.IP 会漏掉这类容器的滥用行为。
		for _, address := range containerMonitoredAddresses(c) {
			ss.checkContainer(c.Name, address, netnsPID)
		}
		// 基于指标采样的 CPU 特征检测（可发现非标准端口的矿机）。
		ss.detectSustainedCPUMining(c)
	}
}

// containerConntrackNetnsPID 返回应当用于读取 conntrack 的容器网络命名空间 init PID。
//   - KVM 客户机经由宿主机网桥/tap 出站，宿主机 conntrack 可见，无需 netns；
//   - LXC 的 NAT 模式（lxcbr0）同样经过宿主机协议栈；
//   - LXC 的 LAN IPv4 模式使用 macvlan，出站绕过宿主机，必须进入容器 netns 才能看到连接。
func containerConntrackNetnsPID(c config.Container) string {
	if c.IsKVM() || !c.UsesLANIPv4() {
		return ""
	}
	out, err := exec.Command("lxc-info", "-n", c.LxcName(), "-pH").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// containerMonitoredAddresses 汇总需要监控出站连接的地址：LAN IP、公网 IPv4、IPv6。
func containerMonitoredAddresses(c config.Container) []string {
	seen := make(map[string]struct{}, 4)
	addresses := make([]string, 0, 4)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		addresses = append(addresses, value)
	}

	add(c.IP)
	for _, ip := range c.PublicIPv4s {
		add(ip.Address)
	}
	add(c.IPv6)
	for _, address := range c.IPv6AddressStrings() {
		add(address)
	}
	return addresses
}

// checkARPConflicts detects IP-MAC mismatches for public/独立 IP containers
// (ARP 欺骗 / 地址冲突检测). Requires the container's recorded MAC address.
func (ss *SecurityScanner) checkARPConflicts(c config.Container) {
	check := func(address, iface string) {
		if strings.TrimSpace(address) == "" || strings.TrimSpace(c.MACAddress) == "" {
			return
		}
		lines := readNeighLines(address, iface)
		for _, line := range lines {
			lladdr := extractField(line, "lladdr=")
			if lladdr == "" {
				continue
			}
			if !strings.EqualFold(lladdr, c.MACAddress) {
				ss.addAlert(c.Name, "arp_spoof", "high", address, c.IP, 0,
					fmt.Sprintf("IP %s 邻居表 MAC %s 与容器绑定 MAC %s 不一致", address, lladdr, c.MACAddress),
					strings.TrimSpace(line))
			}
		}
	}

	for _, ip := range c.PublicIPv4s {
		check(ip.Address, ip.Interface)
	}
	if c.UsesLANIPv4() {
		check(c.IP, c.LANInterface)
	}
}

// readNeighLines returns `ip neigh show` output lines for an address.
func readNeighLines(address, iface string) []string {
	args := []string{"neigh", "show", address}
	if iface != "" {
		args = append(args, "dev", iface)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ip", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return splitNonEmptyLines(string(out))
}

func (ss *SecurityScanner) checkContainer(name, ip, netnsPID string) {
	lines := readConntrackForContainer(ip, netnsPID)
	if len(lines) == 0 {
		return
	}

	out := newTrafficStats()
	in := newInboundStats()
	for _, line := range lines {
		tuples := parseConntrackTuples(line)
		dir, peer, ok := classifyConntrackTuples(tuples, ip)
		if !ok || peer.ip == "" || peer.ip == ip {
			continue
		}
		proto := extractProtocol(line)
		state := extractConnState(line)
		if dir == connOutbound {
			out.add(connEntry{dstIP: peer.ip, dstPort: peer.port, proto: proto, state: state, line: line})
			continue
		}
		in.add(peer.ip, peer.localPort, proto, state)
	}

	if out.total == 0 && in.total == 0 {
		return
	}

	alertBefore := ss.alertCount()

	// 出站方向：容器作为发起方（滥用/外联）。
	ss.detectPortScans(name, ip, out)
	ss.detectBruteForce(name, ip, out)
	ss.detectSpam(name, ip, out)
	ss.detectMassAbuse(name, ip, out)
	ss.detectCC(name, ip, out)
	ss.detectP2P(name, ip, out)
	ss.detectReflectionAbuse(name, ip, out)
	ss.detectMining(name, ip, out)
	ss.detectProxyAndTor(name, ip, out)
	ss.detectMalware(name, ip, out)
	ss.detectLateralMovement(name, ip, out)

	// 入站方向：容器作为目的方（被攻击/被入侵迹象）。
	ss.detectInboundBruteForce(name, ip, in)
	ss.detectInboundDDoS(name, ip, in)
	ss.detectInboundScan(name, ip, in)
	ss.detectBackdoorListener(name, ip, in)

	// 复合判定：多个入侵指标同时出现时，给出"疑似被入侵"总结性告警。
	ss.detectCompromise(name, ip, out, in)

	// If new alerts were generated, snapshot the conntrack data for later retrieval.
	if ss.alertCount() > alertBefore {
		config.SaveConntrackSnapshot(ip, lines)
	}
}

func newTrafficStats() *trafficStats {
	return &trafficStats{
		destCounts:            make(map[string]int),
		destPorts:             make(map[string]map[int]int),
		portDestCounts:        make(map[int]map[string]int),
		portTotalCounts:       make(map[int]int),
		udpDestCounts:         make(map[int]map[string]int),
		udpTotalCounts:        make(map[int]int),
		synSentByDst:          make(map[string]int),
		udpDestTotalCounts:    make(map[string]int),
		tcpSynDestPorts:       make(map[string]map[int]int),
		tcpSynPortDestCounts:  make(map[int]map[string]int),
		tcpSynPortTotalCounts: make(map[int]int),
	}
}

func (ts *trafficStats) add(conn connEntry) {
	ts.total++
	ts.destCounts[conn.dstIP]++

	if conn.dstPort > 0 {
		if ts.destPorts[conn.dstIP] == nil {
			ts.destPorts[conn.dstIP] = make(map[int]int)
		}
		ts.destPorts[conn.dstIP][conn.dstPort]++

		if ts.portDestCounts[conn.dstPort] == nil {
			ts.portDestCounts[conn.dstPort] = make(map[string]int)
		}
		ts.portDestCounts[conn.dstPort][conn.dstIP]++
		ts.portTotalCounts[conn.dstPort]++

		if conn.proto == "udp" {
			if ts.udpDestCounts[conn.dstPort] == nil {
				ts.udpDestCounts[conn.dstPort] = make(map[string]int)
			}
			ts.udpDestCounts[conn.dstPort][conn.dstIP]++
			ts.udpTotalCounts[conn.dstPort]++
			ts.udpDestTotalCounts[conn.dstIP]++
		}
	}

	if conn.proto == "tcp" && conn.state == "SYN_SENT" {
		ts.totalSynSent++
		ts.synSentByDst[conn.dstIP]++
		if conn.dstPort > 0 {
			if ts.tcpSynDestPorts[conn.dstIP] == nil {
				ts.tcpSynDestPorts[conn.dstIP] = make(map[int]int)
			}
			ts.tcpSynDestPorts[conn.dstIP][conn.dstPort]++
			if ts.tcpSynPortDestCounts[conn.dstPort] == nil {
				ts.tcpSynPortDestCounts[conn.dstPort] = make(map[string]int)
			}
			ts.tcpSynPortDestCounts[conn.dstPort][conn.dstIP]++
			ts.tcpSynPortTotalCounts[conn.dstPort]++
		}
	}
}

func (ss *SecurityScanner) detectPortScans(name, ip string, stats *trafficStats) {
	for dstIP, portCounts := range stats.tcpSynDestPorts {
		uniquePorts := len(portCounts)
		switch {
		case uniquePorts >= 25:
			ss.addAlert(name, "port_scan", "high", ip, dstIP, 0,
				fmt.Sprintf("对同一目标 %s 发起 %d 个不同 TCP 半开端口探测", dstIP, uniquePorts),
				"")
		case uniquePorts >= 12:
			ss.addAlert(name, "port_scan", "medium", ip, dstIP, 0,
				fmt.Sprintf("对同一目标 %s 发起 %d 个不同 TCP 半开端口探测", dstIP, uniquePorts),
				"")
		}
	}

	for port, targets := range stats.tcpSynPortDestCounts {
		uniqueTargets := len(targets)
		if service, ok := bruteForcePorts[port]; ok {
			if uniqueTargets >= 30 {
				ss.addAlert(name, "brute_force", "critical", ip, "*", port,
					fmt.Sprintf("对 %s(%d) 服务发起半开连接并覆盖 %d 个不同 IP", service, port, uniqueTargets),
					"")
			} else if uniqueTargets >= 12 {
				ss.addAlert(name, "brute_force", "high", ip, "*", port,
					fmt.Sprintf("对 %s(%d) 服务发起半开连接并覆盖 %d 个不同 IP", service, port, uniqueTargets),
					"")
			}
			continue
		}

		if uniqueTargets >= 50 {
			ss.addAlert(name, "horizontal_scan", "high", ip, "*", port,
				fmt.Sprintf("对同一 TCP 端口 %d 发起半开连接并覆盖 %d 个不同目标", port, uniqueTargets),
				"")
		} else if uniqueTargets >= 20 {
			ss.addAlert(name, "horizontal_scan", "medium", ip, "*", port,
				fmt.Sprintf("对同一 TCP 端口 %d 发起半开连接并覆盖 %d 个不同目标", port, uniqueTargets),
				"")
		}
	}
}

func (ss *SecurityScanner) detectBruteForce(name, ip string, stats *trafficStats) {
	for dstIP, portCounts := range stats.destPorts {
		for port, count := range portCounts {
			service, sensitive := bruteForcePorts[port]
			if !sensitive {
				continue
			}

			synCount := 0
			if ports := stats.tcpSynDestPorts[dstIP]; ports != nil {
				synCount = ports[port]
			}
			if synCount >= 25 {
				ss.addAlert(name, "brute_force", "critical", ip, dstIP, port,
					fmt.Sprintf("对 %s(%d) 发起 %d 条 TCP 半开连接", service, port, synCount),
					"")
			} else if synCount >= 12 {
				ss.addAlert(name, "brute_force", "high", ip, dstIP, port,
					fmt.Sprintf("对 %s(%d) 发起 %d 条 TCP 半开连接", service, port, synCount),
					"")
			} else if count >= 60 {
				ss.addAlert(name, "brute_force", "critical", ip, dstIP, port,
					fmt.Sprintf("对 %s(%d) 维持 %d 条连接", service, port, count),
					"")
			} else if count >= 30 {
				ss.addAlert(name, "brute_force", "high", ip, dstIP, port,
					fmt.Sprintf("对 %s(%d) 维持 %d 条连接", service, port, count),
					"")
			}
		}
	}
}

func (ss *SecurityScanner) detectSpam(name, ip string, stats *trafficStats) {
	// 25 端口对外连接是 VPS 滥用的强信号（垃圾邮件 / 开放中继），单独从严判定。
	port25 := stats.portTotalCounts[25]
	port25Targets := len(stats.portDestCounts[25])
	switch {
	case port25 >= 10 || port25Targets >= 5:
		ss.addAlert(name, "spam", "critical", ip, "*", 25,
			fmt.Sprintf("SMTP(25) 对外连接 %d 条，覆盖 %d 个目标", port25, port25Targets),
			"")
	case port25 >= 3:
		ss.addAlert(name, "spam", "high", ip, "*", 25,
			fmt.Sprintf("SMTP(25) 对外连接 %d 条，覆盖 %d 个目标", port25, port25Targets),
			"")
	}

	total, targets := countPorts(stats.portTotalCounts, stats.portDestCounts, smtpPorts)
	if total == 0 {
		return
	}

	if targets >= 10 || total >= 30 {
		ss.addAlert(name, "spam", "critical", ip, "*", 25,
			fmt.Sprintf("SMTP 相关端口连接 %d 条，覆盖 %d 个目标", total, targets),
			"")
	} else if targets >= 2 || total >= 5 {
		ss.addAlert(name, "spam", "high", ip, "*", 25,
			fmt.Sprintf("SMTP 相关端口连接 %d 条，覆盖 %d 个目标", total, targets),
			"")
	}
}

func (ss *SecurityScanner) detectMassAbuse(name, ip string, stats *trafficStats) {
	targets := len(stats.destCounts)
	switch {
	case targets >= 120 && stats.total >= 600:
		ss.addAlert(name, "ddos", "critical", ip, "*", 0,
			fmt.Sprintf("对外连接 %d 条，覆盖 %d 个不同目标", stats.total, targets),
			"")
	case targets >= 60 && stats.total >= 300:
		ss.addAlert(name, "ddos", "high", ip, "*", 0,
			fmt.Sprintf("对外连接 %d 条，覆盖 %d 个不同目标", stats.total, targets),
			"")
	}

	synTargets := len(stats.synSentByDst)
	switch {
	case stats.totalSynSent >= 250 || (synTargets >= 80 && stats.totalSynSent >= 160):
		ss.addAlert(name, "ddos", "critical", ip, "*", 0,
			fmt.Sprintf("对外半开连接(SYN_SENT) %d 条，覆盖 %d 个不同目标", stats.totalSynSent, synTargets),
			"")
	case stats.totalSynSent >= 100 || (synTargets >= 35 && stats.totalSynSent >= 70):
		ss.addAlert(name, "ddos", "high", ip, "*", 0,
			fmt.Sprintf("对外半开连接(SYN_SENT) %d 条，覆盖 %d 个不同目标", stats.totalSynSent, synTargets),
			"")
	}

	udpTargets := len(stats.udpDestTotalCounts)
	udpTotal := 0
	for _, count := range stats.udpTotalCounts {
		udpTotal += count
	}
	switch {
	case udpTargets >= 120 && udpTotal >= 300:
		ss.addAlert(name, "ddos", "critical", ip, "*", 0,
			fmt.Sprintf("UDP 外发 %d 条，覆盖 %d 个不同目标", udpTotal, udpTargets),
			"")
	case udpTargets >= 50 && udpTotal >= 120:
		ss.addAlert(name, "ddos", "high", ip, "*", 0,
			fmt.Sprintf("UDP 外发 %d 条，覆盖 %d 个不同目标", udpTotal, udpTargets),
			"")
	}

	for dstIP, count := range stats.synSentByDst {
		if count >= 50 {
			ss.addAlert(name, "ddos", "critical", ip, dstIP, 0,
				fmt.Sprintf("对单一目标维持 %d 条半开连接", count),
				"")
		} else if count >= 20 {
			ss.addAlert(name, "ddos", "high", ip, dstIP, 0,
				fmt.Sprintf("对单一目标维持 %d 条半开连接", count),
				"")
		}
	}
}

// detectCC 检测 CC / HTTP 洪水：来自同一容器的大量 Web 端口连接，且高度集中在少数目标。
func (ss *SecurityScanner) detectCC(name, ip string, stats *trafficStats) {
	total := 0
	targets := make(map[string]int)
	for port := range webPorts {
		total += stats.portTotalCounts[port]
		for dstIP, count := range stats.portDestCounts[port] {
			targets[dstIP] += count
		}
	}
	if total == 0 {
		return
	}

	peakTarget, peakCount := "", 0
	for dstIP, count := range targets {
		if count > peakCount {
			peakTarget, peakCount = dstIP, count
		}
	}

	switch {
	case peakCount >= 150 || (total >= 300 && len(targets) <= 3):
		ss.addAlert(name, "cc", "critical", ip, peakTarget, 0,
			fmt.Sprintf("Web 端口连接 %d 条，单一目标最高 %d 条，覆盖 %d 个目标", total, peakCount, len(targets)),
			"")
	case peakCount >= 60 || (total >= 120 && len(targets) <= 8):
		ss.addAlert(name, "cc", "high", ip, peakTarget, 0,
			fmt.Sprintf("Web 端口连接 %d 条，单一目标最高 %d 条，覆盖 %d 个目标", total, peakCount, len(targets)),
			"")
	}
}

// detectP2P 检测 BT / PT（BitTorrent、私有 Tracker）滥用：
// 命中 BT/DHT/Tracker 常用端口，或呈现"大量高位端口对端"的群集特征。
func (ss *SecurityScanner) detectP2P(name, ip string, stats *trafficStats) {
	total := 0
	peers := make(map[string]struct{})
	for port := range btPorts {
		total += stats.portTotalCounts[port]
		for dstIP := range stats.portDestCounts[port] {
			peers[dstIP] = struct{}{}
		}
	}
	if total > 0 {
		severity := "high"
		if total >= 20 || len(peers) >= 10 {
			severity = "critical"
		}
		ss.addAlert(name, "p2p", severity, ip, "*", 0,
			fmt.Sprintf("BitTorrent 相关端口连接 %d 条，覆盖 %d 个节点", total, len(peers)),
			"")
		return
	}

	// 群集特征：与大量目标在高位端口（>=1024）建立连接，是 P2P 节点群的典型形态。
	highPortPeers := 0
	for _, portCounts := range stats.destPorts {
		for port := range portCounts {
			if port >= 1024 {
				highPortPeers++
				break
			}
		}
	}
	if highPortPeers >= 50 && stats.total >= 80 {
		ss.addAlert(name, "p2p", "high", ip, "*", 0,
			fmt.Sprintf("与 %d 个目标在高位端口建立连接（共 %d 条），呈现 P2P 群集特征", highPortPeers, stats.total),
			"")
	}
}

func (ss *SecurityScanner) detectReflectionAbuse(name, ip string, stats *trafficStats) {
	for port, service := range reflectionPorts {
		total := stats.udpTotalCounts[port]
		targets := len(stats.udpDestCounts[port])
		if total == 0 {
			continue
		}

		criticalTargets, criticalTotal := 40, 120
		highTargets, highTotal := 15, 45
		if port == 53 {
			criticalTargets, criticalTotal = 75, 300
			highTargets, highTotal = 25, 100
		}

		if targets >= criticalTargets && total >= criticalTotal {
			ss.addAlert(name, "reflection", "critical", ip, "*", port,
				fmt.Sprintf("%s(%d) UDP 外发 %d 条，覆盖 %d 个目标", service, port, total, targets),
				"")
		} else if targets >= highTargets && total >= highTotal {
			ss.addAlert(name, "reflection", "high", ip, "*", port,
				fmt.Sprintf("%s(%d) UDP 外发 %d 条，覆盖 %d 个目标", service, port, total, targets),
				"")
		}
	}
}

func (ss *SecurityScanner) detectMining(name, ip string, stats *trafficStats) {
	for port, service := range miningPorts {
		total := stats.portTotalCounts[port]
		if total == 0 {
			continue
		}

		severity := "high"
		if total >= 5 {
			severity = "critical"
		}
		ss.addAlert(name, "mining", severity, ip, "*", port,
			fmt.Sprintf("连接矿池端口 %s/%d 共 %d 条", service, port, total),
			"")
	}
}

func (ss *SecurityScanner) detectProxyAndTor(name, ip string, stats *trafficStats) {
	for port, service := range proxyPorts {
		total := stats.portTotalCounts[port]
		targets := len(stats.portDestCounts[port])
		if total == 0 {
			continue
		}

		if port == 1194 || port == 51820 {
			if targets < 3 && total < 10 {
				continue
			}
		}

		severity := "high"
		if targets >= 10 || total >= 30 {
			severity = "critical"
		}
		ss.addAlert(name, "proxy", severity, ip, "*", port,
			fmt.Sprintf("%s(%d) 连接 %d 条，覆盖 %d 个目标", service, port, total, targets),
			"")
	}

	total8080 := stats.portTotalCounts[8080]
	targets8080 := len(stats.portDestCounts[8080])
	if targets8080 >= 5 || total8080 >= 20 {
		ss.addAlert(name, "proxy", "high", ip, "*", 8080,
			fmt.Sprintf("HTTP 代理常用端口 8080 连接 %d 条，覆盖 %d 个目标", total8080, targets8080),
			"")
	}
}

func (ss *SecurityScanner) detectMalware(name, ip string, stats *trafficStats) {
	for port, label := range malwarePorts {
		total := stats.portTotalCounts[port]
		if total == 0 {
			continue
		}

		ss.addAlert(name, "malware", "critical", ip, "*", port,
			fmt.Sprintf("与 %s 端口 %d 存在 %d 条连接", label, port, total),
			"")
	}
}

// lateralMovementTargets 返回容器主动连接的内网高价值服务目标与连接总数。
// 已被入侵的容器常被用作跳板，向内网其它主机的高价值端口发起连接。
func lateralMovementTargets(stats *trafficStats) (map[string]struct{}, int) {
	targets := make(map[string]struct{})
	total := 0
	for dstIP, portCounts := range stats.destPorts {
		if !isPrivateIP(dstIP) {
			continue
		}
		for port, count := range portCounts {
			if _, ok := lateralSensitivePorts[port]; !ok {
				continue
			}
			targets[dstIP] = struct{}{}
			total += count
		}
	}
	return targets, total
}

func (ss *SecurityScanner) detectLateralMovement(name, ip string, stats *trafficStats) {
	targets, total := lateralMovementTargets(stats)
	if len(targets) < 3 {
		return
	}
	severity := "high"
	if len(targets) >= 6 || total >= 15 {
		severity = "critical"
	}
	ss.addAlert(name, "lateral_movement", severity, ip, "*", 0,
		fmt.Sprintf("向内网 %d 个主机的高价值服务端口发起 %d 条连接", len(targets), total),
		"")
}

// backdoorInboundPorts 返回容器在哪些后门/远控端口上收到了入站连接。
func backdoorInboundPorts(in *inboundStats) map[int]int {
	ports := make(map[int]int)
	for port, count := range in.portTotals {
		if count <= 0 {
			continue
		}
		if _, ok := backdoorPorts[port]; ok {
			ports[port] = count
		}
	}
	return ports
}

func (ss *SecurityScanner) detectBackdoorListener(name, ip string, in *inboundStats) {
	for port, count := range backdoorInboundPorts(in) {
		ss.addAlert(name, "backdoor", "critical", ip, "*", port,
			fmt.Sprintf("在 %s 端口 %d 上收到 %d 条入站连接", backdoorPorts[port], port, count),
			"")
	}
}

// inboundBruteForceReasons 返回容器正在被暴力破解的服务端口描述。
func inboundBruteForceReasons(in *inboundStats) []string {
	var reasons []string
	for port, peers := range in.portPeers {
		service, sensitive := bruteForcePorts[port]
		if !sensitive || len(peers) < 15 {
			continue
		}
		reasons = append(reasons, fmt.Sprintf("%s(%d) 被 %d 个不同来源尝试", service, port, len(peers)))
	}
	sort.Strings(reasons)
	return reasons
}

func (ss *SecurityScanner) detectInboundBruteForce(name, ip string, in *inboundStats) {
	for port, peers := range in.portPeers {
		service, sensitive := bruteForcePorts[port]
		if !sensitive {
			continue
		}
		n := len(peers)
		switch {
		case n >= 40:
			ss.addAlert(name, "inbound_brute_force", "critical", ip, "*", port,
				fmt.Sprintf("%s(%d) 被 %d 个不同来源尝试连接", service, port, n),
				"")
		case n >= 15:
			ss.addAlert(name, "inbound_brute_force", "high", ip, "*", port,
				fmt.Sprintf("%s(%d) 被 %d 个不同来源尝试连接", service, port, n),
				"")
		}
	}
}

// detectInboundDDoS 检测容器正在遭受的 DDoS：以半开（SYN_RECV）连接为依据，
// 避免把正常的高并发访问（大量已建立连接）误判为攻击。
func (ss *SecurityScanner) detectInboundDDoS(name, ip string, in *inboundStats) {
	for port, synRecv := range in.portSynRecv {
		peers := len(in.portSynPeers[port])
		switch {
		case synRecv >= 200 || peers >= 80:
			ss.addAlert(name, "inbound_ddos", "critical", ip, "*", port,
				fmt.Sprintf("服务端口 %d 收到 %d 条半开连接，来自 %d 个不同来源", port, synRecv, peers),
				"")
		case synRecv >= 100 || peers >= 30:
			ss.addAlert(name, "inbound_ddos", "high", ip, "*", port,
				fmt.Sprintf("服务端口 %d 收到 %d 条半开连接，来自 %d 个不同来源", port, synRecv, peers),
				"")
		}
	}
}

func (ss *SecurityScanner) detectInboundScan(name, ip string, in *inboundStats) {
	for peer, ports := range in.peerPorts {
		if len(ports) < 20 {
			continue
		}
		ss.addAlert(name, "inbound_scan", "high", ip, peer, 0,
			fmt.Sprintf("来源 %s 探测了 %d 个不同服务端口", peer, len(ports)),
			"")
	}
}

// compromiseReasons 汇总"疑似被入侵"的判定依据；阈值与各专项检测保持一致。
func compromiseReasons(out *trafficStats, in *inboundStats) []string {
	var reasons []string

	if ports := backdoorInboundPorts(in); len(ports) > 0 {
		reasons = append(reasons, fmt.Sprintf("存在后门/远控监听端口（%s）", formatPortList(ports)))
	}
	if targets, _ := lateralMovementTargets(out); len(targets) >= 3 {
		reasons = append(reasons, fmt.Sprintf("向内网 %d 个主机的高价值服务端口发起连接", len(targets)))
	}
	if ports := c2OutboundPorts(out); len(ports) > 0 {
		reasons = append(reasons, fmt.Sprintf("与恶意软件/C2 端口（%s）通信", formatPortList(ports)))
	}
	if list := inboundBruteForceReasons(in); len(list) > 0 {
		reasons = append(reasons, "正被暴力破解："+strings.Join(list, "、"))
	}
	return reasons
}

// detectCompromise 当同一容器同时出现多个入侵指标时，给出"疑似被入侵"总结性告警。
func (ss *SecurityScanner) detectCompromise(name, ip string, out *trafficStats, in *inboundStats) {
	reasons := compromiseReasons(out, in)
	if len(reasons) < 2 {
		return
	}
	ss.addAlert(name, compromiseAlertType, "critical", ip, "*", 0,
		strings.Join(reasons, "；"),
		"")
}

// c2OutboundPorts 返回容器对外通信命中的恶意软件/C2 端口。
func c2OutboundPorts(stats *trafficStats) map[int]int {
	ports := make(map[int]int)
	for port, count := range stats.portTotalCounts {
		if count <= 0 {
			continue
		}
		if _, ok := malwarePorts[port]; ok {
			ports[port] = count
		}
	}
	return ports
}

func formatPortList(ports map[int]int) string {
	keys := make([]int, 0, len(ports))
	for port := range ports {
		keys = append(keys, port)
	}
	sort.Ints(keys)
	parts := make([]string, 0, len(keys))
	for _, port := range keys {
		parts = append(parts, strconv.Itoa(port))
	}
	return strings.Join(parts, ",")
}

// 挖矿的 CPU 特征：连续多个采样周期接近满载（可发现使用非标准端口或 TLS 的矿机）。
const (
	miningSustainedCPUPct  = 90.0
	miningSustainedSamples = 6
)

// detectSustainedCPUMining 基于指标采样判断容器是否长期 CPU 满载。
// 与端口检测互补：矿机使用非标准端口/TLS 时端口特征会失效，但 CPU 仍会持续满载。
func (ss *SecurityScanner) detectSustainedCPUMining(c config.Container) {
	key := containerMetricKey(c)
	if key == "" {
		return
	}
	containerMetricMu.RLock()
	points := containerMetricHistory[key]
	if len(points) < miningSustainedSamples {
		containerMetricMu.RUnlock()
		return
	}
	tail := append([]ContainerMetricPoint(nil), points[len(points)-miningSustainedSamples:]...)
	containerMetricMu.RUnlock()

	sum := 0.0
	for _, p := range tail {
		if p.CPU < miningSustainedCPUPct {
			return
		}
		sum += p.CPU
	}
	avg := sum / float64(len(tail))
	ss.addAlert(c.Name, "mining", "high", c.IP, "*", 0,
		fmt.Sprintf("CPU 连续 %d 个采样周期持续满载（均值 %.0f%%），符合挖矿特征", len(tail), avg),
		"")
}

// conntrackAvailable 判断主机是否具备连接跟踪数据源。
// 滥用检测依赖 conntrack：若既无 conntrack 工具、也无 /proc/net/nf_conntrack，
// 则所有基于出站连接的检测（挖矿/VPN/BT/CC/25端口等）都不会生效，需要显式提示管理员。
func conntrackAvailable() bool {
	if _, err := exec.LookPath("conntrack"); err == nil {
		return true
	}
	for _, path := range []string{"/proc/net/nf_conntrack", "/proc/net/ip_conntrack"} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// readConntrackForContainer 读取与某个容器地址相关的连接记录（出站 + 入站）：
// 若提供了容器网络命名空间 PID（macvlan 场景），优先在容器 netns 内读取；
// 读不到时回退到宿主机 conntrack（NAT/桥接/KVM 场景）。
func readConntrackForContainer(ip, netnsPID string) []string {
	if netnsPID != "" {
		if lines := readConntrackInNetns(netnsPID, ip); len(lines) > 0 {
			return lines
		}
	}
	return readConntrackLines(ip)
}

// conntrackLineReferencesIP 判断 conntrack 记录是否引用了该 IP（任意方向元组）。
func conntrackLineReferencesIP(line, ip string) bool {
	for _, prefix := range []string{"src=", "dst="} {
		needle := prefix + ip
		idx := 0
		for {
			pos := strings.Index(line[idx:], needle)
			if pos < 0 {
				break
			}
			end := idx + pos + len(needle)
			// 必须是完整字段（后接空格或行尾），避免 1.2.3.4 误匹配 1.2.3.40。
			if end >= len(line) || line[end] == ' ' || line[end] == '\t' {
				return true
			}
			idx = end
		}
	}
	return false
}

// readConntrackInNetns 进入指定 PID 的网络命名空间读取 conntrack 条目。
// nsenter 只切换 net namespace，仍使用宿主机的 conntrack/cat 二进制。
func readConntrackInNetns(pid, ip string) []string {
	if _, err := exec.LookPath("nsenter"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 优先用 conntrack 工具分别按源/目的地址过滤，再合并去重。
	var lines []string
	seen := map[string]bool{}
	for _, filter := range []string{"-s", "-d"} {
		cmd := exec.CommandContext(ctx, "nsenter", "-t", pid, "-n", "conntrack", "-L", filter, ip)
		output, err := cmd.Output()
		if err != nil || len(output) == 0 {
			continue
		}
		for _, line := range splitNonEmptyLines(string(output)) {
			if seen[line] {
				continue
			}
			seen[line] = true
			lines = append(lines, line)
		}
	}
	if len(lines) > 0 {
		return lines
	}

	// 容器内若无 conntrack 工具，则直接读取该 netns 的 conntrack 表。
	cmd := exec.CommandContext(ctx, "nsenter", "-t", pid, "-n", "cat", "/proc/net/nf_conntrack")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	return filterConntrackLines(string(output), ip)
}

func readConntrackLines(ip string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var lines []string
	seen := map[string]bool{}
	for _, filter := range []string{"-s", "-d"} {
		cmd := exec.CommandContext(ctx, "conntrack", "-L", filter, ip)
		output, err := cmd.Output()
		if err != nil || len(output) == 0 {
			continue
		}
		for _, line := range splitNonEmptyLines(string(output)) {
			if seen[line] {
				continue
			}
			seen[line] = true
			lines = append(lines, line)
		}
	}
	if len(lines) > 0 {
		return lines
	}

	for _, path := range []string{"/proc/net/nf_conntrack", "/proc/net/ip_conntrack"} {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		lines = append(lines, filterConntrackLines(string(data), ip)...)
	}
	return lines
}

// filterConntrackLines 从 conntrack 表全量文本中筛出引用该 IP 的记录。
func filterConntrackLines(raw, ip string) []string {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if conntrackLineReferencesIP(line, ip) {
			lines = append(lines, line)
		}
	}
	return lines
}

func splitNonEmptyLines(raw string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// conntrackTuple 是 conntrack 记录中的一个方向元组（原始方向或应答方向）。
type conntrackTuple struct {
	src   string
	dst   string
	sport int
	dport int
}

// connDirection 表示连接相对容器的方向。
type connDirection int

const (
	connUnrelated connDirection = 0
	connOutbound  connDirection = 1 // 容器为发起方
	connInbound   connDirection = 2 // 容器为目的方
)

// conntrackPeer 是相对容器的"对端"信息。
type conntrackPeer struct {
	ip        string // 对端 IP
	port      int    // 对端端口
	localPort int    // 容器侧端口（入站时为容器服务端口）
	proto     string
	state     string
}

// parseConntrackTuples 解析 conntrack 记录中的方向元组。
// 记录形如：
//
//	tcp 6 431999 ESTABLISHED src=A dst=B sport=X dport=Y packets=.. bytes=.. \
//	    src=C dst=D sport=Z dport=W packets=.. bytes=.. [ASSURED] mark=0 use=1
//
// 即原始方向元组后紧跟应答方向元组，每个元组以 src= 开始。
func parseConntrackTuples(line string) []conntrackTuple {
	var tuples []conntrackTuple
	var cur *conntrackTuple
	for _, tok := range strings.Fields(line) {
		switch {
		case strings.HasPrefix(tok, "src="):
			if cur != nil {
				tuples = append(tuples, *cur)
			}
			cur = &conntrackTuple{src: strings.TrimPrefix(tok, "src=")}
		case strings.HasPrefix(tok, "dst="):
			if cur != nil {
				cur.dst = strings.TrimPrefix(tok, "dst=")
			}
		case strings.HasPrefix(tok, "sport="):
			if cur != nil {
				cur.sport, _ = strconv.Atoi(strings.TrimPrefix(tok, "sport="))
			}
		case strings.HasPrefix(tok, "dport="):
			if cur != nil {
				cur.dport, _ = strconv.Atoi(strings.TrimPrefix(tok, "dport="))
			}
		}
	}
	if cur != nil {
		tuples = append(tuples, *cur)
	}
	return tuples
}

// classifyConntrackTuples 判断该记录相对 containerIP 的方向，并给出对端信息。
// 兼容三种形态：
//   - 原始方向 src 即容器地址（出站，NAT 场景容器 LAN IP 作为源）；
//   - 应答方向 src 为容器地址（入站，NAT/端口映射后容器地址只出现在应答方向）；
//   - 原始方向 dst 即容器地址（入站，无 NAT 的 macvlan 场景）。
func classifyConntrackTuples(tuples []conntrackTuple, containerIP string) (connDirection, conntrackPeer, bool) {
	if len(tuples) == 0 {
		return connUnrelated, conntrackPeer{}, false
	}
	if tuples[0].src == containerIP {
		return connOutbound, conntrackPeer{ip: tuples[0].dst, port: tuples[0].dport, localPort: tuples[0].sport}, true
	}
	if len(tuples) >= 2 && tuples[1].src == containerIP {
		return connInbound, conntrackPeer{ip: tuples[0].src, port: tuples[0].sport, localPort: tuples[1].sport}, true
	}
	if tuples[0].dst == containerIP {
		return connInbound, conntrackPeer{ip: tuples[0].src, port: tuples[0].sport, localPort: tuples[0].dport}, true
	}
	return connUnrelated, conntrackPeer{}, false
}

func extractProtocol(line string) string {
	for _, field := range strings.Fields(line) {
		switch field {
		case "tcp", "udp", "icmp", "icmpv6", "sctp":
			return field
		}
	}
	return ""
}

func extractConnState(line string) string {
	for _, field := range strings.Fields(line) {
		switch field {
		case "SYN_SENT", "SYN_RECV", "ESTABLISHED", "TIME_WAIT", "CLOSE", "CLOSE_WAIT", "FIN_WAIT", "LAST_ACK", "UNREPLIED":
			return field
		}
	}
	return ""
}

func countPorts(totalCounts map[int]int, destCounts map[int]map[string]int, ports map[int]string) (int, int) {
	total := 0
	targets := make(map[string]struct{})
	for port := range ports {
		total += totalCounts[port]
		for dstIP := range destCounts[port] {
			targets[dstIP] = struct{}{}
		}
	}
	return total, len(targets)
}

// resolveAbuseOwnership 返回容器类型、所属租户与绑定了该容器的子用户（如有），
// 用于把滥用行为归因到用户/租户，并生成"xxx容器（LXC）"这类可读描述。
func resolveAbuseOwnership(containerName string) (tenant, owner, kind string) {
	containerName = strings.TrimSpace(containerName)
	if containerName == "" {
		return "", "", ""
	}

	// 一次读锁内完成容器与子用户的归属解析，避免锁外使用共享指针造成数据竞争。
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	if config.AppConfig == nil {
		return "", "", ""
	}

	var containerUUID string
	for i := range config.AppConfig.Containers {
		c := &config.AppConfig.Containers[i]
		if c.Name == containerName {
			tenant = strings.TrimSpace(c.Tenant)
			containerUUID = c.UUID
			kind = c.Runtime()
			break
		}
	}

	for i := range config.AppConfig.SubUsers {
		su := &config.AppConfig.SubUsers[i]
		if !subUserBindsContainer(su, containerName, containerUUID) {
			continue
		}
		if owner == "" {
			owner = strings.TrimSpace(su.Username)
		}
		if tenant == "" {
			tenant = strings.TrimSpace(su.Tenant)
		}
	}
	return tenant, owner, kind
}

func subUserBindsContainer(su *config.SubUser, name, uuid string) bool {
	for _, n := range su.ContainerNames {
		if strings.EqualFold(strings.TrimSpace(n), name) {
			return true
		}
	}
	if uuid != "" {
		for _, u := range su.ContainerUUIDs {
			if strings.TrimSpace(u) == uuid {
				return true
			}
		}
	}
	return false
}

func (ss *SecurityScanner) addAlert(name, alertType, severity, srcIP, dstIP string, port int, evidence, logLine string) {
	// 归属解析在持有扫描器锁之前完成，避免锁嵌套。
	tenant, owner, kind := resolveAbuseOwnership(name)
	// 统一文案："xxx（LXC）可能存在挖矿行为：证据" / "xxx（LXC）疑似被入侵：证据"。
	detail := abuseDetail(name, kind, alertType, evidence)

	ss.mu.Lock()

	now := time.Now()
	cutoff := now.Add(-5 * time.Minute)

	for i := range ss.alerts {
		a := &ss.alerts[i]
		if a.ContainerName != name || a.Type != alertType || a.TargetIP != dstIP || a.TargetPort != port {
			continue
		}
		t, err := time.Parse("2006-01-02 15:04:05", a.Timestamp)
		if err != nil || t.Before(cutoff) {
			continue
		}

		a.Count++
		a.Detail = detail
		a.LogLine = logLine
		a.Timestamp = now.Format("2006-01-02 15:04:05")
		a.Tenant = tenant
		a.Owner = owner
		if kind != "" {
			a.Kind = kind
		}
		if severityRank(severity) > severityRank(a.Severity) {
			a.Severity = severity
		}
		ss.mu.Unlock()
		// 关机开关在扫描器锁之外读取（自带读锁），避免锁嵌套与数据竞争。
		if securityAutoShutdownEnabled() {
			autoShutdownAlertContainer(name, alertType, severity)
		}
		return
	}

	ss.nextID++
	alert := SecurityAlert{
		ID:            fmt.Sprintf("alert-%d", ss.nextID),
		ContainerName: name,
		Kind:          kind,
		Tenant:        tenant,
		Owner:         owner,
		Type:          alertType,
		Severity:      severity,
		SourceIP:      srcIP,
		TargetIP:      dstIP,
		TargetPort:    port,
		Detail:        detail,
		LogLine:       logLine,
		Timestamp:     now.Format("2006-01-02 15:04:05"),
		Count:         1,
	}

	ss.alerts = append(ss.alerts, alert)
	config.AddAuditLog("security_"+alertType, name, fmt.Sprintf("[%s] %s", severity, detail), "system")

	if len(ss.alerts) > 200 {
		ss.alerts = ss.alerts[len(ss.alerts)-200:]
	}
	ss.mu.Unlock()

	// 新告警推送到外部通道（webhook / 邮件）
	NotifySecurityAlert(alert)

	if securityAutoShutdownEnabled() {
		autoShutdownAlertContainer(name, alertType, severity)
	}
}

func severityRank(severity string) int {
	switch severity {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

// securityAutoShutdownEnabled 在读锁下读取自动关机开关，避免与设置写入并发竞争。
func securityAutoShutdownEnabled() bool {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	return config.AppConfig != nil && config.AppConfig.SecurityAutoShutdown
}

// abuseDetectionEnabled 在读锁下读取滥用行为检测总开关。
func abuseDetectionEnabled() bool {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	return config.AppConfig == nil || config.AppConfig.AbuseDetectionEnabled
}

// findContainerSnapshot 返回容器的值拷贝，避免在锁外读取共享切片指针造成数据竞争。
func findContainerSnapshot(name string) (config.Container, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return config.Container{}, false
	}
	for _, c := range config.GetContainers() {
		if c.Name == name {
			return c, true
		}
	}
	return config.Container{}, false
}

func autoShutdownAlertContainer(containerName, alertType, severity string) {
	if !securityAutoShutdownEnabled() {
		return
	}
	// 用快照查找，避免锁外读取共享容器指针。
	target, found := findContainerSnapshot(containerName)
	if !found || target.Status != "running" {
		return
	}
	reason := fmt.Sprintf("%s 告警触发策略临时封禁", alertType)
	if severity != "" {
		reason = fmt.Sprintf("[%s] %s", severity, reason)
	}
	config.SetContainerPolicyBlock(target.ID, true, reason)
	taskID, queued := globalQueue.EnqueueSecurityStop(target.ID, target.Name)
	if queued {
		config.AddAuditLog("security_auto_shutdown", target.Name, fmt.Sprintf("[%s] %s 告警触发自动关机任务 %s", severity, alertType, taskID), "system")
	}
}

func clearSecurityPolicyBlocks() int {
	cleared := 0
	// 先取容器快照，避免直接遍历共享切片造成数据竞争；
	// SetContainerPolicyBlock 内部会再取写锁，因此不能在持有 AppConfigMu 时调用它。
	for _, c := range config.GetContainers() {
		if !c.PolicyBlocked || !isSecurityPolicyBlockReason(c.PolicyBlockedReason) {
			continue
		}
		config.SetContainerPolicyBlock(c.ID, false, "")
		config.AddAuditLog("security_policy_unblock", c.Name, "关闭安全告警自动关机后解除策略临时封禁", "system")
		cleared++
	}
	return cleared
}

func isSecurityPolicyBlockReason(reason string) bool {
	return strings.Contains(reason, "告警触发策略临时封禁")
}

// HandleSecurityAlerts returns all security alerts.
func HandleSecurityAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "security:read") {
		return
	}

	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: filterSecurityAlertsForRequest(r, mergedSecurityAlerts())})
}

// HandleSecuritySettings returns or updates security automation settings.
func HandleSecuritySettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "security:read") {
			return
		}
		config.AppConfigMu.RLock()
		autoShutdown := config.AppConfig.SecurityAutoShutdown
		arpProtection := config.AppConfig.ARPProtectionEnabled
		ipAntiSpoof := config.AppConfig.IPAntiSpoofEnabled
		abuseDetection := config.AppConfig.AbuseDetectionEnabled
		config.AppConfigMu.RUnlock()
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]bool{
			"auto_shutdown":   autoShutdown,
			"arp_protection":  arpProtection,
			"ip_anti_spoof":   ipAntiSpoof,
			"abuse_detection": abuseDetection,
		}})
	case http.MethodPut:
		if !requireScope(w, r, "security:settings") {
			return
		}
		var req struct {
			AutoShutdown   *bool `json:"auto_shutdown"`
			ARPProtection  *bool `json:"arp_protection"`
			IPAntiSpoof    *bool `json:"ip_anti_spoof"`
			AbuseDetection *bool `json:"abuse_detection"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			if req.AutoShutdown != nil {
				cfg.SecurityAutoShutdown = *req.AutoShutdown
			}
			if req.ARPProtection != nil {
				cfg.ARPProtectionEnabled = *req.ARPProtection
			}
			if req.IPAntiSpoof != nil {
				cfg.IPAntiSpoofEnabled = *req.IPAntiSpoof
			}
			if req.AbuseDetection != nil {
				cfg.AbuseDetectionEnabled = *req.AbuseDetection
			}
		})
		if err := config.SaveConfig(); err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		// 开关变更后立即生效：开启则下发规则，关闭则清理自有链。
		reconcileIPAntiSpoof()
		cancelledTasks := 0
		clearedBlocks := 0
		if req.AutoShutdown != nil && !*req.AutoShutdown {
			cancelledTasks = globalQueue.CancelPendingSecurityStops()
			clearedBlocks = clearSecurityPolicyBlocks()
		}
		// 读取最新设置用于回显：在读锁下快照，避免与其它写入并发竞争。
		config.AppConfigMu.RLock()
		autoShutdown := config.AppConfig.SecurityAutoShutdown
		arpProtection := config.AppConfig.ARPProtectionEnabled
		ipAntiSpoof := config.AppConfig.IPAntiSpoofEnabled
		abuseDetection := config.AppConfig.AbuseDetectionEnabled
		config.AppConfigMu.RUnlock()
		auditRequest(r, "security.settings", "auto_shutdown",
			fmt.Sprintf("auto_shutdown=%v arp_protection=%v ip_anti_spoof=%v abuse_detection=%v", autoShutdown, arpProtection, ipAntiSpoof, abuseDetection), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
			"auto_shutdown":   autoShutdown,
			"arp_protection":  arpProtection,
			"ip_anti_spoof":   ipAntiSpoof,
			"abuse_detection": abuseDetection,
			"cancelled_tasks": cancelledTasks,
			"cleared_blocks":  clearedBlocks,
		}})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleSecurityCheck triggers immediate security check for a container.
func HandleSecurityCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "security:check") {
		return
	}

	var req struct {
		ContainerName string `json:"container_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}

	c := config.FindContainerByName(req.ContainerName)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found or not running"})
		return
	}
	if !isContainerAllowedForRequest(r, c.UUID) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this container"})
		return
	}
	if !abuseDetectionEnabled() {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "滥用行为检测已关闭，未执行检查"})
		return
	}

	target, found := findContainerSnapshot(req.ContainerName)
	if !found || target.Status != "running" {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found or not running"})
		return
	}
	addresses := containerMonitoredAddresses(target)
	if len(addresses) == 0 {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container has no monitored address"})
		return
	}
	ss := ensureScanner()
	netnsPID := containerConntrackNetnsPID(target)
	for _, address := range addresses {
		ss.checkContainer(target.Name, address, netnsPID)
	}
	ss.detectSustainedCPUMining(target)
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Security check completed"})
}

// HandleSecurityLogs returns connection logs for a container.
func HandleSecurityLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "security:read") {
		return
	}

	containerName := r.URL.Query().Get("container")
	if containerName == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Container name required"})
		return
	}

	c := config.FindContainerByName(containerName)
	if c == nil {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: []map[string]interface{}{}})
		return
	}
	if !isContainerAllowedForRequest(r, c.UUID) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this container"})
		return
	}
	target, found := findContainerSnapshot(containerName)
	if !found {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: []map[string]interface{}{}})
		return
	}
	addresses := containerMonitoredAddresses(target)
	if len(addresses) == 0 {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: []map[string]interface{}{}})
		return
	}
	netnsPID := containerConntrackNetnsPID(target)
	logs := make([]map[string]interface{}, 0)
	for _, address := range addresses {
		logs = append(logs, getConnectionLogs(address, netnsPID)...)
		if len(logs) >= 100 {
			logs = logs[:100]
			break
		}
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: logs})
}

func getConnectionLogs(ip, netnsPID string) []map[string]interface{} {
	logs := make([]map[string]interface{}, 0)
	seen := map[string]bool{}

	parseLine := func(line string) map[string]interface{} {
		srcIP := extractField(line, "src=")
		dstIP := extractField(line, "dst=")
		srcPort := extractField(line, "sport=")
		dstPort := extractField(line, "dport=")
		sPort, _ := strconv.Atoi(srcPort)
		dPort, _ := strconv.Atoi(dstPort)
		return map[string]interface{}{
			"src_ip":   srcIP,
			"dst_ip":   dstIP,
			"src_port": sPort,
			"dst_port": dPort,
			"protocol": extractProtocol(line),
			"state":    extractConnState(line),
		}
	}

	// First, load stored snapshots from database (persisted at alert time).
	for _, line := range config.GetConntrackSnapshotLines(ip) {
		if len(logs) >= 100 {
			break
		}
		key := strings.TrimSpace(line)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		logs = append(logs, parseLine(line))
	}

	// Then, merge live conntrack data (deduplicated).
	for _, line := range readConntrackForContainer(ip, netnsPID) {
		if len(logs) >= 100 {
			break
		}
		key := strings.TrimSpace(line)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		logs = append(logs, parseLine(line))
	}

	return logs
}

func extractField(line, prefix string) string {
	idx := strings.Index(line, prefix)
	if idx == -1 {
		return ""
	}
	start := idx + len(prefix)
	end := start
	for end < len(line) && line[end] != ' ' && line[end] != '\t' {
		end++
	}
	return line[start:end]
}

// HandleContainerSecuritySummary returns security status for dashboard.
func HandleContainerSecuritySummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "security:read") {
		return
	}

	critical := 0
	high := 0
	medium := 0
	low := 0
	alerts := filterSecurityAlertsForRequest(r, mergedSecurityAlerts())
	for _, a := range alerts {
		switch a.Severity {
		case "critical":
			critical++
		case "high":
			high++
		case "medium":
			medium++
		case "low":
			low++
		}
	}
	total := len(alerts)

	summary := map[string]interface{}{
		"total_alerts": total,
		"critical":     critical,
		"high":         high,
		"medium":       medium,
		"low":          low,
		// conntrack_available 为 false 时，基于出站连接的滥用检测不会生效，需提示管理员。
		"conntrack_available": conntrackAvailable(),
		// abuse_detection_enabled 为 false 时，滥用行为检测被管理员关闭。
		"abuse_detection_enabled": abuseDetectionEnabled(),
	}

	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: summary})
}

// AbuseOwnerSummary 是按用户/租户聚合的滥用统计。
type AbuseOwnerSummary struct {
	Owner      string         `json:"owner"`
	Tenant     string         `json:"tenant,omitempty"`
	Alerts     int            `json:"alerts"`
	Containers []string       `json:"containers"`
	Types      map[string]int `json:"types"`
	Severity   string         `json:"severity"`
	LastSeen   string         `json:"last_seen"`
}

// HandleAbuseSummary 返回按用户/租户与类型聚合的滥用记录，供管理端归因查看。
// 滥用类型包括挖矿(mining)、代理/VPN(proxy)、DDoS(ddos)、CC/HTTP 洪水(cc)、
// 端口扫描(port_scan)、爆破(brute_force)、垃圾邮件(spam)、反射放大(reflection)、
// 恶意软件(malware)、ARP 欺骗(arp_spoof) 等。
func HandleAbuseSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "security:read") {
		return
	}

	alerts := filterSecurityAlertsForRequest(r, mergedSecurityAlerts())

	byOwner := make(map[string]*AbuseOwnerSummary)
	containersByOwner := make(map[string]map[string]struct{})
	byType := make(map[string]int)

	for _, a := range alerts {
		tenant, owner := a.Tenant, a.Owner
		if tenant == "" && owner == "" {
			tenant, owner, _ = resolveAbuseOwnership(a.ContainerName)
		}
		key := tenant + "\x1f" + owner
		entry := byOwner[key]
		if entry == nil {
			entry = &AbuseOwnerSummary{Owner: owner, Tenant: tenant, Types: make(map[string]int)}
			byOwner[key] = entry
			containersByOwner[key] = make(map[string]struct{})
		}

		count := a.Count
		if count < 1 {
			count = 1
		}
		entry.Alerts += count
		entry.Types[a.Type] += count
		byType[a.Type] += count
		containersByOwner[key][a.ContainerName] = struct{}{}
		if severityRank(a.Severity) > severityRank(entry.Severity) {
			entry.Severity = a.Severity
		}
		if a.Timestamp > entry.LastSeen {
			entry.LastSeen = a.Timestamp
		}
	}

	result := make([]AbuseOwnerSummary, 0, len(byOwner))
	for key, entry := range byOwner {
		for name := range containersByOwner[key] {
			entry.Containers = append(entry.Containers, name)
		}
		sort.Strings(entry.Containers)
		result = append(result, *entry)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Alerts != result[j].Alerts {
			return result[i].Alerts > result[j].Alerts
		}
		return result[i].LastSeen > result[j].LastSeen
	})

	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
		"total_alerts": len(alerts),
		"by_owner":     result,
		"by_type":      byType,
	}})
}

func filterSecurityAlertsForRequest(r *http.Request, alerts []SecurityAlert) []SecurityAlert {
	allowed, restricted := requestAllowedContainers(r)
	if !restricted {
		return alerts
	}
	filtered := make([]SecurityAlert, 0, len(alerts))
	for _, alert := range alerts {
		if c := config.FindContainerByName(alert.ContainerName); c != nil && isContainerAllowed(allowed, c) {
			filtered = append(filtered, alert)
		}
	}
	return filtered
}

func mergedSecurityAlerts() []SecurityAlert {
	ss := ensureScanner()
	ss.mu.Lock()
	alerts := make([]SecurityAlert, len(ss.alerts))
	copy(alerts, ss.alerts)
	ss.mu.Unlock()

	seen := make(map[string]bool)
	for _, alert := range alerts {
		seen[securityAlertKey(alert)] = true
	}

	// 快照审计日志：AddAuditLog 会在其它 goroutine 并发 append，直接遍历
	// AppConfig.AuditLogs 会与写入竞争（切片头撕裂/越界），必须先加读锁拷贝。
	config.AppConfigMu.RLock()
	auditLogs := append([]config.AuditLog(nil), config.AppConfig.AuditLogs...)
	config.AppConfigMu.RUnlock()

	for i, log := range auditLogs {
		alert, ok := alertFromSecurityAuditLog(log, i)
		if !ok {
			continue
		}
		key := securityAlertKey(alert)
		if seen[key] {
			continue
		}
		seen[key] = true
		alerts = append(alerts, alert)
	}

	sort.SliceStable(alerts, func(i, j int) bool {
		ti, errI := time.Parse("2006-01-02 15:04:05", alerts[i].Timestamp)
		tj, errJ := time.Parse("2006-01-02 15:04:05", alerts[j].Timestamp)
		if errI == nil && errJ == nil && !ti.Equal(tj) {
			return ti.After(tj)
		}
		return alerts[i].Timestamp > alerts[j].Timestamp
	})

	if len(alerts) > 200 {
		alerts = alerts[:200]
	}
	if alerts == nil {
		return []SecurityAlert{}
	}
	return alerts
}

func securityAlertKey(alert SecurityAlert) string {
	return strings.Join([]string{
		alert.Timestamp,
		alert.ContainerName,
		alert.Type,
		alert.Detail,
		strconv.Itoa(alert.TargetPort),
	}, "\x1f")
}

func alertFromSecurityAuditLog(log config.AuditLog, index int) (SecurityAlert, bool) {
	if !strings.HasPrefix(log.Action, "security_") || log.Action == "security_auto_shutdown" || log.Action == "security_policy_unblock" {
		return SecurityAlert{}, false
	}
	alertType := strings.TrimPrefix(log.Action, "security_")
	severity, detail := parseSecurityAuditDetail(log.Detail)
	targetPort := parseDetailPort(detail)

	targetIP := ""
	if targetPort > 0 || alertType == "horizontal_scan" || alertType == "brute_force" {
		targetIP = "*"
	}

	return SecurityAlert{
		ID:            fmt.Sprintf("audit-security-%d", index),
		ContainerName: log.Target,
		Type:          alertType,
		Severity:      severity,
		SourceIP:      "",
		TargetIP:      targetIP,
		TargetPort:    targetPort,
		Detail:        detail,
		LogLine:       "",
		Timestamp:     log.Time,
		Count:         1,
	}, true
}

func parseSecurityAuditDetail(detail string) (string, string) {
	severity := "medium"
	if strings.HasPrefix(detail, "[") {
		if end := strings.Index(detail, "]"); end > 1 {
			severity = detail[1:end]
			detail = strings.TrimSpace(detail[end+1:])
		}
	}
	return severity, detail
}

func parseDetailPort(detail string) int {
	for _, marker := range []string{"端口 ", "端口"} {
		idx := strings.Index(detail, marker)
		if idx == -1 {
			continue
		}
		start := idx + len(marker)
		for start < len(detail) && (detail[start] == ' ' || detail[start] == ':' || detail[start] == '(') {
			start++
		}
		end := start
		for end < len(detail) && detail[end] >= '0' && detail[end] <= '9' {
			end++
		}
		if end > start {
			port, _ := strconv.Atoi(detail[start:end])
			return port
		}
	}
	return 0
}
