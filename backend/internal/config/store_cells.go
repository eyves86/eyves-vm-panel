package config

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ---- P3-c 第一片：节点表按 cell 分库 ----
//
// 设计文档 §4.1：每个 cell 拥有独立的元数据分库，主控只持有 node_id→cell_id 映射。
// 本片把 **nodes 表**（心跳的落库目标，B2/B3）按节点所属 cell 路由到各 cell 自己的
// 库；其余表仍留在控制库。未配置任何 cell 库时行为与历史**完全一致**——这正是设计
// 文档 §6 P3 的回滚口径「cell 映射回退为单库」。
//
// 分片顺序：先 nodes（24/7 最高频写入方、无子表），后 containers（行数最多，带
// port_mappings / container_public_ipv4s / container_ipv6_addresses 三张子表，随主行
// 同库同事务写）。容器自己不带 cell 字段，归属**跟随其节点**——节点换库时其容器一并
// 换库（靠指纹键里编码的归属库检测）。container_access_links（访问码凭据，UUID 主键）
// **留在控制库**，不随分库搬运。
//
// 配置来源：环境变量 EYVESCLOUD_CELL_DSNS，逗号分隔 "cell-1=/path/a.db,cell-2=postgres://..."。
// 用 env 而非配置字段，是为了不在本片就引入「凭据入库 + 加密」这条链路（与既有的
// EYVESCLOUD_PG_DSN / EYVESCLOUD_AGENT_GATEWAY_ADDR 风格一致）。未列出的 cell ⇒
// 其节点仍落在控制库。
//
// 一致性口径：跨库没有事务——控制库事务失败不会回滚已提交的 cell 事务（反之亦然）。
// 这是分片的固有代价（设计文档 §7 接受最终一致），也正是 P3 要的隔离性：单个 cell
// 库故障不连坐其它 cell。
//
// 节点换库（改 CellID）走「先写新库、后删旧库」：换库检测靠指纹键里编码的归属库
// （nodeRowKey）——指纹变化即表示这一行要重写，归属一变就顺带把旧库那份删掉
// （applyNodeMoves）。中途崩溃只会在两库各留一份，加载时按 ID 去重自愈
// （mergeCellNodes），绝不会两处都没有。
const cellDSNsEnv = "EYVESCLOUD_CELL_DSNS"

type cellStore struct {
	id   string
	db   *sql.DB
	isPG bool
}

var (
	cellStoresMu sync.Mutex
	cellStores   = map[string]*cellStore{}
)

// parseCellDSNs 解析 "cell-1=dsn,cell-2=dsn"；空项与缺 "=" 的项忽略。
func parseCellDSNs(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, dsn, ok := strings.Cut(part, "=")
		id, dsn = strings.TrimSpace(id), strings.TrimSpace(dsn)
		if !ok || id == "" || dsn == "" {
			continue
		}
		out[id] = dsn
	}
	return out
}

func isPostgresDSN(dsn string) bool {
	lower := strings.ToLower(dsn)
	return strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://")
}

// openCellStores 打开并建表全部已配置的 cell 库。调用方（openConfigDB）已持有 dbMu。
// 节点搬迁不在这里做——搬迁必须排在 migrateLegacyNodesRow 之后（历史节点键要先落
// 表才搬得到），故放在加载路径的 migrateAllCellNodes。
//
// 任一库打不开即返回错误、拒绝启动：否则该 cell 的节点会写进一个不存在的库而静默
// 丢落库——宁可停服务让人来看。
func openCellStores() error {
	dsns := parseCellDSNs(os.Getenv(cellDSNsEnv))
	if len(dsns) == 0 {
		return nil
	}
	ids := make([]string, 0, len(dsns))
	for id := range dsns {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	opened := make(map[string]*cellStore, len(ids))
	closeAll := func() {
		for _, s := range opened {
			_ = s.db.Close()
		}
	}
	for _, id := range ids {
		st, err := openCellStore(id, dsns[id])
		if err != nil {
			closeAll()
			return err
		}
		opened[id] = st
	}

	cellStoresMu.Lock()
	cellStores = opened
	cellStoresMu.Unlock()
	return nil
}

func openCellStore(id, dsn string) (*cellStore, error) {
	isPG := isPostgresDSN(dsn)
	var conn *sql.DB
	var err error
	if isPG {
		conn, err = openPostgresConn(dsn)
	} else {
		if err = os.MkdirAll(filepath.Dir(dsn), 0700); err != nil {
			return nil, fmt.Errorf("创建 cell 库目录失败: %w", err)
		}
		conn, err = sql.Open("sqlite", sqliteDSN(dsn, configDBPragmas))
		if err == nil {
			conn.SetMaxOpenConns(1)
			conn.SetMaxIdleConns(1)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("打开 cell 库失败: %w", err)
	}
	if err := ensureSchemaOn(conn, isPG); err != nil {
		_ = conn.Close()
		return nil, err
	}
	// chmod 必须在建表之后：sql.Open 惰性，文件要等第一条语句才落盘。
	if !isPG {
		_ = os.Chmod(dsn, 0600)
	}
	return &cellStore{id: id, db: conn, isPG: isPG}, nil
}

func closeCellStores() {
	cellStoresMu.Lock()
	stores := cellStores
	cellStores = map[string]*cellStore{}
	cellStoresMu.Unlock()
	for _, s := range stores {
		_ = s.db.Close()
	}
}

// cellStoresSnapshot 返回当前 cell 库集合的副本。集合只在启动/关闭时变化，故保存
// 路径每次取一份快照即可，避免逐行取锁。
func cellStoresSnapshot() map[string]*cellStore {
	cellStoresMu.Lock()
	defer cellStoresMu.Unlock()
	out := make(map[string]*cellStore, len(cellStores))
	for id, s := range cellStores {
		out[id] = s
	}
	return out
}

// cellStoreIDs 返回当前已配置独立分库的 cell id 集合。
func cellStoreIDs() map[string]bool {
	stores := cellStoresSnapshot()
	out := make(map[string]bool, len(stores))
	for id := range stores {
		out[id] = true
	}
	return out
}

// nodeStoreIDIn 在给定快照下判断节点应落到哪个 cell 库；"" 表示控制库。
//
// 判据用节点的原始 CellID（未配置库的 cell 回落到控制库），并把空 CellID 归一为
// DefaultCellID —— 与 CellForNodeLocked 的口径一致，但不依赖 AppConfig.Cells：
// 启动时配置尚未加载，而路由必须在那之前就成立。
func nodeStoreIDIn(n Node, stores map[string]*cellStore) string {
	cell := strings.TrimSpace(n.CellID)
	if cell == "" {
		cell = DefaultCellID
	}
	if _, ok := stores[cell]; ok {
		return cell
	}
	return ""
}

// nodeRowKey 把节点行的**物理归属库**与其内容指纹编码成一个键（\x00 分隔；指纹是
// 十六进制串，不含 \x00）。指纹表因此同时回答两件事：「这一行变没变」与「它现在落在
// 哪个库」——换库必须触发换库写入 + 旧库删除，否则节点会在两个库里各留一份。
func nodeRowKey(storeID string, n Node) string {
	return storeID + "\x00" + fingerprint(n)
}

// rowKeyStore 解出编码键里的归属库；ok=false 表示该键不是本编码（历史/测试直接
// 播种的裸指纹），此时不判定为「换库」。
func rowKeyStore(key string) (string, bool) {
	i := strings.IndexByte(key, 0)
	if i < 0 {
		return "", false
	}
	return key[:i], true
}

// applyNodeUpserts 把节点写入分发到各自的库：控制库走调用方事务 tx，cell 库各开一个
// 独立事务（跨库无事务，见文件头注释）。未配置 cell 库时全部落 tx，行为与历史一致。
func applyNodeUpserts(tx *sql.Tx, stores map[string]*cellStore, nodes []Node) error {
	if len(nodes) == 0 {
		return nil
	}
	byStore := map[string][]Node{}
	for i := range nodes {
		if id := nodeStoreIDIn(nodes[i], stores); id != "" {
			byStore[id] = append(byStore[id], nodes[i])
			continue
		}
		if err := upsertNodeRow(tx, nodes[i]); err != nil {
			return err
		}
	}
	for id, batch := range byStore {
		if err := upsertNodesInTx(stores[id].db, batch); err != nil {
			return fmt.Errorf("cell %s 写入节点失败: %w", id, err)
		}
	}
	return nil
}

func upsertNodesInTx(conn *sql.DB, nodes []Node) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	for i := range nodes {
		if err := upsertNodeRow(tx, nodes[i]); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// applyNodeMoves 清掉「节点换库」留下的旧库副本：新库的写入已由 applyNodeUpserts 完成，
// 这里只负责把 ID 从旧库删掉（oldStore == "" 表示旧副本在控制库）。
//
// 顺序固定为「先写新库、后删旧库」：跨库无事务，中途崩溃只会留下一份重复行（仍可读、
// 加载时按 ID 去重自愈），绝不会两处都没有——反过来删旧库在先就可能丢行。
//
// 旧库已不在 EYVESCLOUD_CELL_DSNS 里（stores[oldStore] == nil）时无从删除：该库本次
// 都没被打开，其行留在库文件里，属运维自行收缩分片规模的既有代价。
func applyNodeMoves(tx *sql.Tx, stores map[string]*cellStore, moved map[string]string) error {
	if len(moved) == 0 {
		return nil
	}
	ids := make([]string, 0, len(moved))
	for id := range moved {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		old := moved[id]
		if old == "" {
			if err := deleteNodeRow(tx, id); err != nil {
				return err
			}
			continue
		}
		st := stores[old]
		if st == nil {
			continue
		}
		if _, err := st.db.Exec(`DELETE FROM nodes WHERE id = ?`, id); err != nil {
			return fmt.Errorf("cell %s 迁移节点失败: %w", old, err)
		}
	}
	return nil
}

// applyNodeDeletes 删除节点：控制库走调用方事务，cell 库逐个广播删除。删除路径可能
// 只拿到 ID（全量扫描时删除来源未知），无法从 ID 反查归属，故对每个 cell 库都执行
// 一次——DELETE 幂等，命中不到即空操作。
func applyNodeDeletes(tx *sql.Tx, stores map[string]*cellStore, ids []string) error {
	for _, id := range ids {
		if err := deleteNodeRow(tx, id); err != nil {
			return err
		}
		for _, st := range stores {
			if _, err := st.db.Exec(`DELETE FROM nodes WHERE id = ?`, id); err != nil {
				return fmt.Errorf("cell %s 删除节点失败: %w", st.id, err)
			}
		}
	}
	return nil
}

// migrateAllCellNodes 把控制库里属于各 cell 的节点搬进各自库。幂等，每次加载都调用
// ——稳定运行的系统里控制库已无 cell 节点，直接空跑。
func migrateAllCellNodes() error {
	stores := cellStoresSnapshot()
	ids := make([]string, 0, len(stores))
	for id := range stores {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := migrateCellNodes(stores[id]); err != nil {
			return fmt.Errorf("cell %s: 节点迁移失败: %w", id, err)
		}
	}
	return nil
}

// migrateCellNodes 把控制库里属于该 cell 的节点搬进 cell 库。幂等：cell 库已有的行
// 直接跳过（随后仍会从控制库删除）；崩溃在「已插入、未删除」之间时下次启动重跑即可。
//
// 先插后删：任何时刻崩溃，节点都至少存在于一处，不会丢。
func migrateCellNodes(st *cellStore) error {
	where := `cell_id = ?`
	args := []any{st.id}
	if st.id == DefaultCellID {
		where = `(cell_id = ? OR cell_id = '')`
	}
	rows, err := queryNodes(db, ` WHERE `+where, args...)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	tx, err := st.db.Begin()
	if err != nil {
		return err
	}
	for i := range rows {
		if err := upsertNodeRow(tx, rows[i]); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, err = db.Exec(`DELETE FROM nodes WHERE `+where, args...)
	return err
}

// loadAllCellNodes 读回所有 cell 库的节点，按创建时间保持顺序。
func loadAllCellNodes() ([]Node, error) {
	stores := cellStoresSnapshot()
	ids := make([]string, 0, len(stores))
	for id := range stores {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []Node
	for _, id := range ids {
		nodes, err := queryNodes(stores[id].db, "")
		if err != nil {
			return nil, fmt.Errorf("cell %s: 读取节点失败: %w", id, err)
		}
		out = append(out, nodes...)
	}
	return out, nil
}

// mergeCellNodes 把 cell 库读到的节点并入内存配置，并按 ID 去重（cell 库优先）。
// 去重兜住两类「同一节点两份」：控制库与 cell 库之间的迁移窗口，以及**两个 cell 库
// 之间**换库时「已写新库、未删旧库」的崩溃窗口。两份内容不同时取 LastSeen 较新者
// （本就同源，只有已删/未删之别）；仍相同则取加载顺序在先者（loadAllCellNodes 按
// cell id 升序读取，结果确定）。
func mergeCellNodes(cfg *EyvescloudConfig, cellNodes []Node) {
	if len(cellNodes) == 0 {
		return
	}
	byID := make(map[string]int, len(cellNodes))
	kept := make([]Node, 0, len(cellNodes))
	for i := range cellNodes {
		n := cellNodes[i]
		idx, ok := byID[n.ID]
		if !ok {
			byID[n.ID] = len(kept)
			kept = append(kept, n)
			continue
		}
		if n.LastSeen > kept[idx].LastSeen {
			kept[idx] = n
		}
	}
	out := make([]Node, 0, len(cfg.Nodes)+len(kept))
	for i := range cfg.Nodes {
		if _, dup := byID[cfg.Nodes[i].ID]; dup {
			continue
		}
		out = append(out, cfg.Nodes[i])
	}
	cfg.Nodes = append(out, kept...)
	// 各库分别按 created_at 有序，但拼接后是「控制库 → 各 cell 库」而非全局有序；
	// 这里统一按 (created_at, id) 重排，保持与未分片一致的全局顺序（顺序即 API
	// 下发顺序）。
	sort.SliceStable(cfg.Nodes, func(i, j int) bool {
		if cfg.Nodes[i].CreatedAt != cfg.Nodes[j].CreatedAt {
			return cfg.Nodes[i].CreatedAt < cfg.Nodes[j].CreatedAt
		}
		return cfg.Nodes[i].ID < cfg.Nodes[j].ID
	})
}

// ---- 容器（P3-c 第二片）：归属跟随节点 ----

// containerRowKey 把容器行的**物理归属库**与其内容指纹编码成一个键（与 nodeRowKey
// 同一口径）：归属一变指纹键就变 ⇒ 触发新库写入 + 旧库删除（applyContainerMoves），
// 不会出现「两库各留一份还都算未变」。
func containerRowKey(storeID string, c Container) string {
	return storeID + "\x00" + containerFingerprint(c)
}

// containerStoreIDIn 判断容器应落到哪个库：容器自己不带 cell 字段，归属**跟随其节点**。
// 节点归属查指纹表 next.nodes（saveConfigIncremental 里 diffNodes 先于 diffContainers
// 执行，故已含本次保存的节点变更）；指纹表没有该节点时（同一次保存里新建节点+其容器
// 且节点未声明的罕见路径）回落扫 AppConfig.Nodes。空 NodeID / 节点不存在 / 该 cell
// 未配库 → 控制库（""）。
func containerStoreIDIn(c Container, next *rowFingerprints, stores map[string]*cellStore) string {
	if c.NodeID == "" {
		return ""
	}
	if key, ok := next.nodes[c.NodeID]; ok {
		if sid, ok2 := rowKeyStore(key); ok2 && sid != "" {
			if _, alive := stores[sid]; alive {
				return sid
			}
		}
		return ""
	}
	// 装载期（loadConfigFromDB → seedPersistedRows）AppConfig 尚未装配、为 nil；
	// 节点不存在 → 按文档语义回落控制库，装载期不得触碰全局。
	if AppConfig != nil {
		for i := range AppConfig.Nodes {
			if AppConfig.Nodes[i].ID == c.NodeID {
				return nodeStoreIDIn(AppConfig.Nodes[i], stores)
			}
		}
	}
	return ""
}

// applyContainerUpserts 把容器写入（主行 + 三张子表，同一事务）分发到各自的库：控制库
// 走调用方事务 tx，cell 库各开一个独立事务（跨库无事务，见文件头注释）。byStore 的键
// 为归属库、"" 表示控制库——分组在 diff 处完成，避免逐行重复解析归属。
func applyContainerUpserts(tx *sql.Tx, stores map[string]*cellStore, byStore map[string][]Container) error {
	if len(byStore) == 0 {
		return nil
	}
	for i := range byStore[""] {
		if err := upsertContainerRow(tx, byStore[""][i]); err != nil {
			return err
		}
	}
	cellIDs := make([]string, 0, len(byStore))
	for id := range byStore {
		if id != "" {
			cellIDs = append(cellIDs, id)
		}
	}
	sort.Strings(cellIDs)
	for _, id := range cellIDs {
		if err := upsertContainersInTx(stores[id].db, byStore[id]); err != nil {
			return fmt.Errorf("cell %s 写入容器失败: %w", id, err)
		}
	}
	return nil
}

func upsertContainersInTx(conn *sql.DB, cs []Container) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	for i := range cs {
		if err := upsertContainerRow(tx, cs[i]); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// applyContainerMoves 清掉「容器换库」（其节点改属 cell）留下的旧库副本：新库的写入
// 已由 applyContainerUpserts 完成，这里按旧库分组删除（oldStore == "" 表示控制库）。
// 顺序固定「先写新库、后删旧库」：中途崩溃只会留下重复行（加载合并按 ID 去重自愈），
// 绝不会两处都没有。旧库已不在 EYVESCLOUD_CELL_DSNS 里时无从删除（同 applyNodeMoves）。
func applyContainerMoves(tx *sql.Tx, stores map[string]*cellStore, moved map[string][]int) error {
	if len(moved) == 0 {
		return nil
	}
	oldStores := make([]string, 0, len(moved))
	for old := range moved {
		oldStores = append(oldStores, old)
	}
	sort.Strings(oldStores)
	for _, old := range oldStores {
		ids := moved[old]
		if old == "" {
			for _, id := range ids {
				if err := deleteContainerRow(tx, id); err != nil {
					return err
				}
			}
			continue
		}
		st := stores[old]
		if st == nil {
			continue
		}
		tx2, err := st.db.Begin()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := deleteContainerRow(tx2, id); err != nil {
				_ = tx2.Rollback()
				return fmt.Errorf("cell %s 迁移容器失败: %w", old, err)
			}
		}
		if err := tx2.Commit(); err != nil {
			return fmt.Errorf("cell %s 迁移容器提交失败: %w", old, err)
		}
	}
	return nil
}

// applyContainerDeletes 删除容器：控制库走调用方事务，cell 库逐库批量广播删除（删除
// 路径只拿得到 ID，无法反查归属；DELETE 幂等，命中不到即空操作）。
// container_access_links（控制库、UUID 主键）由 diffAccessLinks 独立处理，不随行删。
func applyContainerDeletes(tx *sql.Tx, stores map[string]*cellStore, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		if err := deleteContainerRow(tx, id); err != nil {
			return err
		}
	}
	cellIDs := make([]string, 0, len(stores))
	for id := range stores {
		cellIDs = append(cellIDs, id)
	}
	sort.Strings(cellIDs)
	for _, sid := range cellIDs {
		tx2, err := stores[sid].db.Begin()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := deleteContainerRow(tx2, id); err != nil {
				_ = tx2.Rollback()
				return fmt.Errorf("cell %s 删除容器失败: %w", sid, err)
			}
		}
		if err := tx2.Commit(); err != nil {
			return fmt.Errorf("cell %s 删除容器提交失败: %w", sid, err)
		}
	}
	return nil
}

// migrateCellContainers 把控制库里「节点已归属 cell 分库」的容器搬进各自库。幂等，
// 每次加载都调用——稳定运行时零容器命中、零 SQL。成员资格直接用内存 cfg（节点已在
// migrateAllCellNodes/loadNodes/mergeCellNodes 之后合并完成），子表数据就在容器结构体
// 里、随 upsertContainerRow 一起写。访问码凭据不随迁（见文件头注释）。
// 先写 cell 库、后删控制库：崩溃窗口留重复行，加载合并去重自愈，绝不丢行。
func migrateCellContainers(cfg *EyvescloudConfig) error {
	stores := cellStoresSnapshot()
	if len(stores) == 0 || len(cfg.Containers) == 0 {
		return nil
	}
	nodeStore := make(map[string]string, len(cfg.Nodes))
	for i := range cfg.Nodes {
		nodeStore[cfg.Nodes[i].ID] = nodeStoreIDIn(cfg.Nodes[i], stores)
	}
	byStore := map[string][]Container{}
	for i := range cfg.Containers {
		if sid := nodeStore[cfg.Containers[i].NodeID]; sid != "" {
			byStore[sid] = append(byStore[sid], cfg.Containers[i])
		}
	}
	if len(byStore) == 0 {
		return nil
	}
	cellIDs := make([]string, 0, len(byStore))
	for id := range byStore {
		cellIDs = append(cellIDs, id)
	}
	sort.Strings(cellIDs)
	for _, id := range cellIDs {
		if err := upsertContainersInTx(stores[id].db, byStore[id]); err != nil {
			return fmt.Errorf("cell %s: 容器迁移失败: %w", id, err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for _, id := range cellIDs {
		for i := range byStore[id] {
			if err := deleteContainerRow(tx, byStore[id][i].ID); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	}
	return tx.Commit()
}

// loadAllCellContainers 读回所有 cell 库的容器（含子表），按 cell id 升序拼接。
func loadAllCellContainers() ([]Container, error) {
	stores := cellStoresSnapshot()
	ids := make([]string, 0, len(stores))
	for id := range stores {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []Container
	for _, id := range ids {
		cs, err := loadContainersFrom(stores[id].db)
		if err != nil {
			return nil, fmt.Errorf("cell %s: 读取容器失败: %w", id, err)
		}
		out = append(out, cs...)
	}
	return out, nil
}

// mergeCellContainers 把 cell 库读到的容器并入内存配置，按 ID 去重（cell 库副本优先）
// 并恢复全局 ID 序（与未分片的 ORDER BY id 一致）。去重兜住两类「同一容器两份」：
// 控制库与 cell 库之间的迁移窗口，以及节点换库时「已写新库、未删旧库」的崩溃窗口。
// Container 没有 LastSeen 可判新旧，两份有差异时取 cell 库副本——它才是行该在的地方，
// 控制库副本只可能是迁移残留。
func mergeCellContainers(cfg *EyvescloudConfig, cellContainers []Container) {
	if len(cellContainers) == 0 {
		return
	}
	byID := make(map[int]bool, len(cellContainers))
	for i := range cellContainers {
		byID[cellContainers[i].ID] = true
	}
	out := make([]Container, 0, len(cfg.Containers)+len(cellContainers))
	for i := range cfg.Containers {
		if !byID[cfg.Containers[i].ID] {
			out = append(out, cfg.Containers[i])
		}
	}
	cfg.Containers = append(out, cellContainers...)
	sort.SliceStable(cfg.Containers, func(i, j int) bool {
		return cfg.Containers[i].ID < cfg.Containers[j].ID
	})
}
