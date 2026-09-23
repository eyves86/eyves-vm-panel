package api

import (
	"context"
	"encoding/json"
	"fmt"
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
	// Tenant / Owner 记录滥用行为的归属：Tenant 为容器所属租户，Owner 为绑定了该容器的
	// 子用户（若存在）。便于管理端按用户/租户归因与统计，而不只是按容器。
	Tenant     string `json:"tenant,omitempty"`
	Owner      string `json:"owner,omitempty"`
	Type       string `json:"type"`     // port_scan, horizontal_scan, brute_force, ddos, cc, spam, malware, mining, proxy, reflection
	Severity   string `json:"severity"` // low, medium, high, critical
	SourceIP   string `json:"source_ip"`
	TargetIP   string `json:"target_ip"`
	TargetPort int    `json:"target_port"`
	Detail     string `json:"detail"`
	LogLine    string `json:"log_line"`
	Timestamp  string `json:"timestamp"`
	Count      int    `json:"count"`
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
	31337: "Back Orifice",
	4444:  "Metasploit/reverse shell",
	5555:  "Android debug/reverse shell",
	6666:  "IRC botnet",
	6667:  "IRC botnet",
	6697:  "IRC over TLS",
	9050:  "Tor/C2 proxy",
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
		// macvlan（LAN IPv4 模式）容器的出站流量不经过宿主机协议栈，
		// 需要进入容器网络命名空间读取 conntrack；NAT/桥接容器仍用宿主机 conntrack。
		netnsPID := containerConntrackNetnsPID(c)
		// 逐一对容器的所有地址检查出站连接：容器可能只持有公网 IP（macvlan），
		// 只监控 c.IP 会漏掉这类容器的滥用行为。
		for _, address := range containerMonitoredAddresses(c) {
			ss.checkContainer(c.Name, address, netnsPID)
		}
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
					fmt.Sprintf("ARP 地址冲突/欺骗: IP %s 邻居表 MAC %s 与容器绑定 MAC %s 不一致", address, lladdr, c.MACAddress),
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

	stats := newTrafficStats()
	for _, line := range lines {
		conn, ok := parseConntrackLine(line, ip)
		if !ok || conn.dstIP == "" || conn.dstIP == ip {
			continue
		}
		stats.add(conn)
	}

	if stats.total == 0 {
		return
	}

	alertBefore := ss.alertCount()
	ss.detectPortScans(name, ip, stats)
	ss.detectBruteForce(name, ip, stats)
	ss.detectSpam(name, ip, stats)
	ss.detectMassAbuse(name, ip, stats)
	ss.detectCC(name, ip, stats)
	ss.detectP2P(name, ip, stats)
	ss.detectReflectionAbuse(name, ip, stats)
	ss.detectMining(name, ip, stats)
	ss.detectProxyAndTor(name, ip, stats)
	ss.detectMalware(name, ip, stats)

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
				fmt.Sprintf("端口扫描: 同一目标 %s 出现 %d 个不同 TCP 半开目标端口", dstIP, uniquePorts),
				"")
		case uniquePorts >= 12:
			ss.addAlert(name, "port_scan", "medium", ip, dstIP, 0,
				fmt.Sprintf("可疑端口探测: 同一目标 %s 出现 %d 个不同 TCP 半开目标端口", dstIP, uniquePorts),
				"")
		}
	}

	for port, targets := range stats.tcpSynPortDestCounts {
		uniqueTargets := len(targets)
		if service, ok := bruteForcePorts[port]; ok {
			if uniqueTargets >= 30 {
				ss.addAlert(name, "brute_force", "critical", ip, "*", port,
					fmt.Sprintf("横向爆破: 目标服务 %s(%d) 出现 TCP 半开连接并覆盖 %d 个不同 IP", service, port, uniqueTargets),
					"")
			} else if uniqueTargets >= 12 {
				ss.addAlert(name, "brute_force", "high", ip, "*", port,
					fmt.Sprintf("疑似横向爆破: 目标服务 %s(%d) 出现 TCP 半开连接并覆盖 %d 个不同 IP", service, port, uniqueTargets),
					"")
			}
			continue
		}

		if uniqueTargets >= 50 {
			ss.addAlert(name, "horizontal_scan", "high", ip, "*", port,
				fmt.Sprintf("横向扫描: 同一 TCP 端口 %d 出现半开连接并覆盖 %d 个不同目标", port, uniqueTargets),
				"")
		} else if uniqueTargets >= 20 {
			ss.addAlert(name, "horizontal_scan", "medium", ip, "*", port,
				fmt.Sprintf("可疑横向探测: 同一 TCP 端口 %d 出现半开连接并覆盖 %d 个不同目标", port, uniqueTargets),
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
					fmt.Sprintf("暴力破解: %s(%d) 当前 TCP 半开连接 %d 条", service, port, synCount),
					"")
			} else if synCount >= 12 {
				ss.addAlert(name, "brute_force", "high", ip, dstIP, port,
					fmt.Sprintf("疑似暴力破解: %s(%d) 当前 TCP 半开连接 %d 条", service, port, synCount),
					"")
			} else if count >= 60 {
				ss.addAlert(name, "brute_force", "critical", ip, dstIP, port,
					fmt.Sprintf("暴力破解: %s(%d) 当前连接数 %d 条", service, port, count),
					"")
			} else if count >= 30 {
				ss.addAlert(name, "brute_force", "high", ip, dstIP, port,
					fmt.Sprintf("疑似暴力破解: %s(%d) 当前连接数 %d 条", service, port, count),
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
			fmt.Sprintf("SMTP(25) 对外连接异常: 连接 %d 条，覆盖 %d 个目标", port25, port25Targets),
			"")
	case port25 >= 3:
		ss.addAlert(name, "spam", "high", ip, "*", 25,
			fmt.Sprintf("SMTP(25) 对外连接: 连接 %d 条，覆盖 %d 个目标", port25, port25Targets),
			"")
	}

	total, targets := countPorts(stats.portTotalCounts, stats.portDestCounts, smtpPorts)
	if total == 0 {
		return
	}

	if targets >= 10 || total >= 30 {
		ss.addAlert(name, "spam", "critical", ip, "*", 25,
			fmt.Sprintf("疑似垃圾邮件: SMTP 相关端口当前连接 %d 条，覆盖 %d 个目标", total, targets),
			"")
	} else if targets >= 2 || total >= 5 {
		ss.addAlert(name, "spam", "high", ip, "*", 25,
			fmt.Sprintf("可疑邮件发送: SMTP 相关端口当前连接 %d 条，覆盖 %d 个目标", total, targets),
			"")
	}
}

func (ss *SecurityScanner) detectMassAbuse(name, ip string, stats *trafficStats) {
	targets := len(stats.destCounts)
	switch {
	case targets >= 120 && stats.total >= 600:
		ss.addAlert(name, "ddos", "critical", ip, "*", 0,
			fmt.Sprintf("大规模对外连接: 当前 conntrack 出站记录 %d 条，覆盖 %d 个不同目标", stats.total, targets),
			"")
	case targets >= 60 && stats.total >= 300:
		ss.addAlert(name, "ddos", "high", ip, "*", 0,
			fmt.Sprintf("大量对外连接: 当前 conntrack 出站记录 %d 条，覆盖 %d 个不同目标", stats.total, targets),
			"")
	}

	synTargets := len(stats.synSentByDst)
	switch {
	case stats.totalSynSent >= 250 || (synTargets >= 80 && stats.totalSynSent >= 160):
		ss.addAlert(name, "ddos", "critical", ip, "*", 0,
			fmt.Sprintf("大量半开连接: 当前 TCP SYN_SENT %d 条，覆盖 %d 个不同目标", stats.totalSynSent, synTargets),
			"")
	case stats.totalSynSent >= 100 || (synTargets >= 35 && stats.totalSynSent >= 70):
		ss.addAlert(name, "ddos", "high", ip, "*", 0,
			fmt.Sprintf("可疑大量半开连接: 当前 TCP SYN_SENT %d 条，覆盖 %d 个不同目标", stats.totalSynSent, synTargets),
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
			fmt.Sprintf("UDP 大规模外发: 当前 UDP 连接 %d 条，覆盖 %d 个不同目标", udpTotal, udpTargets),
			"")
	case udpTargets >= 50 && udpTotal >= 120:
		ss.addAlert(name, "ddos", "high", ip, "*", 0,
			fmt.Sprintf("可疑 UDP 大规模外发: 当前 UDP 连接 %d 条，覆盖 %d 个不同目标", udpTotal, udpTargets),
			"")
	}

	for dstIP, count := range stats.synSentByDst {
		if count >= 50 {
			ss.addAlert(name, "ddos", "critical", ip, dstIP, 0,
				fmt.Sprintf("SYN 洪水: 单一目标半开连接 %d 条", count),
				"")
		} else if count >= 20 {
			ss.addAlert(name, "ddos", "high", ip, dstIP, 0,
				fmt.Sprintf("可疑 SYN 洪水: 单一目标半开连接 %d 条", count),
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
			fmt.Sprintf("疑似 CC/HTTP 洪水: Web 端口连接 %d 条，单一目标最高 %d 条，覆盖 %d 个目标", total, peakCount, len(targets)),
			"")
	case peakCount >= 60 || (total >= 120 && len(targets) <= 8):
		ss.addAlert(name, "cc", "high", ip, peakTarget, 0,
			fmt.Sprintf("可疑 CC/HTTP 洪水: Web 端口连接 %d 条，单一目标最高 %d 条，覆盖 %d 个目标", total, peakCount, len(targets)),
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
			fmt.Sprintf("疑似 BT/PT 下载: BitTorrent 相关端口连接 %d 条，覆盖 %d 个节点", total, len(peers)),
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
			fmt.Sprintf("疑似 P2P/BT 群集: 与 %d 个目标在高位端口建立连接（共 %d 条）", highPortPeers, stats.total),
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
				fmt.Sprintf("UDP 反射放大: %s(%d) 当前 UDP 连接 %d 条，覆盖 %d 个目标", service, port, total, targets),
				"")
		} else if targets >= highTargets && total >= highTotal {
			ss.addAlert(name, "reflection", "high", ip, "*", port,
				fmt.Sprintf("疑似 UDP 反射放大: %s(%d) 当前 UDP 连接 %d 条，覆盖 %d 个目标", service, port, total, targets),
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
			fmt.Sprintf("疑似挖矿连接: %s/%d 当前连接 %d 条", service, port, total),
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
			fmt.Sprintf("疑似代理/VPN/Tor 滥用: %s(%d) 当前连接 %d 条，覆盖 %d 个目标", service, port, total, targets),
			"")
	}

	total8080 := stats.portTotalCounts[8080]
	targets8080 := len(stats.portDestCounts[8080])
	if targets8080 >= 5 || total8080 >= 20 {
		ss.addAlert(name, "proxy", "high", ip, "*", 8080,
			fmt.Sprintf("疑似开放代理流量: HTTP 代理常用端口 8080 当前连接 %d 条，覆盖 %d 个目标", total8080, targets8080),
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
			fmt.Sprintf("疑似恶意软件/C2 连接: %s 端口 %d 当前连接 %d 条", label, port, total),
			"")
	}
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

// readConntrackForContainer 读取某个容器地址的出站连接：
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

// readConntrackInNetns 进入指定 PID 的网络命名空间读取 conntrack 条目。
// nsenter 只切换 net namespace，仍使用宿主机的 conntrack/cat 二进制。
func readConntrackInNetns(pid, ip string) []string {
	if _, err := exec.LookPath("nsenter"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 优先用 conntrack 工具按源地址过滤。
	cmd := exec.CommandContext(ctx, "nsenter", "-t", pid, "-n", "conntrack", "-L", "-s", ip)
	if output, err := cmd.Output(); err == nil && len(output) > 0 {
		return splitNonEmptyLines(string(output))
	}

	// 容器内若无 conntrack 工具，则直接读取该 netns 的 conntrack 表。
	cmd = exec.CommandContext(ctx, "nsenter", "-t", pid, "-n", "cat", "/proc/net/nf_conntrack")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "src="+ip+" ") {
			lines = append(lines, line)
		}
	}
	return lines
}

func readConntrackLines(ip string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "conntrack", "-L", "-s", ip)
	output, err := cmd.Output()
	if err == nil && len(output) > 0 {
		return splitNonEmptyLines(string(output))
	}

	var lines []string
	for _, path := range []string{"/proc/net/nf_conntrack", "/proc/net/ip_conntrack"} {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if strings.Contains(line, "src="+ip+" ") {
				lines = append(lines, line)
			}
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

func parseConntrackLine(line, containerIP string) (connEntry, bool) {
	srcIP := extractField(line, "src=")
	if srcIP != containerIP {
		return connEntry{}, false
	}

	dstIP := extractField(line, "dst=")
	dstPort, _ := strconv.Atoi(extractField(line, "dport="))

	return connEntry{
		dstIP:   dstIP,
		dstPort: dstPort,
		proto:   extractProtocol(line),
		state:   extractConnState(line),
		line:    line,
	}, true
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

// resolveAbuseOwnership 返回容器所属租户与绑定了该容器的子用户（如有），
// 用于把滥用行为归因到用户/租户，而不仅是容器。
func resolveAbuseOwnership(containerName string) (tenant, owner string) {
	containerName = strings.TrimSpace(containerName)
	if containerName == "" {
		return "", ""
	}

	// 一次读锁内完成容器与子用户的归属解析，避免锁外使用共享指针造成数据竞争。
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	if config.AppConfig == nil {
		return "", ""
	}

	var containerUUID string
	for i := range config.AppConfig.Containers {
		c := &config.AppConfig.Containers[i]
		if c.Name == containerName {
			tenant = strings.TrimSpace(c.Tenant)
			containerUUID = c.UUID
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
	return tenant, owner
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

func (ss *SecurityScanner) addAlert(name, alertType, severity, srcIP, dstIP string, port int, detail, logLine string) {
	// 归属解析在持有扫描器锁之前完成，避免锁嵌套。
	tenant, owner := resolveAbuseOwnership(name)

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

func autoShutdownAlertContainer(containerName, alertType, severity string) {
	if !securityAutoShutdownEnabled() {
		return
	}
	// 用快照查找，避免锁外读取共享容器指针。
	var target config.Container
	found := false
	for _, c := range config.GetContainers() {
		if c.Name == containerName {
			target = c
			found = true
			break
		}
	}
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
		config.AppConfigMu.RUnlock()
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]bool{
			"auto_shutdown":  autoShutdown,
			"arp_protection": arpProtection,
			"ip_anti_spoof":  ipAntiSpoof,
		}})
	case http.MethodPut:
		if !requireScope(w, r, "security:settings") {
			return
		}
		var req struct {
			AutoShutdown  *bool `json:"auto_shutdown"`
			ARPProtection *bool `json:"arp_protection"`
			IPAntiSpoof   *bool `json:"ip_anti_spoof"`
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
		config.AppConfigMu.RUnlock()
		auditRequest(r, "security.settings", "auto_shutdown",
			fmt.Sprintf("auto_shutdown=%v arp_protection=%v ip_anti_spoof=%v", autoShutdown, arpProtection, ipAntiSpoof), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
			"auto_shutdown":   autoShutdown,
			"arp_protection":  arpProtection,
			"ip_anti_spoof":   ipAntiSpoof,
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
	if c == nil || c.IP == "" {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found or not running"})
		return
	}
	if !isContainerAllowedForRequest(r, c.UUID) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this container"})
		return
	}

	ensureScanner().checkContainer(c.Name, c.IP, containerConntrackNetnsPID(*c))
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
	if c == nil || c.IP == "" {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: []map[string]interface{}{}})
		return
	}
	if !isContainerAllowedForRequest(r, c.UUID) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this container"})
		return
	}

	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: getConnectionLogs(c.IP, containerConntrackNetnsPID(*c))})
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
			tenant, owner = resolveAbuseOwnership(a.ContainerName)
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
