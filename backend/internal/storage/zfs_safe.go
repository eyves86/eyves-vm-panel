package storage

import (
	"fmt"
	"strings"
)

// ZFS dataset 名字规则（简化版，依据 OpenZFS 文档）：
//   - 字符集：[A-Za-z0-9_.:-]
//   - 不允许：空名、纯 "." / ".."、以 "@" 或 "#" 结尾（含快照/书签标识）、含 "/"
//   - "/" 是层级分隔符，由调用方显式 Join（zfsSafeJoinUnder），不接受片段含 "/"
//
// ZFS 池根 dataset 也遵循上述规则（不允许 `.` / `..`），且不能以 "/" 开头
// （dataset 路径相对 zfs 根而言）。
//
// ZFS 池根与 dir 池根的区别：
//   - dir 池 PoolPath = 文件系统绝对路径（如 /mnt/data/eyvescloud）
//   - ZFS 池 PoolPath = dataset 路径（如 tank/eyvescloud/disk-data），相对 zfs root
//
// safeJoinUnder 处理绝对路径文件系统根（dir 后端），zfsSafeJoinUnder
// 处理相对 zfs 根的 dataset 路径（ZFS 后端）。两者都强制要求：
//   1. 片段非空、不含 "." / ".."、不含 "/"（层级分隔符仅由 Join 处理）；
//   2. 片段符合 dataset 字符集（[A-Za-z0-9_.:-]）；
//   3. 拼接结果以 "<root>/<a>/<b>/..." 形成相对路径字符串，无前缀 "/"；
//   4. base 不为空、不以 "/" 开头、不含 "@" / "#"。

// isSafeZFSFragment 校验单个 dataset 名片段合法。
func isSafeZFSFragment(fragment string) bool {
	if fragment == "" || fragment == "." || fragment == ".." {
		return false
	}
	if strings.ContainsAny(fragment, "/\\@#") {
		return false
	}
	for _, r := range fragment {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == ':' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}

// zfsSafeJoinUnder 在 base（zfs 池根 dataset 路径，相对 zfs 根）下拼接
// dataset 名片段，返回规范的 dataset 路径字符串。任一片段非法或 base 非法均报错。
func zfsSafeJoinUnder(base string, parts ...string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return "", fmt.Errorf("zfs pool root dataset is empty")
	}
	if strings.HasPrefix(base, "/") {
		return "", fmt.Errorf("zfs pool root must be relative to zfs root: %s", base)
	}
	if strings.ContainsAny(base, "@#") {
		return "", fmt.Errorf("zfs pool root must not contain @/#: %s", base)
	}
	for _, seg := range strings.Split(base, "/") {
		if !isSafeZFSFragment(seg) {
			return "", fmt.Errorf("zfs pool root segment %q is unsafe", seg)
		}
	}
	segments := []string{base}
	for _, part := range parts {
		if !isSafeZFSFragment(part) {
			return "", fmt.Errorf("refusing unsafe zfs dataset name %q", part)
		}
		segments = append(segments, part)
	}
	// 连续 "/" 已被 strings.Split 过滤（产生空段由 isSafeZFSFragment 拦截）。
	return strings.Join(segments, "/"), nil
}

// parseZFSListLine 解析 `zfs list -Hp` 的单行输出。
//
// 列顺序：name, used, avail, refer, quota, refquota, mountpoint ...（实际列数由
// `-o` 控制）。本项目固定查询 USED/AVAIL/QUOTA/REFQUOTA，因此调用方需传
// `-o name,used,avail,quota,refquota`，输出形如：
//
//	tank/eyvescloud/disk-data/vol-1    123456    987654321    1073741824    1073741824    /tank/eyvescloud/disk-data/vol-1
//
// 数值单位：bytes（zfs 默认）。本函数容忍 unknown/-\u2014（zfs 表示无限制）。
func parseZFSListLine(line string) (name string, used, avail, quota int64, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return "", 0, 0, 0, false
	}
	name = fields[0]
	used = parseZFSNumber(fields[1])
	avail = parseZFSNumber(fields[2])
	quota = parseZFSNumber(fields[4])
	return name, used, avail, quota, true
}

// parseZFSNumber 容忍 zfs 的 "none"/"-"(无限制) 与单位后缀 K/M/G/T（虽然
// `-Hp` 默认 bytes，但旧版本或别名可能带单位；保守处理）。
func parseZFSNumber(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" || strings.EqualFold(s, "none") {
		return 0
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "K") || strings.HasSuffix(s, "k"):
		mult = 1024
		s = strings.TrimSuffix(strings.TrimSuffix(s, "K"), "k")
	case strings.HasSuffix(s, "M") || strings.HasSuffix(s, "m"):
		mult = 1024 * 1024
		s = strings.TrimSuffix(strings.TrimSuffix(s, "M"), "m")
	case strings.HasSuffix(s, "G") || strings.HasSuffix(s, "g"):
		mult = 1024 * 1024 * 1024
		s = strings.TrimSuffix(strings.TrimSuffix(s, "G"), "g")
	case strings.HasSuffix(s, "T") || strings.HasSuffix(s, "t"):
		mult = 1024 * 1024 * 1024 * 1024
		s = strings.TrimSuffix(strings.TrimSuffix(s, "T"), "t")
	}
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0
	}
	return n * mult
}

// parseZFSListPoolLine 解析池根 dataset（POOLUSED 来自 `zfs list -Hp -o name,used,avail`）。
// 返回 (used, avail, ok)。POOLQUOTA 通常无意义（池根不设 quota），不解析。
func parseZFSListPoolLine(line string) (used, avail int64, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return 0, 0, false
	}
	return parseZFSNumber(fields[1]), parseZFSNumber(fields[2]), true
}
