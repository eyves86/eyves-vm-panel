package config

import (
	"database/sql"
	"strings"
)

// ---- P1 行级增量落库 ----
//
// 背景：saveConfigToDB 此前是「DELETE 17 张表 + 重插整份内存快照」。单次用户
// 操作写 O(全量) 行；容器/子用户到 10w 量级后，任何一次保存（含节点定时心跳
// 落库）都会重写数十万行，写放大不可接受，设计不变量「任何单次用户操作只应
// 写 O(受影响行) 行」被破坏。
//
// 本文件把保存路径改成行级增量：以「已落库行指纹」为准，只对发生变化的行做
// upsert、对已消失的行做 delete，未变化的行完全不碰（不变更 SQL，也不产生
// 任何语句）。
//
// 指纹取自**内存态明文结构体**（按类型预编译的编码计划 → FNV-1a 64bit，见
// store_fingerprint.go）。之所以用明文：敏感字段落库走 AES-GCM，密文每次加密
// 都不同；若对密文取指纹，同一条记录会被判定为「每次都变了」而每条必写。明文
// 未变即跳过写入，库里保留旧密文仍可解密。
//
// 前提：参与指纹的结构体（Container / SubUser / ApiKeyConfig / Snapshot /
// SavedTask）每个需要落库的字段都必须有 json tag —— JSON 覆盖面即落库覆盖面。
// 新增字段务必带 json tag，否则该字段的变化不会被检出（由
// TestFingerprintCoversPersistedFields 兜底）；编码计划的完备性则由
// TestFingerprintCoversEveryField 兜底。

type rowFingerprints struct {
	containers    map[int]string
	nodes         map[string]string
	subUsers      map[string]string
	apiKeys       map[string]string
	snapshots     map[string]string
	tasks         map[string]string
	accessLinks   map[string]string
	enabledImages string
	// meta 是 app_meta 按「键」的行指纹（见 store_meta.go）。刻意每次保存新建：
	// 它不像 containers 那样原地复用 persistedRows，因此提交失败无需回滚。
	meta map[string]string
}

func newRowFingerprints() rowFingerprints {
	return rowFingerprints{
		containers:  make(map[int]string),
		nodes:       make(map[string]string),
		subUsers:    make(map[string]string),
		apiKeys:     make(map[string]string),
		snapshots:   make(map[string]string),
		tasks:       make(map[string]string),
		accessLinks: make(map[string]string),
		meta:        make(map[string]string),
	}
}

// persistedRows 记录「上一次成功落库」的各集合行指纹。仅在 dbMu 保护下读写；
// 重启后由 loadConfigFromDB 用加载到的内存快照重新播种（内存==库），故重启后
// 的第一次保存若无改动不会产生任何写。
var persistedRows = newRowFingerprints()

func resetPersistedRows() {
	persistedRows = newRowFingerprints()
	resetRowIndexes()
	logsDirty = false
}

// containerFingerprint 与落库口径一致：先做资源别名归一（saveContainers 也做），
// 再取指纹，避免内存未归一 / 库里已归一造成误判。
func containerFingerprint(c Container) string {
	NormalizeContainerResourceAliases(&c)
	return fingerprint(c)
}

// DirtySet 是写入路径「精确模式」的改动声明：**只列出的行会被处理，未列出即视为
// 未变**（节点、容器、目录类集合都一并未改动）。零值表示「本次没有任何改动」。
//
// 注意 app_meta 键不在此列：diffMeta 每跳仍按键指纹 diff（键数有界 ~80，成本固定
// 1.2-1.5ms，见 #98），日志表也照旧按有界行重写。故精确模式省的是 O(行数) 的扫描，
// 不是这两项固定开销。
//
// 契约很强，漏声明会静默漏写，所以只应用于能穷举改动的高频路径；默认入口
// saveConfigToDB()（全量扫描）才是兜底。
type DirtySet struct {
	Nodes             []Node
	RemovedNodes      []Node
	Containers        []Container
	RemovedContainers []Container

	// 目录类集合（子用户 / API Key / 快照 / 任务）的精确声明。仅当 hint 由
	// newExactCatalogSet 构造时才生效；由 newExactDirtySet 构造时这四项被声明为
	// 「未改动」（零扫描），即使填了也不会被处理。
	SubUsers         []SubUser
	RemovedSubUsers  []string
	APIKeys          []ApiKeyConfig
	RemovedAPIKeys   []string
	Snapshots        []Snapshot
	RemovedSnapshots []string
	Tasks            []SavedTask
	RemovedTasks     []string
}

// dirtyHint 是写入路径对「本次保存可能改动了哪些行」的声明，用于把指纹扫描从
// O(全部行) 压到 O(声明行)。nil（未声明）表示不做假设、回退全量扫描——这是**安全
// 兜底**：未被改造的写入路径仍走全量，绝不会漏写。
//
// 两种模式：
//   - ID 集合模式（containers）：调用方只声明「哪些容器 ID 动过」，其余集合仍全量
//     扫描；未声明的容器行沿用旧指纹、不写 SQL，但簿记仍 O(全部容器)。
//   - 精确模式（exact + set）：调用方给出完整的 DirtySet，只处理列出的行，指纹表
//     原地复用，簿记也是 O(声明行)。**代价是契约更强：未列出即视为未变，漏声明
//     会静默漏写**，故只应用于能穷举改动的高频路径。
type dirtyHint struct {
	containers map[int]bool

	exact bool
	set   DirtySet
	// catalogsUntouched 表示调用方保证本次保存**没有改动目录类集合**（子用户 /
	// API Key / 快照 / 任务）：它们一律沿用已落库指纹，完全跳过指纹扫描（见 #97）。
	//
	// 为什么必须有它：这四个集合此前每次保存都要对全部行重算指纹，20k 子用户实测
	// 32.6ms、10w 子用户约 163ms。心跳是 24/7 的高频落库方（30k 节点 ≈500 次/秒），
	// 若保存成本随「用户目录规模」线性增长，节点维度优化得再彻底也没用。
	//
	// 默认 false = 全量扫描，故只有显式声明「未改动」的路径才享受这个短路。
	catalogsUntouched bool
	// catalogsDeclared 表示调用方**声明了目录类集合的具体改动行**（见 newExactCatalogSet）：
	// 只处理 set 里列出的目录行，未列出即视为未变。与 catalogsUntouched 互斥。
	catalogsDeclared bool
	undo             []fpUndo
}

// declaresCatalogs 表示本 hint 走「目录行级精确声明」而非「目录未改动短路」。
func (h *dirtyHint) declaresCatalogs() bool {
	return h != nil && h.exact && h.catalogsDeclared
}

// newDirtyHintContainers 用容器 ID 列表构造 ID 集合模式声明。
func newDirtyHintContainers(ids []int) *dirtyHint {
	m := make(map[int]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return &dirtyHint{containers: m}
}

// newExactDirtyHint 构造「只改容器」的精确声明：changed 为本次可能变动/新增的容器，
// removed 为本次被删除的容器（需保留原值以清理其访问码行）。精确模式通性「未列出即
// 未变」也适用于节点，故节点一并视为未变；但目录类集合**仍全量扫描**，因此只用于
// 测试与「确实没碰目录」的过渡场景，热路径请用 newExactDirtySet 显式声明。
func newExactDirtyHint(changed, removed []Container) *dirtyHint {
	return &dirtyHint{exact: true, set: DirtySet{Containers: changed, RemovedContainers: removed}}
}

// newExactDirtySet 用完整声明（节点 + 容器）构造精确模式，并把目录类集合声明为
// 未改动（零扫描）。**前提：调用方确实没有触碰子用户 / 密钥 / 快照 / 任务**，
// 否则改动会静默漏写。
func newExactDirtySet(set DirtySet) *dirtyHint {
	return &dirtyHint{exact: true, set: set, catalogsUntouched: true}
}

// newExactContainersHint 构造「只改容器」的精确声明：容器按声明处理、无节点改动，
// 目录类集合声明为未改动、零扫描。
func newExactContainersHint(changed, removed []Container) *dirtyHint {
	return newExactDirtySet(DirtySet{Containers: changed, RemovedContainers: removed})
}

// newExactCatalogOnlyHint 构造「只改目录类集合」的精确声明：节点 / 容器 / 访问码一律
// 视为未改动（指纹表原地复用，零指纹、零 SQL），目录类集合（子用户 / 密钥 / 快照 / 任务）
// 照常全量扫描。
//
// 用于管理接口里「只改一个子用户 / 一个 API Key」的落库路径。它们此前走全量兜底：
// 10w 容器 + 3w 节点 + 10w 子用户实测 594ms，本模式 **153ms**（#110 基线，约 4×）。
// 注意 153ms 全部来自「目录本身仍要全量扫描」——子用户到 10w 时目录扫描就是大头，
// 所以本模式**不会随目录规模降为 O(1)**；「每次请求都写」的路径还需调用方节流。
// **契约：fn 绝不能增删改 Nodes / Containers，也不得改容器的 AccessCode***，否则这些
// 改动会静默漏写（不会报错）。
func newExactCatalogOnlyHint() *dirtyHint {
	return &dirtyHint{exact: true}
}

// newExactCatalogSet 构造「目录类集合精确声明」：节点 / 容器 / 访问码视为未改动
// （指纹表原地复用，零指纹、零 SQL），目录类集合只处理 set 里列出的行，未列出即
// 视为未变。与 newExactCatalogOnlyHint 的区别是**目录不再全量扫描**，因此目录保存
// 从 O(全部目录行) 降到 O(声明行)。
//
// 用于「每次请求都写一行目录」的热路径（api_key.last_used、管理接口只改一个子用户）：
// 10w 子用户下扫描式目录保存约 164ms，本模式为亚毫秒级。
//
// **契约**：fn 必须穷举本次改动的目录行（漏声明会静默漏写），且绝不能增删改
// Nodes / Containers，也不得改容器的 AccessCode*。
func newExactCatalogSet(set DirtySet) *dirtyHint {
	return &dirtyHint{exact: true, catalogsDeclared: true, set: set}
}

// fpUndo 记录精确模式对 persistedRows 的原地改动，用于提交失败时回滚。
type fpUndo struct {
	kind   byte
	intKey int
	strKey string
	prev   string
	had    bool
}

const (
	fpUndoContainer  byte = 1
	fpUndoAccessLink byte = 2
	fpUndoNode       byte = 3
	fpUndoSubUser    byte = 4
	fpUndoAPIKey     byte = 5
	fpUndoSnapshot   byte = 6
	fpUndoTask       byte = 7
)

func (h *dirtyHint) recordContainer(id int, prev string, had bool) {
	h.undo = append(h.undo, fpUndo{kind: fpUndoContainer, intKey: id, prev: prev, had: had})
}

func (h *dirtyHint) recordStr(kind byte, key, prev string, had bool) {
	h.undo = append(h.undo, fpUndo{kind: kind, strKey: key, prev: prev, had: had})
}

func (h *dirtyHint) recordAccessLink(uuid, prev string, had bool) {
	h.recordStr(fpUndoAccessLink, uuid, prev, had)
}

func (h *dirtyHint) recordNode(id, prev string, had bool) {
	h.recordStr(fpUndoNode, id, prev, had)
}

// rollbackIfExact 撤销本次精确保存对 persistedRows 的原地改动。提交失败后调用，
// 使内存指纹表与库内容重新一致；配合 forceFullScanNextSave，下次保存会全量补齐。
func (h *dirtyHint) rollbackIfExact() {
	if h == nil || !h.exact {
		return
	}
	for i := len(h.undo) - 1; i >= 0; i-- {
		u := h.undo[i]
		if u.kind == fpUndoContainer {
			if u.had {
				persistedRows.containers[u.intKey] = u.prev
			} else {
				delete(persistedRows.containers, u.intKey)
			}
			continue
		}
		var m map[string]string
		switch u.kind {
		case fpUndoAccessLink:
			m = persistedRows.accessLinks
		case fpUndoNode:
			m = persistedRows.nodes
		case fpUndoSubUser:
			m = persistedRows.subUsers
		case fpUndoAPIKey:
			m = persistedRows.apiKeys
		case fpUndoSnapshot:
			m = persistedRows.snapshots
		case fpUndoTask:
			m = persistedRows.tasks
		}
		if m == nil {
			continue
		}
		if u.had {
			m[u.strKey] = u.prev
		} else {
			delete(m, u.strKey)
		}
	}
	h.undo = nil
}

// forceFullScanNextSave 在声明式保存失败后置位：下一次保存强制回退全量扫描，
// 保证「失败的那次改动」不会因为后续遗漏声明而永久丢失。
var forceFullScanNextSave bool

// logsDirty 表示内存里的 audit_logs / login_logs 可能与库不一致，下一次保存需要
// 重写这两张有界表（≤500 / ≤200 行）。由 dbMu 保护（与 forceFullScanNextSave 同）。
//
// 为什么需要它（#95）：常规日志写入走 appendAuditLogRow / appendLoginLogRow —— 它们
// 每追加一条就单独 INSERT 一次并顺带裁剪，库已与内存一致；而此前每次保存又无条件
// DELETE+INSERT 这两张表（约 700 行），在心跳高频落库下（30k 节点 ≈500 次/秒）纯属
// 无谓的固定写入。改为「只有内存被改动却没同步到库」的路径才置位：
//   - 日志追加落库失败（appendAuditLogRow 返回 error）→ 置位，下次保存自愈补齐；
//   - 保留期裁剪（api.purgeRetainedLogs 在内存里删除旧行）→ 置位。
//
// 注意：置位方一律通过 MarkLogsDirty()，在 saveConfigToDBHinted 持有 dbMu 期间会被
// 阻塞，因此「保存中发生的置位」不会丢——保存结束才轮到置位者拿到锁。
var logsDirty bool

// MarkLogsDirty 标记有界日志表需要在下一次保存时重写。任何「改了内存日志却没同步
// 到库」的路径都必须调用它，否则该改动会一直留在内存、重启即丢。
func MarkLogsDirty() {
	dbMu.Lock()
	logsDirty = true
	dbMu.Unlock()
}

// saveConfigIncremental 是 saveConfigToDB 的新实现体：按指纹 diff 逐行落库。
// 返回新的指纹集合，调用方在事务提交成功后用它替换 persistedRows。
func saveConfigIncremental(tx *sql.Tx, hint *dirtyHint) (rowFingerprints, error) {
	if forceFullScanNextSave {
		hint = nil
		forceFullScanNextSave = false
	}
	next := newRowFingerprints()

	// 顺序契约：diffNodes 必须先于 diffContainers——容器（P3-c）的物理归属库跟随其
	// 节点，靠 next.nodes 里的编码键解出，节点先 diff 才能反映本次的归属变更。
	steps := []func() error{
		func() error { return diffNodes(tx, &next, hint) },
		func() error { return diffContainers(tx, &next, hint) },
		func() error { return diffAccessLinks(tx, &next, hint) },
		func() error { return diffSubUsers(tx, &next, hint) },
		func() error { return diffAPIKeys(tx, &next, hint) },
		func() error { return diffSnapshots(tx, &next, hint) },
		func() error { return diffTasks(tx, &next, hint) },
		func() error { return diffEnabledImages(tx, &next) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			hint.rollbackIfExact()
			return next, err
		}
	}
	return next, nil
}

// diffNodes 是节点的行级 diff（P2：节点从单行 JSON 改为每节点一行）。
// 精确模式下只处理声明的节点；否则全量扫描。
//
// P3-c：节点表可分布于多个 cell 库。写入/删除按节点归属路由到对应库（见 store_cells.go
// 的 applyNodeUpserts / applyNodeDeletes）；指纹键里编码了归属库（nodeRowKey），故
// 「换 cell」= 指纹变化 ⇒ 触发换库写入，并从旧库删掉副本（applyNodeMoves）。指纹本身
// 仍只回答「变没变」，与行落在哪个库无关。未配置任何 cell 库时行为与历史完全一致。
func diffNodes(tx *sql.Tx, next *rowFingerprints, hint *dirtyHint) error {
	stores := cellStoresSnapshot()
	var upserts []Node
	var deletes []string
	// moved：ID → 旧归属库（"" = 控制库）。
	moved := map[string]string{}

	if hint != nil && hint.exact {
		next.nodes = persistedRows.nodes
		for i := range hint.set.Nodes {
			n := hint.set.Nodes[i]
			prev, known := next.nodes[n.ID]
			target := nodeStoreIDIn(n, stores)
			key := nodeRowKey(target, n)
			if known && prev == key {
				continue
			}
			if known {
				if old, ok := rowKeyStore(prev); ok && old != target {
					moved[n.ID] = old
				}
			}
			hint.recordNode(n.ID, prev, known)
			next.nodes[n.ID] = key
			upserts = append(upserts, n)
		}
		for i := range hint.set.RemovedNodes {
			n := hint.set.RemovedNodes[i]
			prev, known := next.nodes[n.ID]
			if !known {
				continue
			}
			hint.recordNode(n.ID, prev, true)
			delete(next.nodes, n.ID)
			deletes = append(deletes, n.ID)
		}
	} else {
		seen := make(map[string]bool, len(AppConfig.Nodes))
		for i := range AppConfig.Nodes {
			n := AppConfig.Nodes[i]
			seen[n.ID] = true
			target := nodeStoreIDIn(n, stores)
			key := nodeRowKey(target, n)
			prev, known := persistedRows.nodes[n.ID]
			next.nodes[n.ID] = key
			if known && prev == key {
				continue
			}
			if known {
				if old, ok := rowKeyStore(prev); ok && old != target {
					moved[n.ID] = old
				}
			}
			upserts = append(upserts, n)
		}
		for id := range persistedRows.nodes {
			if !seen[id] {
				deletes = append(deletes, id)
			}
		}
	}

	if err := applyNodeUpserts(tx, stores, upserts); err != nil {
		return err
	}
	if err := applyNodeMoves(tx, stores, moved); err != nil {
		return err
	}
	return applyNodeDeletes(tx, stores, deletes)
}

// diffContainers 是容器的行级 diff。
//
// P3-c：容器可分布于多个 cell 库（归属跟随其节点）。指纹键里编码了归属库
// （containerRowKey），故「节点换 cell ⇒ 其容器换库」= 指纹变化 ⇒ 触发新库写入 +
// 旧库删除（applyContainerMoves）。未配置任何 cell 库时行为与历史完全一致。
func diffContainers(tx *sql.Tx, next *rowFingerprints, hint *dirtyHint) error {
	if hint != nil && hint.exact {
		return diffContainersExact(tx, next, hint)
	}
	stores := cellStoresSnapshot()
	seen := make(map[int]bool, len(AppConfig.Containers))
	upserts := map[string][]Container{}
	moved := map[string][]int{}
	var deletes []int
	for i := range AppConfig.Containers {
		c := AppConfig.Containers[i]
		seen[c.ID] = true
		target := containerStoreIDIn(c, next, stores)
		key := containerRowKey(target, c)
		prev, known := persistedRows.containers[c.ID]
		// 声明式快速路径：未声明改动且上次已落库的行沿用旧指纹，既不重算指纹
		// 也不产生任何 SQL。（未声明 = nil hint，走全量。）
		if hint != nil && known && !hint.containers[c.ID] {
			next.containers[c.ID] = prev
			continue
		}
		next.containers[c.ID] = key
		if known && prev == key {
			continue
		}
		if known {
			if old, ok := rowKeyStore(prev); ok && old != target {
				moved[old] = append(moved[old], c.ID)
			}
		}
		upserts[target] = append(upserts[target], c)
	}
	for id := range persistedRows.containers {
		if !seen[id] {
			deletes = append(deletes, id)
		}
	}
	if err := applyContainerUpserts(tx, stores, upserts); err != nil {
		return err
	}
	if err := applyContainerMoves(tx, stores, moved); err != nil {
		return err
	}
	return applyContainerDeletes(tx, stores, deletes)
}

// diffContainersExact 只处理调用方列出的容器：指纹表原地复用，未列出的行
// 既不算指纹也不写入，簿记 O(声明行)。
func diffContainersExact(tx *sql.Tx, next *rowFingerprints, hint *dirtyHint) error {
	stores := cellStoresSnapshot()
	next.containers = persistedRows.containers
	upserts := map[string][]Container{}
	moved := map[string][]int{}
	var deletes []int
	for i := range hint.set.Containers {
		c := hint.set.Containers[i]
		target := containerStoreIDIn(c, next, stores)
		key := containerRowKey(target, c)
		prev, known := next.containers[c.ID]
		if known && prev == key {
			continue
		}
		if known {
			if old, ok := rowKeyStore(prev); ok && old != target {
				moved[old] = append(moved[old], c.ID)
			}
		}
		hint.recordContainer(c.ID, prev, known)
		next.containers[c.ID] = key
		upserts[target] = append(upserts[target], c)
	}
	for i := range hint.set.RemovedContainers {
		c := hint.set.RemovedContainers[i]
		prev, known := next.containers[c.ID]
		if !known {
			continue
		}
		hint.recordContainer(c.ID, prev, true)
		delete(next.containers, c.ID)
		deletes = append(deletes, c.ID)
	}
	if err := applyContainerUpserts(tx, stores, upserts); err != nil {
		return err
	}
	if err := applyContainerMoves(tx, stores, moved); err != nil {
		return err
	}
	return applyContainerDeletes(tx, stores, deletes)
}

func diffAccessLinks(tx *sql.Tx, next *rowFingerprints, hint *dirtyHint) error {
	if hint != nil && hint.exact {
		return diffAccessLinksExact(tx, next, hint)
	}
	seen := make(map[string]bool, len(AppConfig.Containers))
	for i := range AppConfig.Containers {
		c := AppConfig.Containers[i]
		code := strings.TrimSpace(c.AccessCode)
		pw := strings.TrimSpace(c.AccessCodePassword)
		if code == "" && pw == "" {
			continue
		}
		seen[c.UUID] = true
		if hint != nil && !hint.containers[c.ID] {
			if prev, ok := persistedRows.accessLinks[c.UUID]; ok {
				next.accessLinks[c.UUID] = prev
				continue
			}
		}
		fp := fingerprint([2]string{code, pw})
		next.accessLinks[c.UUID] = fp
		if persistedRows.accessLinks[c.UUID] == fp {
			continue
		}
		if err := upsertAccessLinkRow(tx, c); err != nil {
			return err
		}
	}
	for uuid := range persistedRows.accessLinks {
		if seen[uuid] {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM container_access_links WHERE container_uuid = ?`, uuid); err != nil {
			return err
		}
	}
	return nil
}

func diffAccessLinksExact(tx *sql.Tx, next *rowFingerprints, hint *dirtyHint) error {
	next.accessLinks = persistedRows.accessLinks
	for i := range hint.set.Containers {
		if err := applyAccessLinkExact(tx, next, hint, hint.set.Containers[i]); err != nil {
			return err
		}
	}
	for i := range hint.set.RemovedContainers {
		uuid := hint.set.RemovedContainers[i].UUID
		prev, known := next.accessLinks[uuid]
		if !known {
			continue
		}
		hint.recordAccessLink(uuid, prev, true)
		delete(next.accessLinks, uuid)
		if _, err := tx.Exec(`DELETE FROM container_access_links WHERE container_uuid = ?`, uuid); err != nil {
			return err
		}
	}
	return nil
}

// applyAccessLinkExact 重算单个容器的访问码链接（精确模式）。访问码被清空时删除链接。
func applyAccessLinkExact(tx *sql.Tx, next *rowFingerprints, hint *dirtyHint, c Container) error {
	code := strings.TrimSpace(c.AccessCode)
	pw := strings.TrimSpace(c.AccessCodePassword)
	prev, known := next.accessLinks[c.UUID]
	if code == "" && pw == "" {
		if !known {
			return nil
		}
		hint.recordAccessLink(c.UUID, prev, true)
		delete(next.accessLinks, c.UUID)
		_, err := tx.Exec(`DELETE FROM container_access_links WHERE container_uuid = ?`, c.UUID)
		return err
	}
	fp := fingerprint([2]string{code, pw})
	if known && prev == fp {
		return nil
	}
	hint.recordAccessLink(c.UUID, prev, known)
	next.accessLinks[c.UUID] = fp
	return upsertAccessLinkRow(tx, c)
}

// ---- 目录类集合（子用户 / API Key / 快照 / 任务）的行级 diff ----
//
// 这四个集合主键都是字符串、落库形状一致（指纹 + upsert + delete），故共用一份
// 泛型实现，避免四份逐字复制的 diff 逻辑各自漂移。
//
// 短路由调用方显式声明（见 skipIfCatalogsUntouched）：只有「确实没碰目录」的路径
// 才跳过扫描，泛型实现本身不含任何隐式假设。节点虽同属字符串主键，但比热路径
// 变动更频繁、又不是目录，故单独实现 diffNodes、不复用这里的短路。

// strKeyedDiff 描述一个「字符串主键」集合的行级 diff 契约。
type strKeyedDiff[T any] struct {
	dst    *map[string]string
	prev   map[string]string
	rows   []T
	id     func(T) string
	upsert func(*sql.Tx, T) error
	del    func(*sql.Tx, string) error
}

// skipIfCatalogsUntouched 在声明「目录未改动」的精确模式下把指纹表原地复用
// （零指纹计算、零 SQL），返回 true 表示该集合无需再 diff。只做别名、不改内容，
// 故无需登记 undo；失败时 next 会被整体丢弃。
func (h *dirtyHint) skipIfCatalogsUntouched(dst *map[string]string, prev map[string]string) bool {
	if h != nil && h.exact && h.catalogsUntouched {
		*dst = prev
		return true
	}
	return false
}

func diffStringKeyed[T any](tx *sql.Tx, d strKeyedDiff[T]) error {
	m := *d.dst
	seen := make(map[string]bool, len(d.rows))
	for i := range d.rows {
		r := d.rows[i]
		k := d.id(r)
		fp := fingerprint(r)
		m[k] = fp
		seen[k] = true
		if d.prev[k] == fp {
			continue
		}
		if err := d.upsert(tx, r); err != nil {
			return err
		}
	}
	for k := range d.prev {
		if seen[k] {
			continue
		}
		if err := d.del(tx, k); err != nil {
			return err
		}
	}
	return nil
}

// strKeyedExact 描述「字符串主键集合」的精确声明契约：m 必须是 persistedRows 里
// 该集合的指纹表（原地复用），只处理 rows（改/增）与 removed（删）。
type strKeyedExact[T any] struct {
	m        map[string]string
	rows     []T
	removed  []string
	id       func(T) string
	upsert   func(*sql.Tx, T) error
	del      func(*sql.Tx, string) error
	undoKind byte
}

// diffStringKeyedExact 是 diffStringKeyed 的精确版：只处理声明行，指纹表原地复用，
// 未声明的行既不算指纹也不产生 SQL，簿记 O(声明行)。镜像 diffContainersExact。
func diffStringKeyedExact[T any](tx *sql.Tx, hint *dirtyHint, d strKeyedExact[T]) error {
	for i := range d.rows {
		r := d.rows[i]
		k := d.id(r)
		fp := fingerprint(r)
		prev, known := d.m[k]
		if known && prev == fp {
			continue
		}
		hint.recordStr(d.undoKind, k, prev, known)
		d.m[k] = fp
		if err := d.upsert(tx, r); err != nil {
			return err
		}
	}
	for _, k := range d.removed {
		prev, known := d.m[k]
		if !known {
			continue
		}
		hint.recordStr(d.undoKind, k, prev, true)
		delete(d.m, k)
		if err := d.del(tx, k); err != nil {
			return err
		}
	}
	return nil
}

func diffSubUsers(tx *sql.Tx, next *rowFingerprints, hint *dirtyHint) error {
	if hint.declaresCatalogs() {
		next.subUsers = persistedRows.subUsers
		return diffStringKeyedExact(tx, hint, strKeyedExact[SubUser]{
			m:        persistedRows.subUsers,
			rows:     hint.set.SubUsers,
			removed:  hint.set.RemovedSubUsers,
			id:       func(su SubUser) string { return su.ID },
			upsert:   upsertSubUserRow,
			del:      deleteSubUserRow,
			undoKind: fpUndoSubUser,
		})
	}
	if hint.skipIfCatalogsUntouched(&next.subUsers, persistedRows.subUsers) {
		return nil
	}
	return diffStringKeyed(tx, strKeyedDiff[SubUser]{
		dst:    &next.subUsers,
		prev:   persistedRows.subUsers,
		rows:   AppConfig.SubUsers,
		id:     func(su SubUser) string { return su.ID },
		upsert: upsertSubUserRow,
		del:    deleteSubUserRow,
	})
}

func diffAPIKeys(tx *sql.Tx, next *rowFingerprints, hint *dirtyHint) error {
	delKey := func(tx *sql.Tx, id string) error {
		_, err := tx.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
		return err
	}
	if hint.declaresCatalogs() {
		next.apiKeys = persistedRows.apiKeys
		return diffStringKeyedExact(tx, hint, strKeyedExact[ApiKeyConfig]{
			m:        persistedRows.apiKeys,
			rows:     hint.set.APIKeys,
			removed:  hint.set.RemovedAPIKeys,
			id:       func(k ApiKeyConfig) string { return k.ID },
			upsert:   upsertAPIKeyRow,
			del:      delKey,
			undoKind: fpUndoAPIKey,
		})
	}
	if hint.skipIfCatalogsUntouched(&next.apiKeys, persistedRows.apiKeys) {
		return nil
	}
	return diffStringKeyed(tx, strKeyedDiff[ApiKeyConfig]{
		dst:    &next.apiKeys,
		prev:   persistedRows.apiKeys,
		rows:   AppConfig.ApiKeys,
		id:     func(k ApiKeyConfig) string { return k.ID },
		upsert: upsertAPIKeyRow,
		del:    delKey,
	})
}

func diffSnapshots(tx *sql.Tx, next *rowFingerprints, hint *dirtyHint) error {
	delSnapshot := func(tx *sql.Tx, id string) error {
		_, err := tx.Exec(`DELETE FROM snapshots WHERE id = ?`, id)
		return err
	}
	if hint.declaresCatalogs() {
		next.snapshots = persistedRows.snapshots
		return diffStringKeyedExact(tx, hint, strKeyedExact[Snapshot]{
			m:        persistedRows.snapshots,
			rows:     hint.set.Snapshots,
			removed:  hint.set.RemovedSnapshots,
			id:       func(s Snapshot) string { return s.ID },
			upsert:   upsertSnapshotRow,
			del:      delSnapshot,
			undoKind: fpUndoSnapshot,
		})
	}
	if hint.skipIfCatalogsUntouched(&next.snapshots, persistedRows.snapshots) {
		return nil
	}
	return diffStringKeyed(tx, strKeyedDiff[Snapshot]{
		dst:    &next.snapshots,
		prev:   persistedRows.snapshots,
		rows:   AppConfig.Snapshots,
		id:     func(s Snapshot) string { return s.ID },
		upsert: upsertSnapshotRow,
		del:    delSnapshot,
	})
}

func diffTasks(tx *sql.Tx, next *rowFingerprints, hint *dirtyHint) error {
	if hint.declaresCatalogs() {
		next.tasks = persistedRows.tasks
		return diffStringKeyedExact(tx, hint, strKeyedExact[SavedTask]{
			m:        persistedRows.tasks,
			rows:     hint.set.Tasks,
			removed:  hint.set.RemovedTasks,
			id:       func(task SavedTask) string { return task.ID },
			upsert:   upsertTaskRow,
			del:      deleteTaskRow,
			undoKind: fpUndoTask,
		})
	}
	if hint.skipIfCatalogsUntouched(&next.tasks, persistedRows.tasks) {
		return nil
	}
	return diffStringKeyed(tx, strKeyedDiff[SavedTask]{
		dst:    &next.tasks,
		prev:   persistedRows.tasks,
		rows:   AppConfig.Tasks,
		id:     func(task SavedTask) string { return task.ID },
		upsert: upsertTaskRow,
		del:    deleteTaskRow,
	})
}

// diffEnabledImages 是纯位置序列表（无业务主键），整表变小，按整片段指纹判定。
func diffEnabledImages(tx *sql.Tx, next *rowFingerprints) error {
	next.enabledImages = fingerprint(AppConfig.EnabledImages)
	if next.enabledImages == persistedRows.enabledImages {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM enabled_images`); err != nil {
		return err
	}
	return saveEnabledImages(tx)
}

// saveBoundedLogs 重写有界日志表（audit_logs ≤500 / login_logs ≤200 行）。
// 常规日志写入走 appendAuditLogRow / appendLoginLogRow 增量追加并各自裁剪，库已与
// 内存一致，故此函数只在 logsDirty 置位时被调用（追加落库失败的自愈 / 保留期裁剪），
// 行数有界，不构成写放大（#95）。
func saveBoundedLogs(tx *sql.Tx) error {
	for _, table := range []string{"audit_logs", "login_logs"} {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	if err := saveAuditLogs(tx); err != nil {
		return err
	}
	return saveLoginLogs(tx)
}
