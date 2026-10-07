package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---- app_meta 行级增量落库单测 ----

func metaSnapshot(t *testing.T) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT key, value FROM app_meta`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// metaChangedKeys 返回两次快照之间「内容变化 / 新增 / 消失」的键（有序）。
func metaChangedKeys(before, after map[string]string) []string {
	var keys []string
	for k, v := range after {
		if old, ok := before[k]; !ok || old != v {
			keys = append(keys, k)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// metaWrites 统计 app_meta 的实际写行数：新增键走 INSERT，改写已有键走 UPDATE
// （upsert 的 `ON CONFLICT DO UPDATE` 触发 UPDATE 而非 INSERT）。
func metaWrites(t *testing.T) int {
	t.Helper()
	return probeCount(t, "app_meta", "INSERT") + probeCount(t, "app_meta", "UPDATE")
}

func setupMetaTest(t *testing.T) string {
	t.Helper()
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	path := filepath.Join(dir, "config.json")
	SetConfigPath(path)
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestMetaEntriesCoverEveryPersistedKey 是 app_meta 的**产出清单守卫**：
// 一次保存之后，库里的键集合必须与 metaEntries 完全一致——既不能漏掉任何该落库
// 的键（漏掉=重启丢配置），也不能凭空多出键（多出=旧键没被清理）。
func TestMetaEntriesCoverEveryPersistedKey(t *testing.T) {
	setupMetaTest(t)
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}

	inDB := metaSnapshot(t)
	entries := metaEntries(AppConfig)
	want := make(map[string]bool, len(entries))
	dup := map[string]bool{}
	for i := range entries {
		k := entries[i].key
		if dup[k] {
			t.Errorf("metaEntries 中键 %q 重复", k)
		}
		dup[k] = true
		want[k] = true
	}
	for k := range want {
		if _, ok := inDB[k]; !ok {
			t.Errorf("键 %q 未落库（重启后会丢配置）", k)
		}
	}
	for k := range inDB {
		if !want[k] {
			t.Errorf("库中存在 metaEntries 未产出的键 %q（历史键应被清理）", k)
		}
	}
	t.Logf("app_meta 键数 = %d，与 metaEntries 完全一致", len(entries))
}

// TestMetaSaveWritesOnlyChangedKeys 是 app_meta 行级化的核心断言：
// 无变化 → 不写任何会改变内容的行；改一个键 → 只写那一个键；旧键 → 被删除。
//
// 断言口径与机器、时钟无关：把「实际写入行数」与「库内容真正变化的键数」对齐。
// 唯一可能自发变化的键是 updated_at（秒级时间戳），因此允许它出现在变化集里。
func TestMetaSaveWritesOnlyChangedKeys(t *testing.T) {
	setupMetaTest(t)
	AppConfig.BrandName = "初始品牌"
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "app_meta")

	// 1) 无变化保存：写入行数必须等于「内容真正变化的键数」（通常为 0，
	//    跨秒边界时最多 1 个 updated_at），且不得有删除。
	before := metaSnapshot(t)
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	after := metaSnapshot(t)
	changed := metaChangedKeys(before, after)
	if got := metaWrites(t); got != len(changed) {
		t.Fatalf("无变化保存写入 %d 行，但实际变化 %d 个键（%v）：行级化失效",
			got, len(changed), changed)
	}
	for _, k := range changed {
		if k != "updated_at" {
			t.Fatalf("无变化保存却改写了 %q（期望只有 updated_at 可能自变）", k)
		}
	}
	assertProbe(t, "app_meta", "DELETE", 0)

	// 2) 改一个键：只有它（以及可能的 updated_at）被写。
	writes0 := metaWrites(t)
	before = metaSnapshot(t)
	AppConfig.BrandName = "改名后的品牌"
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	after = metaSnapshot(t)
	changed = metaChangedKeys(before, after)
	inserts := metaWrites(t) - writes0
	if inserts != len(changed) {
		t.Fatalf("改 1 个键写入 %d 行，实际变化 %d 个键（%v）", inserts, len(changed), changed)
	}
	if inserts > 2 || inserts == 0 {
		t.Fatalf("改 1 个键应只写 1-2 行（brand_name ± updated_at），实为 %d", inserts)
	}
	found := false
	for _, k := range changed {
		if k == "brand_name" {
			found = true
		}
	}
	if !found {
		t.Fatalf("brand_name 变化未落库（变化集 %v）", changed)
	}
	if after["brand_name"] != "改名后的品牌" {
		t.Fatalf("库中 brand_name = %q", after["brand_name"])
	}

	// 3) 旧版本遗留键：加载时发现「库里有、本轮不再产出」的键，下一次保存必须把它
	//    清掉（与旧「整表重写」语义一致）。这里必须先重启再保存——清理的对象是
	//    加载期播种出来的孤儿键；运行期由外部直接写入的键不归落库层管。
	path := filepath.Join(filepath.Dir(configPath), "config.json")
	if _, err := db.Exec(`INSERT INTO app_meta(key, value) VALUES('legacy_orphan_key','x')`); err != nil {
		t.Fatal(err)
	}
	resetConfigStoreForTest(t)
	SetConfigPath(path)
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	if _, ok := metaSnapshot(t)["legacy_orphan_key"]; ok {
		t.Fatal("升级后历史遗留键未被清理")
	}
	// 清理完成后不再有可删的行（探针与触发器是 DB 对象，重启后依然计数）。
	deletes0 := probeCount(t, "app_meta", "DELETE")
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	if got := probeCount(t, "app_meta", "DELETE") - deletes0; got != 0 {
		t.Fatalf("清理完成后仍产生 %d 次删除", got)
	}
}

// TestMetaRestartRoundTripWritesNothing 断言重启后无变化保存不写任何 app_meta 行：
// 加载即播种（内存==库），所以 81 个键一个都不需要重写。这是本次改造的核心收益
// ——旧实现每次保存都整表重写（DELETE + 81 INSERT），无论有没有改动。
func TestMetaRestartRoundTripWritesNothing(t *testing.T) {
	path := setupMetaTest(t)
	AppConfig.BrandName = "重启前"
	AppConfig.TurnstileSiteKey = "site-key"
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}

	// 模拟重启：关闭连接 → 重新加载（播种）→ 无变化保存。
	resetConfigStoreForTest(t)
	SetConfigPath(path)
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "app_meta")
	before := metaSnapshot(t)
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	changed := metaChangedKeys(before, metaSnapshot(t))
	if got := metaWrites(t); got != len(changed) {
		t.Fatalf("重启后无变化保存写入 %d 行，实际变化 %d 个键（%v）",
			got, len(changed), changed)
	}
	for _, k := range changed {
		if k != "updated_at" {
			t.Fatalf("重启后无变化保存却改写了 %q", k)
		}
	}
	assertProbe(t, "app_meta", "DELETE", 0)
	t.Logf("重启后无变化保存：app_meta 写 %d 行（旧实现恒为 %d 行）", probeCount(t, "app_meta", "INSERT")+probeCount(t, "app_meta", "UPDATE"), len(metaEntries(AppConfig)))
}

// TestSeedMetaFingerprintsForcesRewriteOfLegacyPlaintext 是「透明重加密」的
// 直接守卫：播种时若发现库里存的是加密保护上线前的明文形态，必须给出
// metaForceRewrite，逼下一次保存重写——否则行级化的「未变就不写」会让明文无限期
// 滞留磁盘，等于静默关掉静态加密。
func TestSeedMetaFingerprintsForcesRewriteOfLegacyPlaintext(t *testing.T) {
	cfg := &EyvescloudConfig{JWTSecret: "plain-jwt", TurnstileSecretKey: "plain-ts", BrandName: "x"}
	dbMeta := map[string]string{
		"jwt_secret":           "plain-jwt",                 // 历史明文 → 必须重写
		"turnstile_secret_key": nodeTokenEncPrefix + "QUJD", // 已是密文 → 正常播种
		"nodes":                `[{"id":"n1","token":"plain-tok"}]`,
		"brand_name":           "x",
	}
	got := seedMetaFingerprints(cfg, dbMeta)
	if got["jwt_secret"] != metaForceRewrite {
		t.Fatalf("历史明文 jwt_secret 未被要求重写，播种值 %q", got["jwt_secret"])
	}
	// nodes 自 P2 起不再是 app_meta 键（改每节点一行），故历史键一律标记删除。
	if got["nodes"] != metaForceRewrite {
		t.Fatalf("历史 nodes 键未被标记删除，播种值 %q", got["nodes"])
	}
	if got["turnstile_secret_key"] == metaForceRewrite {
		t.Fatal("已是密文的键不该被要求重写（会造成每次重启的无效重写）")
	}
	if got["brand_name"] != fingerprint("x") {
		t.Fatal("普通键未按明文播种")
	}
	// 库里存在但不再产出的键：播种为哨兵 → 下一次保存删除。
	dbMeta["legacy_orphan_key"] = "y"
	if got := seedMetaFingerprints(cfg, dbMeta); got["legacy_orphan_key"] != metaForceRewrite {
		t.Fatalf("历史遗留键未被标记删除，播种值 %q", got["legacy_orphan_key"])
	}
}

// TestMetaLegacyPlaintextSecretIsReEncrypted 端到端确认：老库（jwt_secret 为明文）
// 升级后，磁盘上的值必须变成密文且可解密回原值；此后无变化保存不再重复加密。
func TestMetaLegacyPlaintextSecretIsReEncrypted(t *testing.T) {
	path := setupMetaTest(t)
	const plain = "plain-jwt-secret-value"
	AppConfig.JWTSecret = plain
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	if got := metaSnapshot(t)["jwt_secret"]; !strings.HasPrefix(got, nodeTokenEncPrefix) {
		t.Fatalf("正常落库应为密文，实为 %q", got)
	}

	// 模拟老库：把该键改回明文，然后重启（重新播种指纹）。
	if _, err := db.Exec(`UPDATE app_meta SET value = ? WHERE key = 'jwt_secret'`, plain); err != nil {
		t.Fatal(err)
	}
	resetConfigStoreForTest(t)
	SetConfigPath(path)
	cfg, err := InitConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JWTSecret != plain {
		t.Fatalf("存量明文应原样读回，得到 %q", cfg.JWTSecret)
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}

	stored := metaSnapshot(t)["jwt_secret"]
	if !strings.HasPrefix(stored, nodeTokenEncPrefix) {
		t.Fatalf("重写后仍非密文：%q", stored)
	}
	back, err := DecryptNodeToken(stored)
	if err != nil {
		t.Fatalf("重写后的密文无法解密：%v", err)
	}
	if back != plain {
		t.Fatalf("重写后解密值 = %q, want %q", back, plain)
	}

	// 已经是密文之后：再来一次无变化保存不应再写该键。
	before := metaSnapshot(t)
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	for _, k := range metaChangedKeys(before, metaSnapshot(t)) {
		if k == "jwt_secret" {
			t.Fatal("密文已就位后仍在重复加密落库")
		}
	}
}

// TestHeartbeatSaveWritesOneNodeRowAtScale 是 P2「nodes 单行 JSON → 每节点一行」的
// 验收断言：过去 30k 节点时单次心跳保存要整键重写 nodes（marshal 全部节点 + 逐节点
// AES-GCM 加密 Token/InstallKey + 提交 9.5MB 值 ≈220ms），现在只写心跳声明的那一行。
//
// 断言只依赖写量（与机器无关）：每轮 nodes 表恰好 DELETE+INSERT 各 1 行，且
// app_meta 里再也不会出现 nodes 键。耗时仅作证据。
func TestHeartbeatSaveWritesOneNodeRowAtScale(t *testing.T) {
	n := 30000
	if testing.Short() {
		n = 3000
	}
	if raw := os.Getenv("EYVES_NODES_N"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			t.Fatalf("invalid EYVES_NODES_N=%q", raw)
		}
		n = v
	}

	setupMetaTest(t)
	nodes := make([]Node, 0, n)
	for i := 0; i < n; i++ {
		nodes = append(nodes, Node{
			ID: fmt.Sprintf("node-%d", i), Name: fmt.Sprintf("node-%d", i),
			Address: fmt.Sprintf("https://10.%d.%d.%d:8999", (i/65025)%250+1, (i/255)%255, i%255+1),
			Token:   fmt.Sprintf("node-token-%048d", i),
			Status:  "online", LastSeen: "2026-01-01 00:00:00",
			Version: "v2.2.50", CPUCount: 8, RAMTotalMB: 16384,
		})
	}
	AppConfig.Nodes = nodes
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != n {
		t.Fatalf("nodes 表行数 = %d, want %d（每节点一行）", rowCount, n)
	}
	installWriteProbe(t, "nodes")
	installWriteProbe(t, "app_meta")
	base := probeRead(t)

	const rounds = 5
	var min time.Duration
	for i := 0; i < rounds; i++ {
		// 心跳最小的必然变更：LastSeen（秒级）；只声明这一个节点，与 api/nodes.go
		// 心跳处理器的精确脏集声明一致。
		AppConfig.Nodes[0].LastSeen = fmt.Sprintf("2026-01-01 00:00:%02d", i+1)
		declared := AppConfig.Nodes[0]
		t0 := time.Now()
		if err := saveConfigToDBHinted(newExactDirtySet(DirtySet{Nodes: []Node{declared}})); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(t0); d > 0 && (min == 0 || d < min) {
			min = d
		}
	}
	after := probeRead(t)

	nodeWrites := after["nodes_INSERT"] - base["nodes_INSERT"] +
		after["nodes_DELETE"] - base["nodes_DELETE"]
	// upsertNodeRow 是 DELETE + INSERT：每轮恰好 2 条语句，一律只碰 1 行。
	if want := 2 * rounds; nodeWrites != want {
		t.Fatalf("nodes 表写入语句 %d 条, want %d（每轮只写 1 行 = DELETE+INSERT）", nodeWrites, want)
	}
	metaWrites := after["app_meta_UPDATE"] - base["app_meta_UPDATE"] +
		after["app_meta_INSERT"] - base["app_meta_INSERT"]
	// 只剩 updated_at（秒级，同秒内不写）这个有界固定项。
	if metaWrites > rounds {
		t.Fatalf("app_meta 写入 %d 次, want ≤%d（nodes 键已退出 app_meta）", metaWrites, rounds)
	}
	if _, ok := metaSnapshot(t)["nodes"]; ok {
		t.Fatal("nodes 键仍在 app_meta 中（单行 JSON 未拆分干净）")
	}

	// 声明的那一行确实落库，且密文可解回明文（加密路径未被拆分破坏）。
	var lastSeen string
	if err := db.QueryRow(`SELECT last_seen FROM nodes WHERE id = ?`, "node-0").Scan(&lastSeen); err != nil {
		t.Fatal(err)
	}
	if want := AppConfig.Nodes[0].LastSeen; lastSeen != want {
		t.Fatalf("node-0 last_seen = %q, want %q", lastSeen, want)
	}
	var token string
	if err := db.QueryRow(`SELECT token FROM nodes WHERE id = ?`, "node-0").Scan(&token); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, nodeTokenEncPrefix) {
		t.Fatalf("node-0 token 落库非密文：%q", token)
	}
	if plain, err := DecryptNodeToken(token); err != nil || plain != AppConfig.Nodes[0].Token {
		t.Fatalf("node-0 token 解密失败或不等：%q / %v", plain, err)
	}
	// 未声明的节点既不被重写也不被删除。
	if err := db.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != n {
		t.Fatalf("精确保存后 nodes 行数 = %d, want %d（未声明的行不许被删）", rowCount, n)
	}

	t.Logf("节点 n=%d（取 %d 轮最小值）：心跳式保存 %v（过去整键重写 nodes 约 220ms）",
		n, rounds, min.Round(time.Microsecond))
}

// TestLegacyNodesRowMigratedToTable 覆盖升级路径：老库节点只在 app_meta[nodes] 的
// 单行 JSON 里。新版本启动时必须把它们搬进 nodes 表并删掉旧键，否则升级即丢节点。
func TestLegacyNodesRowMigratedToTable(t *testing.T) {
	path := setupMetaTest(t)
	legacy := `[{"id":"legacy-1","name":"旧节点","address":"https://1.2.3.4:8999","token":"plain-legacy-token","status":"online"},{"id":"legacy-2","name":"旧节点2","address":"https://5.6.7.8:8999","status":"offline"}]`
	if _, err := db.Exec(`INSERT INTO app_meta(key, value) VALUES ('nodes', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, legacy); err != nil {
		t.Fatal(err)
	}

	resetConfigStoreForTest(t)
	SetConfigPath(path)
	cfg, err := InitConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Nodes) != 2 {
		t.Fatalf("迁移后内存节点数 = %d, want 2", len(cfg.Nodes))
	}
	if cfg.Nodes[0].ID != "legacy-1" || cfg.Nodes[0].Name != "旧节点" {
		t.Fatalf("迁移后节点 0 = %+v", cfg.Nodes[0])
	}
	// 旧值里的明文 Token 在搬迁时被加密落库。
	var token string
	if err := db.QueryRow(`SELECT token FROM nodes WHERE id = 'legacy-1'`).Scan(&token); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, nodeTokenEncPrefix) {
		t.Fatalf("搬迁后 token 非密文：%q", token)
	}
	if plain, err := DecryptNodeToken(token); err != nil || plain != "plain-legacy-token" {
		t.Fatalf("搬迁后 token 解密 = %q / %v", plain, err)
	}
	// 旧键必须在搬迁的同一事务里被删除，不依赖后续保存的清理时机。
	var keyCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM app_meta WHERE key = 'nodes'`).Scan(&keyCount); err != nil {
		t.Fatal(err)
	}
	if keyCount != 0 {
		t.Fatal("迁移完成后 app_meta[nodes] 旧键仍存在")
	}
	// 迁移后内存==库，故紧接的保存不该产生任何 nodes 写入。
	installWriteProbe(t, "nodes")
	base := probeRead(t)
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	after := probeRead(t)
	if got := after["nodes_INSERT"] - base["nodes_INSERT"] +
		after["nodes_DELETE"] - base["nodes_DELETE"]; got != 0 {
		t.Fatalf("迁移后无变化保存写了 nodes %d 条语句, want 0（重启不该触发全量重写）", got)
	}
}
