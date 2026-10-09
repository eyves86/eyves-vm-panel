package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// ---- P1 行级增量落库单测 ----

// TestFingerprintDeterministic 验证指纹对同一值稳定、对不同值敏感，
// 且不受 map 迭代顺序影响（否则每次保存都会误判为「变了」）。
func TestFingerprintDeterministic(t *testing.T) {
	type payload struct {
		A string            `json:"a"`
		B int               `json:"b"`
		M map[string]string `json:"m"`
	}
	p1 := payload{A: "x", B: 1, M: map[string]string{"k1": "v1", "k2": "v2", "k3": "v3"}}
	p2 := payload{A: "x", B: 1, M: map[string]string{"k3": "v3", "k1": "v1", "k2": "v2"}}
	if fingerprint(p1) != fingerprint(p2) {
		t.Fatalf("map 键顺序不应影响指纹: %s vs %s", fingerprint(p1), fingerprint(p2))
	}
	p3 := p1
	p3.B = 2
	if fingerprint(p1) == fingerprint(p3) {
		t.Fatal("字段变化必须改变指纹")
	}
}

// TestPersistedStructsFullyJSONTagged 是增量落库的**前提守卫**：指纹取自 JSON，
// 若某个需要落库的字段没有 json tag，它的变化就不会被检出 → 漏写（数据丢失）。
// 新增字段必须带 json tag。
func TestPersistedStructsFullyJSONTagged(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(Container{}),
		reflect.TypeOf(SubUser{}),
		reflect.TypeOf(ApiKeyConfig{}),
		reflect.TypeOf(Snapshot{}),
		reflect.TypeOf(SavedTask{}),
		reflect.TypeOf(Node{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.PkgPath != "" { // 非导出字段不参与 JSON
				continue
			}
			if _, ok := f.Tag.Lookup("json"); !ok {
				t.Errorf("%s.%s 缺少 json tag：增量落库无法感知该字段变化", typ.Name(), f.Name)
			}
		}
	}
}

// TestNodeRowRoundTripsEveryField 是 nodes 表**列覆盖**的守卫：Node 每个字段都必须
// 有对应的表列，否则该字段落库即丢（重启后变零值）。做法是整字段填充 → 落库 → 读回
// → reflect.DeepEqual，列缺失、列序错位、类型截断都会在这里暴露。
// Token / InstallKey 落库走 AES-GCM，读回解密后应等于明文，故一并参与比较。
func TestNodeRowRoundTripsEveryField(t *testing.T) {
	setupMetaTest(t)
	want := Node{
		ID: "node-rt", Name: "往返节点", Address: "https://10.1.2.3:8999",
		PublicHost: "node.example.com", Token: "tok-plain-往返",
		InstallKey: "key-plain", InstallKeyCreatedAt: "2026-10-06T01:02:03Z",
		InstallKeyIP: "10.1.2.3", Status: "online", LastSeen: "2026-10-06 01:02:03",
		Version: "v2.2.50", OSName: "Ubuntu 22.04", CPUCount: 16,
		RAMTotalMB: 32768, RAMUsedMB: 12345, DiskTotalGB: 1023.5, DiskUsedGB: 456.25,
		ContainerCount: 42, RegionID: "region-1", NodeGroupID: "group-1", ClusterID: "cluster-1",
		CellID:    "cell-1",
		VirtTypes: []string{"lxc", "kvm"}, CreatedAt: "2026-10-06 01:00:00",
		MaintenanceMode: true, MaintenanceSince: "2026-10-06 01:01:00",
		TLSSkipVerify: true, AllowPrivateAddr: true,
	}
	AppConfig.Nodes = []Node{want}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	got, err := loadNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("读回节点数 = %d, want 1", len(got))
	}
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("节点往返后不一致：\n落库前 %+v\n读回后 %+v", want, got[0])
	}
}

// TestFingerprintCoversEveryField 是编码计划的**完备性守卫**：逐个叶子字段改值，
// 断言指纹必然改变；恢复后再断言指纹还原。
//
// 为什么需要它：指纹是「是否要写这一行」的唯一依据。若某个字段变化不影响指纹，
// 该字段的修改就会被静默丢弃（漏写）。本用例与编码计划同源于 reflect 元数据，
// 但走的是独立路径（逐字段改值），因此能抓住「字段进了结构体却没进编码」的漂移。
func TestFingerprintCoversEveryField(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(Container{}),
		reflect.TypeOf(SubUser{}),
		reflect.TypeOf(ApiKeyConfig{}),
		reflect.TypeOf(Snapshot{}),
		reflect.TypeOf(SavedTask{}),
	} {
		root := reflect.New(typ).Elem()
		populateFingerprintSample(root)
		base := fingerprint(root.Interface())

		checked := 0
		fpWalkLeaves(root, typ.Name(), func(path string, leaf reflect.Value, sync func()) {
			restore := fpMutateLeaf(t, leaf)
			sync()
			got := fingerprint(root.Interface())
			restore()
			sync()

			if got == base {
				t.Errorf("%s 改值后指纹不变：该字段未进入编码计划（改动会被漏写）", path)
			}
			if back := fingerprint(root.Interface()); back != base {
				t.Errorf("%s 恢复后指纹未还原：base=%s back=%s", path, base, back)
			}
			checked++
		}, func() {})

		if checked == 0 {
			t.Fatalf("%s 未校验到任何叶子字段", typ.Name())
		}
		t.Logf("%s: 已校验 %d 个叶子字段的指纹覆盖", typ.Name(), checked)
	}
}

// populateFingerprintSample 预填充样本，保证每个切片/映射至少有一个元素、指针非空，
// 否则空容器改值也无法体现（无元素可改）。
func populateFingerprintSample(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath != "" {
				continue
			}
			populateFingerprintSample(v.Field(i))
		}
	case reflect.Ptr:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		populateFingerprintSample(v.Elem())
	case reflect.Slice:
		if v.Len() == 0 {
			v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		}
		for i := 0; i < v.Len(); i++ {
			populateFingerprintSample(v.Index(i))
		}
	case reflect.Map:
		if v.Len() == 0 {
			v.Set(reflect.MakeMap(v.Type()))
			key := reflect.New(v.Type().Key()).Elem()
			if v.Type().Key().Kind() == reflect.String {
				key.SetString("__fp_base__")
			}
			v.SetMapIndex(key, reflect.Zero(v.Type().Elem()))
		}
	}
}

// fpWalkLeaves 深度遍历，对每个可改动的叶子（含切片长度、映射键集合、指针空值、
// 映射值）回调 cb。sync 把「映射值代理」写回所属映射；其余情况为 no-op。
func fpWalkLeaves(v reflect.Value, path string, cb func(path string, leaf reflect.Value, sync func()), sync func()) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.PkgPath != "" { // 非导出字段：编码退化为 %v，不单独校验
				continue
			}
			fpWalkLeaves(v.Field(i), path+"."+f.Name, cb, sync)
		}
	case reflect.Ptr:
		if v.IsNil() {
			return
		}
		fpWalkLeaves(v.Elem(), path+"*", cb, sync)
	case reflect.Slice:
		cb(path, v, sync) // 长度变化
		for i := 0; i < v.Len(); i++ {
			fpWalkLeaves(v.Index(i), fmt.Sprintf("%s[%d]", path, i), cb, sync)
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			fpWalkLeaves(v.Index(i), fmt.Sprintf("%s[%d]", path, i), cb, sync)
		}
	case reflect.Map:
		cb(path, v, sync) // 键集合变化
		for _, k := range v.MapKeys() {
			ev := v.MapIndex(k)
			proxy := reflect.New(ev.Type()).Elem()
			proxy.Set(ev)
			key := k
			innerSync := func() { v.SetMapIndex(key, proxy) }
			fpWalkLeaves(proxy, fmt.Sprintf("%s[%v]", path, key.Interface()), cb, innerSync)
		}
	default:
		cb(path, v, sync)
	}
}

// fpMutateLeaf 把叶子改成一个不同的值并返回还原函数。必须保证「不同」是必然的
// （长度前缀 / 位模式差异），否则用例会产生假阳性。
func fpMutateLeaf(t *testing.T, v reflect.Value) func() {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		old := v.String()
		v.SetString(old + "\x00fp")
		return func() { v.SetString(old) }
	case reflect.Bool:
		old := v.Bool()
		v.SetBool(!old)
		return func() { v.SetBool(old) }
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		old := v.Int()
		v.SetInt(old + 1)
		return func() { v.SetInt(old) }
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		old := v.Uint()
		v.SetUint(old + 1)
		return func() { v.SetUint(old) }
	case reflect.Float32, reflect.Float64:
		old := v.Float()
		v.SetFloat(old + 1)
		return func() { v.SetFloat(old) }
	case reflect.Slice:
		old := v.Interface()
		v.Set(reflect.Append(v, reflect.Zero(v.Type().Elem())))
		return func() { v.Set(reflect.ValueOf(old)) }
	case reflect.Map:
		if v.Len() > 0 {
			key := v.MapKeys()[0]
			old := v.MapIndex(key)
			ev := reflect.New(v.Type().Elem()).Elem()
			ev.Set(old)
			mutRestore := fpMutateLeaf(t, ev)
			v.SetMapIndex(key, ev)
			return func() { mutRestore(); v.SetMapIndex(key, old) }
		}
		key := reflect.New(v.Type().Key()).Elem()
		if v.Type().Key().Kind() == reflect.String {
			key.SetString("__fp_probe__")
		}
		v.SetMapIndex(key, reflect.Zero(v.Type().Elem()))
		return func() { v.SetMapIndex(key, reflect.Value{}) }
	case reflect.Ptr:
		old := v.Interface()
		v.Set(reflect.Zero(v.Type()))
		return func() { v.Set(reflect.ValueOf(old)) }
	default:
		t.Fatalf("未支持的叶子类型 %s（Kind=%v）：编码计划需要显式处理", v.Type(), v.Kind())
		return func() {}
	}
}

// installWriteProbe 统计特定表的 INSERT/UPDATE/DELETE 次数，用于断言「只写变化的行」。
// Postgres 的触发器必须挂在一个 plpgsql 函数上（不像 SQLite 可内联 BEGIN..END）。
func installWriteProbe(t *testing.T, tables ...string) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS _write_probe (name TEXT PRIMARY KEY, n INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE OR REPLACE FUNCTION _probe_bump() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN UPDATE _write_probe SET n = n + 1 WHERE name = TG_ARGV[0]; RETURN NULL; END $$`); err != nil {
		t.Fatal(err)
	}
	// UPDATE 也要计：app_meta 的 upsert 走 `INSERT .. ON CONFLICT DO UPDATE`，
	// 改写已存在的键触发的是 UPDATE 而非 INSERT。
	for _, table := range tables {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			name := table + "_" + op
			if _, err := db.Exec(`INSERT INTO _write_probe(name, n) VALUES (?, 0) ON CONFLICT(name) DO NOTHING`, name); err != nil {
				t.Fatal(err)
			}
			// PG13 不支持 CREATE OR REPLACE TRIGGER（PG14 才有），统一先 DROP IF EXISTS 再建，
			// 兼容老发行版自带的 PostgreSQL。
			if _, err := db.Exec("DROP TRIGGER IF EXISTS probe_" + name + " ON " + table); err != nil {
				t.Fatal(err)
			}
			stmt := "CREATE TRIGGER probe_" + name +
				" AFTER " + op + " ON " + table +
				" FOR EACH ROW EXECUTE FUNCTION _probe_bump('" + name + "')"
			if _, err := db.Exec(stmt); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// installFailTrigger 在 table 的 op（INSERT/UPDATE/DELETE）前挂一个「写入即报错」的触发器，
// 用于向落库路径注入失败。Postgres 的触发器要挂在 plpgsql 函数上；PG13 无
// CREATE OR REPLACE TRIGGER，故先 DROP IF EXISTS 再建。
func installFailTrigger(t *testing.T, name, table, op string) {
	t.Helper()
	fn := name + "_fn"
	if _, err := db.Exec("CREATE OR REPLACE FUNCTION " + fn + "() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'inject'; END $$"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TRIGGER IF EXISTS " + name + " ON " + table); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TRIGGER " + name + " BEFORE " + op + " ON " + table + " FOR EACH ROW EXECUTE FUNCTION " + fn + "()"); err != nil {
		t.Fatal(err)
	}
}

func dropFailTrigger(t *testing.T, name, table string) {
	t.Helper()
	if _, err := db.Exec("DROP TRIGGER IF EXISTS " + name + " ON " + table); err != nil {
		t.Fatal(err)
	}
}

func probeCount(t *testing.T, table, op string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT n FROM _write_probe WHERE name = ?`, table+"_"+op).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func assertProbe(t *testing.T, table, op string, want int) {
	t.Helper()
	if got := probeCount(t, table, op); got != want {
		t.Fatalf("%s.%s 写入次数 = %d, want %d", table, op, got, want)
	}
}

// TestIncrementalSaveWritesOnlyChangedRows 是行级落库的核心断言：
// 无变化 → 零行写；改一行 → 只写该行；增/删 → 只写增删的那行。
func TestIncrementalSaveWritesOnlyChangedRows(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	// 顺序有讲究：t.TempDir 的清理在 LIFO 下最后注册、最先执行。必须让
	// resetConfigStoreForTest（关连接）先跑，否则 Windows 上无法删除仍被
	// 占用的 config.db，TempDir 清理会报 unlinkat 失败。
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	cfg, err := InitConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("InitConfig returned nil config")
	}

	cfg.Containers = []Container{
		{ID: 1, UUID: "uuid-1", Name: "ct-1", Virtualization: VirtualizationLXC, Status: "running"},
		{ID: 2, UUID: "uuid-2", Name: "ct-2", Virtualization: VirtualizationLXC, Status: "running"},
		{ID: 3, UUID: "uuid-3", Name: "ct-3", Virtualization: VirtualizationLXC, Status: "running"},
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "containers", "sub_users")

	// 1) 无任何改动：不得产生行写（这是「路径无关的写放大」是否消灭的直接证据）。
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	assertProbe(t, "containers", "INSERT", 0)
	assertProbe(t, "containers", "DELETE", 0)

	// 2) 只改一个容器的状态：只有该行被删+插，另外两行不动。
	AppConfig.Containers[1].Status = "stopped"
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	assertProbe(t, "containers", "INSERT", 1)
	assertProbe(t, "containers", "DELETE", 1)

	// 3) 新增一个容器：只插 1 行。
	AppConfig.Containers = append(AppConfig.Containers, Container{
		ID: 4, UUID: "uuid-4", Name: "ct-4", Virtualization: VirtualizationLXC, Status: "running",
	})
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	assertProbe(t, "containers", "INSERT", 2)
	assertProbe(t, "containers", "DELETE", 1)

	// 4) 删除两个容器（截断到前两个）：只删这两行，保留的两行不再重写。
	AppConfig.Containers = AppConfig.Containers[:2]
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	assertProbe(t, "containers", "INSERT", 2)
	assertProbe(t, "containers", "DELETE", 3) // 2) 改写的 ct-2 + 4) 删除的 ct-3 / ct-4

	// 5) 子用户集合无变化时同样零写。
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	assertProbe(t, "sub_users", "INSERT", 0)
	assertProbe(t, "sub_users", "DELETE", 0)
}

// TestIncrementalSaveDeletionRemovesChildren 验证删除容器时其位置序列表子行一并清理，
// 不留孤儿行（旧实现在整表 DELETE 下天然没这个问题，行级改造必须显式处理）。
func TestIncrementalSaveDeletionRemovesChildren(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	AppConfig.Containers = []Container{{
		ID:     7,
		UUID:   "uuid-7",
		Name:   "ct-7",
		Status: "running",
		PortMappings: []PortMapping{
			{ContainerPort: 80, HostPort: 8080, Protocol: "tcp"},
			{ContainerPort: 443, HostPort: 8443, Protocol: "tcp"},
		},
		PublicIPv4s: []PublicIPv4Assignment{{Address: "203.0.113.7"}},
	}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	countRows := func(table string) int {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := countRows("port_mappings"); got != 2 {
		t.Fatalf("port_mappings rows = %d, want 2", got)
	}
	if got := countRows("container_public_ipv4s"); got != 1 {
		t.Fatalf("container_public_ipv4s rows = %d, want 1", got)
	}

	AppConfig.Containers = nil
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"containers", "port_mappings", "container_public_ipv4s", "container_ipv6_addresses"} {
		if got := countRows(table); got != 0 {
			t.Fatalf("%s rows = %d after container delete, want 0", table, got)
		}
	}
}

// TestReseedAfterRestartAvoidsRewrite 验证「重启后第一次保存不产生无效重写」：
// loadConfigFromDB 用加载到的快照播种行指纹，内存==库，故无改动时零行写。
func TestReseedAfterRestartAvoidsRewrite(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	path := filepath.Join(dir, "config.json")
	SetConfigPath(path)
	cfg, err := InitConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Containers = []Container{
		{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"},
		{ID: 2, UUID: "uuid-2", Name: "ct-2", Status: "stopped"},
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}

	// 模拟重启：关连接、重开、重放加载（播种指纹）。
	resetConfigStoreForTest(t)
	SetConfigPath(path)
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "containers")
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	assertProbe(t, "containers", "INSERT", 0)
	assertProbe(t, "containers", "DELETE", 0)

	// 反证：真的改了，仍然会被写下去（增量不等于不写）。
	AppConfig.Containers[0].Name = "renamed"
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	assertProbe(t, "containers", "INSERT", 1)
	assertProbe(t, "containers", "DELETE", 1)
}

// probeRead 读取全部写探针计数。
func probeRead(t *testing.T) map[string]int {
	t.Helper()
	rows, err := db.Query(`SELECT name, n FROM _write_probe`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			t.Fatal(err)
		}
		out[name] = n
	}
	return out
}

// TestSaveAmplificationAtScale 量化行级增量落库相对整库重写的写量差异。
// N 由 EYVES_SCALE_N 控制（默认 2000；-short 时 200）。
//
// 判定标准（不依赖机器性能）：
//   - 未改动保存 → containers 行写 0 次；
//   - 改 1 个容器 → containers 行写恰好 2 次（1 删 + 1 插），与 N 无关。
//
// 这正是设计不变量「单次操作只写 O(受影响行)」在规模化下的直接证据：
// 旧实现每次保存写 N 行，新实现恒为 2 行。
func TestSaveAmplificationAtScale(t *testing.T) {
	n := 2000
	if testing.Short() {
		n = 200
	}
	if raw := os.Getenv("EYVES_SCALE_N"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			t.Fatalf("invalid EYVES_SCALE_N=%q", raw)
		}
		n = v
	}

	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}

	// 构造 N 个容器，每个带端口映射与公网地址，模拟真实子表规模。
	containers := make([]Container, 0, n)
	for i := 0; i < n; i++ {
		containers = append(containers, Container{
			ID:             i + 1,
			UUID:           fmt.Sprintf("uuid-%d", i+1),
			Name:           fmt.Sprintf("ct-%d", i+1),
			Virtualization: VirtualizationLXC,
			LXCName:        fmt.Sprintf("ct-%d", i+1),
			Status:         "running",
			VCPU:           2,
			RAMMB:          2048,
			DiskGB:         20,
			IP:             fmt.Sprintf("10.0.0.%d", (i%250)+1),
			PortMappings: []PortMapping{
				{ContainerPort: 22, HostPort: 20000 + i, Protocol: "tcp"},
				{ContainerPort: 80, HostPort: 30000 + i, Protocol: "tcp"},
			},
			PublicIPv4s: []PublicIPv4Assignment{{Address: fmt.Sprintf("203.0.113.%d", (i%250)+1)}},
		})
	}
	AppConfig.Containers = containers

	t0 := time.Now()
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	fullSave := time.Since(t0)

	installWriteProbe(t, "containers")
	base := probeRead(t)

	// 1) 未改动的保存：零行写。
	t1 := time.Now()
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	noopSave := time.Since(t1)
	afterNoop := probeRead(t)
	if got := afterNoop["containers_INSERT"] - base["containers_INSERT"]; got != 0 {
		t.Fatalf("未改动保存产生了 %d 次 containers INSERT，want 0", got)
	}
	if got := afterNoop["containers_DELETE"] - base["containers_DELETE"]; got != 0 {
		t.Fatalf("未改动保存产生了 %d 次 containers DELETE，want 0", got)
	}

	// 2) 改 1 个容器：恰好 1 删 + 1 插，与 N 无关。
	AppConfig.Containers[n/2].Status = "stopped"
	t2 := time.Now()
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	oneChangeSave := time.Since(t2)
	afterOne := probeRead(t)
	if got := afterOne["containers_INSERT"] - afterNoop["containers_INSERT"]; got != 1 {
		t.Fatalf("改 1 个容器产生了 %d 次 containers INSERT，want 1", got)
	}
	if got := afterOne["containers_DELETE"] - afterNoop["containers_DELETE"]; got != 1 {
		t.Fatalf("改 1 个容器产生了 %d 次 containers DELETE，want 1", got)
	}

	t.Logf("N=%d 全量首写=%v 无改动保存=%v 改1个容器保存=%v（行写 2 次，与 N 无关）",
		n, fullSave.Round(time.Millisecond), noopSave.Round(time.Millisecond), oneChangeSave.Round(time.Millisecond))
}

// TestHintedSaveWritesOnlyDeclaredRows 验证声明式落库：只对声明过的容器重算指纹、
// 只写该行；未声明但实际改动的行会被跳过 —— 随后一次**全量保存**必须把它补上，
// 这正是「未改造路径仍安全」的兜底证据。
func TestHintedSaveWritesOnlyDeclaredRows(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	AppConfig.Containers = []Container{
		{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"},
		{ID: 2, UUID: "uuid-2", Name: "ct-2", Status: "running"},
		{ID: 3, UUID: "uuid-3", Name: "ct-3", Status: "running"},
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "containers")
	base := probeRead(t)

	// 声明容器 2 改动：恰好 1 删 + 1 插。
	AppConfig.Containers[1].Status = "stopped"
	if err := saveConfigToDBHinted(newDirtyHintContainers([]int{2})); err != nil {
		t.Fatal(err)
	}
	after := probeRead(t)
	if got := after["containers_INSERT"] - base["containers_INSERT"]; got != 1 {
		t.Fatalf("声明式保存 INSERT = %d, want 1", got)
	}
	if got := after["containers_DELETE"] - base["containers_DELETE"]; got != 1 {
		t.Fatalf("声明式保存 DELETE = %d, want 1", got)
	}

	// 未声明的改动：声明式保存会跳过（契约：声明集合必须覆盖实际改动）。
	AppConfig.Containers[2].Name = "renamed-3"
	if err := saveConfigToDBHinted(newDirtyHintContainers(nil)); err != nil {
		t.Fatal(err)
	}
	skipped := probeRead(t)
	if got := skipped["containers_INSERT"] - after["containers_INSERT"]; got != 0 {
		t.Fatalf("空声明保存却写了 %d 行，want 0（应全部沿用旧指纹）", got)
	}

	// 全量保存（nil 声明）必须补上被跳过的那一行 —— 安全兜底。
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	recovered := probeRead(t)
	if got := recovered["containers_INSERT"] - skipped["containers_INSERT"]; got != 1 {
		t.Fatalf("全量保存只补了 %d 行，want 1（未声明的改动必须被全量扫描捞回）", got)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM containers WHERE id = 3`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "renamed-3" {
		t.Fatalf("容器 3 名 = %q, want renamed-3", name)
	}
}

// TestHintedSaveWritesNewContainer 验证空声明下，主控新增（库里没有）的容器仍会
// 被写入 —— 跳过逻辑只作用于「上次已落库」的行，新行没有旧指纹可沿用。
func TestHintedSaveWritesNewContainer(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	AppConfig.Containers = []Container{{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "containers")
	base := probeRead(t)

	AppConfig.Containers = append(AppConfig.Containers, Container{
		ID: 2, UUID: "uuid-2", Name: "ct-2", Status: "running",
	})
	if err := saveConfigToDBHinted(newDirtyHintContainers(nil)); err != nil {
		t.Fatal(err)
	}
	after := probeRead(t)
	if got := after["containers_INSERT"] - base["containers_INSERT"]; got != 1 {
		t.Fatalf("空声明下新增容器写入 = %d, want 1", got)
	}
}

// TestHintedSaveCostAtScale 对比「全量保存」与「声明式保存」在 N 个容器下的墙钟
// 成本，量化 O(全部容器) → O(声明容器) 的收益。N 由 EYVES_SCALE_N 控制。
// 断言只依赖写量（与机器性能无关）；时间仅作日志证据。
func TestHintedSaveCostAtScale(t *testing.T) {
	n := 2000
	if testing.Short() {
		n = 200
	}
	if raw := os.Getenv("EYVES_SCALE_N"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			t.Fatalf("invalid EYVES_SCALE_N=%q", raw)
		}
		n = v
	}

	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	containers := make([]Container, 0, n)
	for i := 0; i < n; i++ {
		containers = append(containers, Container{
			ID: i + 1, UUID: fmt.Sprintf("uuid-%d", i+1), Name: fmt.Sprintf("ct-%d", i+1),
			Virtualization: VirtualizationLXC, LXCName: fmt.Sprintf("ct-%d", i+1),
			Status: "running", VCPU: 2, RAMMB: 2048, DiskGB: 20,
		})
	}
	AppConfig.Containers = containers
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "containers")
	base := probeRead(t)

	// 全量保存（nil 声明）：会对全部 N 个容器重算指纹。
	tFull := time.Now()
	if err := saveConfigToDBHinted(nil); err != nil {
		t.Fatal(err)
	}
	full := time.Since(tFull)

	// 声明式保存（只声明 1 个容器，且该容器未改动）。
	AppConfig.Containers[0].Status = "stopped"
	tHint := time.Now()
	if err := saveConfigToDBHinted(newDirtyHintContainers([]int{1})); err != nil {
		t.Fatal(err)
	}
	hint := time.Since(tHint)

	after := probeRead(t)
	if got := after["containers_INSERT"] - base["containers_INSERT"]; got != 1 {
		t.Fatalf("两次保存共写 %d 行，want 1（全量 0 + 声明 1）", got)
	}
	hintUS := hint.Microseconds()
	if hintUS < 1 {
		hintUS = 1
	}
	t.Logf("N=%d 全量保存=%v（扫描全部 %d 行） 声明式保存=%v（只扫描 1 行），提速 %dx",
		n, full.Round(time.Microsecond), n, hint.Round(time.Microsecond),
		full.Microseconds()/hintUS)
}

// ---- 精确模式（exact）落库单测 ----
//
// 精确模式是心跳路径的落库契约：调用方直接给出「变动的容器值」与「被删除的容器」，
// 未列出即视为未变。它的收益是簿记 O(声明行)（不再全量建 seen 集合 / 扫旧指纹表找删除），
// 代价是**漏声明会静默漏写**——所以必须有测试把契约的两面都钉死：
//   - 声明了的改动一定落库（含只改遥测的阈值节流场景）；
//   - 未声明的不被误写，也不被误删。

// TestExactSaveWritesOnlyDeclaredContainers 验证精确模式只写被声明的行，
// 且未声明的行既不被重写也不被删除（精确模式不做全量删除扫描）。
func TestExactSaveWritesOnlyDeclaredContainers(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	AppConfig.Containers = []Container{
		{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"},
		{ID: 2, UUID: "uuid-2", Name: "ct-2", Status: "running"},
		{ID: 3, UUID: "uuid-3", Name: "ct-3", Status: "running"},
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "containers")
	base := probeRead(t)

	// 只声明容器 2 改动（心跳：只声明本节点上报的容器）。
	AppConfig.Containers[1].Status = "stopped"
	if err := saveConfigToDBHinted(newExactDirtyHint([]Container{AppConfig.Containers[1]}, nil)); err != nil {
		t.Fatal(err)
	}
	after := probeRead(t)
	if got := after["containers_INSERT"] - base["containers_INSERT"]; got != 1 {
		t.Fatalf("精确保存 INSERT = %d, want 1（只写声明的 1 行）", got)
	}
	if got := after["containers_DELETE"] - base["containers_DELETE"]; got != 1 {
		t.Fatalf("精确保存 DELETE = %d, want 1", got)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM containers WHERE id = 2`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "stopped" {
		t.Fatalf("容器 2 状态 = %q, want stopped", status)
	}

	// 空声明：零行写，且未声明的行必须存活（精确模式绝不能顺手清表）。
	AppConfig.Containers[0].Name = "ct-1-dirty-in-memory"
	if err := saveConfigToDBHinted(newExactDirtyHint(nil, nil)); err != nil {
		t.Fatal(err)
	}
	empty := probeRead(t)
	if got := empty["containers_INSERT"] - after["containers_INSERT"]; got != 0 {
		t.Fatalf("空声明精确保存写了 %d 行，want 0", got)
	}
	if got := empty["containers_DELETE"] - after["containers_DELETE"]; got != 0 {
		t.Fatalf("空声明精确保存删了 %d 行，want 0", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM containers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("容器行数 = %d, want 3（未声明的行不允许被删）", n)
	}
}

// TestExactSavePersistsTelemetryOnlyChange 是心跳节流场景的直接回归：
// 无结构变化（只有流量遥测变动）时，被声明的容器仍必须落库 ——
// 否则 30w 容器下「内存新、库里旧」的遥测会永久不落盘。
func TestExactSavePersistsTelemetryOnlyChange(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	AppConfig.Containers = []Container{
		{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running", TrafficUsedRX: 100, TrafficUsedTX: 200},
		{ID: 2, UUID: "uuid-2", Name: "ct-2", Status: "running", TrafficUsedRX: 300, TrafficUsedTX: 400},
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "containers")
	base := probeRead(t)

	// 只刷新容器 1 的流量（心跳遥测），声明它。
	AppConfig.Containers[0].TrafficUsedRX = 999111
	AppConfig.Containers[0].TrafficUsedTX = 888222
	if err := saveConfigToDBHinted(newExactDirtyHint([]Container{AppConfig.Containers[0]}, nil)); err != nil {
		t.Fatal(err)
	}
	after := probeRead(t)
	if got := after["containers_INSERT"] - base["containers_INSERT"]; got != 1 {
		t.Fatalf("遥测落库 INSERT = %d, want 1", got)
	}
	var rx, tx int64
	if err := db.QueryRow(`SELECT traffic_used_rx, traffic_used_tx FROM containers WHERE id = 1`).Scan(&rx, &tx); err != nil {
		t.Fatal(err)
	}
	if rx != 999111 || tx != 888222 {
		t.Fatalf("遥测未落库：rx=%d tx=%d, want 999111/888222", rx, tx)
	}
	// 未声明的容器 2 遥测原样保留。
	if err := db.QueryRow(`SELECT traffic_used_rx FROM containers WHERE id = 2`).Scan(&rx); err != nil {
		t.Fatal(err)
	}
	if rx != 300 {
		t.Fatalf("未声明容器的遥测被改写：rx=%d, want 300", rx)
	}
}

// TestExactSaveRemovalDeletesContainerAndAccessLink 验证 removed 声明同时清理
// 容器行与其访问码链接行（精确模式不做全量删除扫描，孤儿行清理必须显式）。
func TestExactSaveRemovalDeletesContainerAndAccessLink(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	AppConfig.Containers = []Container{
		{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running", AccessCode: "code-1", AccessCodePassword: "pw-1"},
		{ID: 2, UUID: "uuid-2", Name: "ct-2", Status: "running"},
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	count := func(table string) int {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count("container_access_links"); n != 1 {
		t.Fatalf("container_access_links 行数 = %d, want 1", n)
	}

	gone := AppConfig.Containers[0]
	AppConfig.Containers = AppConfig.Containers[1:]
	if err := saveConfigToDBHinted(newExactDirtyHint(nil, []Container{gone})); err != nil {
		t.Fatal(err)
	}
	if n := count("containers"); n != 1 {
		t.Fatalf("删除后容器行数 = %d, want 1", n)
	}
	if n := count("container_access_links"); n != 0 {
		t.Fatalf("删除后访问码链接行数 = %d, want 0（必须随容器一并清理）", n)
	}
}

// TestExactSaveAccessLinkClearedDeletesLink 验证访问码被清空时链接行被删除，
// 而不是留一条空链接（访问码登录会命中该容器）。
func TestExactSaveAccessLinkClearedDeletesLink(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	AppConfig.Containers = []Container{
		{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running", AccessCode: "code-1", AccessCodePassword: "pw-1"},
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	AppConfig.Containers[0].AccessCode = ""
	AppConfig.Containers[0].AccessCodePassword = ""
	if err := saveConfigToDBHinted(newExactDirtyHint([]Container{AppConfig.Containers[0]}, nil)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM container_access_links WHERE container_uuid = 'uuid-1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("访问码清空后链接行数 = %d, want 0", n)
	}
}

// TestExactSaveRollsBackFingerprintsOnFailure 验证提交失败时精确模式对 persistedRows
// 的原地改动被回滚，并置位 forceFullScanNextSave —— 下一次保存会全量补齐，
// 保证失败的那次改动不会因为后续漏声明而永久丢失。
func TestExactSaveRollsBackFingerprintsOnFailure(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	t.Cleanup(func() { forceFullScanNextSave = false })
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	AppConfig.Containers = []Container{{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	originalFP := persistedRows.containers[1]
	if originalFP == "" {
		t.Fatal("播种后持久化指纹为空")
	}

	// 注入失败：sub_users 插入报错。落库步骤顺序为 containers → accessLinks →
	// subUsers → ...，故容器行已「写入」后失败，正是跨步骤回滚的用例。
	installFailTrigger(t, "fail_sub_user_insert", "sub_users", "INSERT")
	t.Cleanup(func() { dropFailTrigger(t, "fail_sub_user_insert", "sub_users") })

	AppConfig.Containers[0].Name = "ct-1-renamed"
	AppConfig.SubUsers = []SubUser{{ID: "su-1", Username: "alice", PassHash: "h", Role: "operator", CreatedAt: "2026-01-01"}}
	err := saveConfigToDBHinted(newExactDirtyHint([]Container{AppConfig.Containers[0]}, nil))
	if err == nil {
		t.Fatal("注入的触发器未让保存失败：用例前提不成立")
	}
	if !forceFullScanNextSave {
		t.Fatal("保存失败后未置位 forceFullScanNextSave")
	}
	if got := persistedRows.containers[1]; got != originalFP {
		t.Fatalf("失败后指纹未回滚：got %s, want %s", got, originalFP)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM containers WHERE id = 1`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "ct-1" {
		t.Fatalf("失败事务未回滚：库中 name = %q, want ct-1", name)
	}

	// 恢复路径：移除注入后，下一次保存必须把「失败的那次改动」补上。
	dropFailTrigger(t, "fail_sub_user_insert", "sub_users")
	if err := saveConfigToDBHinted(newExactDirtyHint(nil, nil)); err != nil {
		t.Fatal(err)
	}
	if forceFullScanNextSave {
		t.Fatal("恢复保存后 forceFullScanNextSave 应为 false")
	}
	if err := db.QueryRow(`SELECT name FROM containers WHERE id = 1`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "ct-1-renamed" {
		t.Fatalf("恢复保存未补上改动：库中 name = %q, want ct-1-renamed", name)
	}
}

// TestExactSaveCostAtScale 在 N 个容器下对比三种保存的墙钟成本与写量：
// 全量扫描（nil）→ ID 集合声明 → 精确声明。断言只依赖写量（与机器性能无关），
// 时间仅作日志证据。这是「心跳落库从 O(全部容器) 降到 O(声明容器)」的量化依据。
func TestExactSaveCostAtScale(t *testing.T) {
	n := 20000
	if testing.Short() {
		n = 2000
	}
	if raw := os.Getenv("EYVES_SCALE_N"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			t.Fatalf("invalid EYVES_SCALE_N=%q", raw)
		}
		n = v
	}

	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	containers := make([]Container, 0, n)
	for i := 0; i < n; i++ {
		containers = append(containers, Container{
			ID: i + 1, UUID: fmt.Sprintf("uuid-%d", i+1), Name: fmt.Sprintf("ct-%d", i+1),
			Virtualization: VirtualizationLXC, LXCName: fmt.Sprintf("ct-%d", i+1),
			Status: "running", VCPU: 2, RAMMB: 2048, DiskGB: 20,
			IP: fmt.Sprintf("10.0.0.%d", (i%250)+1),
		})
	}
	AppConfig.Containers = containers
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "containers")
	base := probeRead(t)

	// 统计口径：非零样本的最小值。取最小值是因为后续阶段（全量/ID集合）会制造大量
	// 短命对象，GC 暂停会污染中位数；最小值对暂停不敏感。丢弃 0 是因为 Windows 单调
	// 时钟对亚毫秒区间会返回 0（测量失败而非「瞬时」）。
	const rounds = 9
	best := func(fn func()) time.Duration {
		var min time.Duration
		for i := 0; i < rounds; i++ {
			t0 := time.Now()
			fn()
			if d := time.Since(t0); d > 0 && (min == 0 || d < min) {
				min = d
			}
		}
		return min
	}
	toggle := func(id int) {
		if AppConfig.Containers[id].Status == "running" {
			AppConfig.Containers[id].Status = "stopped"
		} else {
			AppConfig.Containers[id].Status = "running"
		}
	}

	// 1) 全量扫描：每轮都对全部 N 个容器重算指纹。
	full := best(func() {
		toggle(n / 2)
		if err := saveConfigToDBHinted(nil); err != nil {
			t.Fatal(err)
		}
	})
	afterFull := probeRead(t)

	// 2) ID 集合声明：簿记仍 O(N)（建 seen 集合 + 遍历旧指纹表找删除），只重算声明的 1 行。
	idset := best(func() {
		toggle(0)
		if err := saveConfigToDBHinted(newDirtyHintContainers([]int{1})); err != nil {
			t.Fatal(err)
		}
	})
	afterIDSet := probeRead(t)

	// 3) 精确声明：只处理声明的 1 行，无任何 O(N) 簿记。
	exact := best(func() {
		toggle(0)
		if err := saveConfigToDBHinted(newExactDirtyHint([]Container{AppConfig.Containers[0]}, nil)); err != nil {
			t.Fatal(err)
		}
	})
	afterExact := probeRead(t)

	// 4) 空精确声明：隔离「每次保存的固定成本」——事务 + app_meta 整表重写 +
	//    日志表重写 + 提交。与 N 无关，规模化下它会主导总写入量。
	floor := best(func() {
		if err := saveConfigToDBHinted(newExactDirtyHint(nil, nil)); err != nil {
			t.Fatal(err)
		}
	})
	afterFloor := probeRead(t)

	for name, got := range map[string]int{
		"全量扫描":   afterFull["containers_INSERT"] - base["containers_INSERT"],
		"ID集合声明": afterIDSet["containers_INSERT"] - afterFull["containers_INSERT"],
		"精确声明":   afterExact["containers_INSERT"] - afterIDSet["containers_INSERT"],
		"空声明":    afterFloor["containers_INSERT"] - afterExact["containers_INSERT"],
	} {
		want := rounds
		if name == "空声明" {
			want = 0
		}
		if got != want {
			t.Fatalf("%s 共 %d 次 INSERT, want %d（每轮必须只写改动行）", name, got, want)
		}
	}

	// 结论：精确声明已降到「固定下限」附近（只写 1 行 ≈ 空保存），说明容器维度的
	// O(N) 成本被彻底消除；ID 集合声明仍残留 O(N) 簿记；全量扫描仍 O(N) 重算指纹。
	t.Logf("N=%d（每项取 %d 轮最小值）全量扫描=%v ID集合声明=%v 精确声明=%v 空声明(固定下限)=%v",
		n, rounds, full.Round(time.Microsecond), idset.Round(time.Microsecond),
		exact.Round(time.Microsecond), floor.Round(time.Microsecond))
}

// ---- #97：目录类集合（子用户/密钥/快照/任务）的脏声明 ----
//
// 背景：这四个集合此前无视声明、每次保存都全量重算指纹。20k 子用户实测 32.6ms，
// 10w 子用户约 163ms —— 心跳是 24/7 的高频落库方，若保存成本随用户目录线性增长，
// 容器维度优化得再彻底也没用。修法：精确模式增加「只改容器」声明，目录类集合
// 整体短路（沿用旧指纹、零扫描）。

// TestExactContainersOnlyLeavesCatalogsUntouched 验证「只改容器」模式对目录类集合
// 的落库行为等价于「它们没变」：不重算指纹、不写任何 SQL、已落库的行不被误删。
//
// 判定不依赖机器性能：以指纹表是否被「原地复用」（map 指针不变）证明扫描被跳过。
func TestExactContainersOnlyLeavesCatalogsUntouched(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	AppConfig.SubUsers = []SubUser{
		{ID: "su-1", Username: "u1", PassHash: "h", Role: "operator", CreatedAt: "2026-01-01"},
		{ID: "su-2", Username: "u2", PassHash: "h", Role: "operator", CreatedAt: "2026-01-01"},
	}
	AppConfig.ApiKeys = []ApiKeyConfig{{ID: "k-1", Name: "k1", KeyHash: "h", Prefix: "p"}}
	AppConfig.Snapshots = []Snapshot{{ID: "snap-1", ContainerID: 1, ContainerName: "ct-1"}}
	AppConfig.Tasks = []SavedTask{{ID: "task-1", Type: "create", ContainerID: 1, Status: "done"}}
	AppConfig.Containers = []Container{{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "sub_users", "api_keys", "snapshots", "tasks", "containers")

	ptrBefore := reflect.ValueOf(persistedRows.subUsers).Pointer()
	base := probeRead(t)

	AppConfig.Containers[0].Status = "stopped"
	if err := saveConfigToDBHinted(newExactContainersHint([]Container{AppConfig.Containers[0]}, nil)); err != nil {
		t.Fatal(err)
	}
	after := probeRead(t)

	for _, table := range []string{"sub_users", "api_keys", "snapshots", "tasks"} {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			if got := after[table+"_"+op] - base[table+"_"+op]; got != 0 {
				t.Fatalf("只改容器的保存写了 %s.%s %d 次，want 0", table, op, got)
			}
		}
	}
	if got := after["containers_INSERT"] - base["containers_INSERT"]; got != 1 {
		t.Fatalf("容器改动未落库：INSERT = %d, want 1", got)
	}
	// 指纹表被原地复用 => diffSubUsers 直接返回，未做任何指纹计算。
	if ptrAfter := reflect.ValueOf(persistedRows.subUsers).Pointer(); ptrAfter != ptrBefore {
		t.Fatal("子用户指纹表被重建，说明目录类集合仍在全量扫描")
	}
	for table, want := range map[string]int{"sub_users": 2, "api_keys": 1, "snapshots": 1, "tasks": 1} {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Fatalf("%s 行数 = %d, want %d（短路不得误删已落库的行）", table, n, want)
		}
	}
}

// TestExactContainersOnlyThenFullSaveRepairsCatalog 钉死「只改容器」的声明契约：
// 未被声明的目录改动本次不落库（这是有意的、已在文档写明），随后的普通全量保存
// 必须把它补上 —— 即声明模式不会让改动永久丢失。
func TestExactContainersOnlyThenFullSaveRepairsCatalog(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	AppConfig.SubUsers = []SubUser{
		{ID: "su-1", Username: "before", PassHash: "h", Role: "operator", CreatedAt: "2026-01-01"},
	}
	AppConfig.Containers = []Container{{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}

	AppConfig.SubUsers[0].Username = "after"
	AppConfig.Containers[0].Status = "stopped"
	// 心跳式保存：只声明容器，子用户改动这一次不落库（契约允许）。
	if err := saveConfigToDBHinted(newExactContainersHint([]Container{AppConfig.Containers[0]}, nil)); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.QueryRow(`SELECT username FROM sub_users WHERE id = 'su-1'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "before" {
		t.Fatalf("未声明的子用户改动被写入（库中 = %q），契约说明与实现不一致", got)
	}

	// 随后一次普通保存（全量兜底）必须补齐，且指纹表在短路后仍然可用。
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT username FROM sub_users WHERE id = 'su-1'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "after" {
		t.Fatalf("全量保存未补齐子用户改动：库中 = %q, want after", got)
	}

	// 再存一次应无写（指纹已与库一致），证明短路没有破坏指纹表的正确性。
	installWriteProbe(t, "sub_users")
	base := probeRead(t)
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	after := probeRead(t)
	if d := after["sub_users_INSERT"] - base["sub_users_INSERT"]; d != 0 {
		t.Fatalf("无改动保存写了 %d 行子用户，want 0", d)
	}
}

// TestExactContainersOnlySkipsSubUserScanAtScale 量化 #97 的收益：同样是「只改 1 个
// 容器」的精确保存，通用精确模式（newExactDirtyHint）仍要全量扫描子用户目录，
// 「只改容器」模式（newExactContainersHint）完全跳过。
// 断言只依赖写量（与机器性能无关）；耗时仅作日志证据。
func TestExactContainersOnlySkipsSubUserScanAtScale(t *testing.T) {
	m := 20000
	if testing.Short() {
		m = 2000
	}
	if raw := os.Getenv("EYVES_SCALE_N"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			t.Fatalf("invalid EYVES_SCALE_N=%q", raw)
		}
		m = v
	}

	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	subs := make([]SubUser, 0, m)
	for i := 0; i < m; i++ {
		subs = append(subs, SubUser{
			ID: fmt.Sprintf("su-%d", i), Username: fmt.Sprintf("user-%d", i),
			PassHash: "h", Role: "operator", CreatedAt: "2026-01-01",
		})
	}
	AppConfig.SubUsers = subs
	AppConfig.Containers = []Container{{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	installWriteProbe(t, "sub_users", "containers")
	base := probeRead(t)

	const rounds = 5
	best := func(fn func()) time.Duration {
		var min time.Duration
		for i := 0; i < rounds; i++ {
			t0 := time.Now()
			fn()
			if d := time.Since(t0); d > 0 && (min == 0 || d < min) {
				min = d
			}
		}
		return min
	}
	toggle := func() {
		if AppConfig.Containers[0].Status == "running" {
			AppConfig.Containers[0].Status = "stopped"
		} else {
			AppConfig.Containers[0].Status = "running"
		}
	}

	general := best(func() {
		toggle()
		if err := saveConfigToDBHinted(newExactDirtyHint([]Container{AppConfig.Containers[0]}, nil)); err != nil {
			t.Fatal(err)
		}
	})
	afterGeneral := probeRead(t)
	containersOnly := best(func() {
		toggle()
		if err := saveConfigToDBHinted(newExactContainersHint([]Container{AppConfig.Containers[0]}, nil)); err != nil {
			t.Fatal(err)
		}
	})
	afterOnly := probeRead(t)

	// 两种模式都不写子用户行（值未变）。
	if d := afterGeneral["sub_users_INSERT"] - base["sub_users_INSERT"]; d != 0 {
		t.Fatalf("通用精确模式写了 %d 行子用户，want 0", d)
	}
	if d := afterOnly["sub_users_INSERT"] - afterGeneral["sub_users_INSERT"]; d != 0 {
		t.Fatalf("只改容器模式写了 %d 行子用户，want 0", d)
	}
	// 容器改动两种模式都必须落库（每轮 1 次 upsert = 1 INSERT）。
	if got := afterOnly["containers_INSERT"] - base["containers_INSERT"]; got != 2*rounds {
		t.Fatalf("容器 INSERT 合计 = %d, want %d", got, 2*rounds)
	}
	t.Logf("子用户 m=%d（每项取 %d 轮最小值）通用精确模式=%v 只改容器模式=%v；差值≈被跳过的全量子用户扫描",
		m, rounds, general.Round(time.Microsecond), containersOnly.Round(time.Microsecond))
}

// BenchmarkFingerprintContainer 量化单行指纹成本——这是增量落库唯一的 O(n) 残项。
func BenchmarkFingerprintContainer(b *testing.B) {
	c := Container{
		ID: 1, UUID: "uuid-1", Name: "ct-1", Virtualization: VirtualizationLXC, LXCName: "ct-1",
		Status: "running", VCPU: 2, RAMMB: 2048, DiskGB: 20, IP: "10.0.0.1",
		PortMappings: []PortMapping{
			{ContainerPort: 22, HostPort: 20000, Protocol: "tcp"},
			{ContainerPort: 80, HostPort: 30000, Protocol: "tcp"},
		},
		PublicIPv4s: []PublicIPv4Assignment{{Address: "203.0.113.1"}},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = containerFingerprint(c)
	}
}

// BenchmarkFingerprintContainerBare 同上但不带子表，用于区分子表开销。
func BenchmarkFingerprintContainerBare(b *testing.B) {
	c := Container{
		ID: 1, UUID: "uuid-1", Name: "ct-1", Virtualization: VirtualizationLXC, LXCName: "ct-1",
		Status: "running", VCPU: 2, RAMMB: 2048, DiskGB: 20, IP: "10.0.0.1",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = containerFingerprint(c)
	}
}

// TestRestartDoesNotRewriteUnchangedRows 钉死「重启不重写整库」：加载配置时即把
// 内存快照播种为行指纹，因此一次重启（关库 → 重新 InitConfig → 保存）不应产生
// 任何行写入。缺了这一步，启动路径拿不到已落库指纹，会把全库当成「新行」重写，
// 10 万容器在 Postgres 上表现为数分钟的启动停滞。
func TestRestartDoesNotRewriteUnchangedRows(t *testing.T) {
	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	path := filepath.Join(dir, "config.json")
	t.Setenv("EYVESCLOUD_DATA_DIR", dir)
	SetConfigPath(path)
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}

	AppConfigMu.Lock()
	AppConfig.Containers = []Container{
		{ID: 1, UUID: "uuid-1", Name: "ct-1", Virtualization: VirtualizationLXC, Status: "running", VCPU: 1, RAMMB: 512, DiskGB: 10},
		{ID: 2, UUID: "uuid-2", Name: "ct-2", Virtualization: VirtualizationLXC, Status: "stopped", VCPU: 2, RAMMB: 1024, DiskGB: 20},
	}
	AppConfig.Nodes = []Node{{ID: "node-1", Name: "node-1", Address: "https://10.0.0.1:8999", Token: "tok-1", Status: "online"}}
	AppConfig.SubUsers = []SubUser{{ID: "su-1", Username: "user-1", PassHash: "hash", Role: "operator"}}
	AppConfigMu.Unlock()
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}

	// 触发器建在库文件里，会跨「重启」存活；先建好再重启，才能把重启后的写入算进来。
	installWriteProbe(t, "containers", "nodes", "sub_users")

	// 一次「重启」= 关库、清空进程级状态（指纹 / 索引 / AppConfig）、重新 InitConfig
	// （内部判定有迁移时会自行保存）再显式保存一次。返回相对上一次快照的增量写入。
	prev := probeRead(t)
	restart := func(label string) map[string]int {
		resetConfigStoreForTest(t)
		SetConfigPath(path)
		if _, err := InitConfig(); err != nil {
			t.Fatal(err)
		}
		if err := SaveConfig(); err != nil {
			t.Fatal(err)
		}
		now := probeRead(t)
		delta := map[string]int{}
		for _, table := range []string{"containers", "nodes", "sub_users"} {
			for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
				k := table + "_" + op
				delta[k] = now[k] - prev[k]
			}
		}
		t.Logf("%s写入增量 %v", label, delta)
		prev = now
		return delta
	}

	// 首次重启允许是一次性迁移（把旧库里的字段归一后落回），二次重启必须零写入——
	// 那才说明「库已成为内存的忠实镜像」，重启不再产生任何写放大。
	restart("首次重启")
	second := restart("二次重启")
	for k, n := range second {
		if n != 0 {
			t.Errorf("二次重启 %s 写了 %d 行，应为 0：库与内存未收敛，每次重启都会重写", k, n)
		}
	}
}
