package config

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"sync"
)

// ---- P1 指纹：按类型预编译的编码计划 ----
//
// 增量落库每次保存都要对每一行取指纹判断是否变化。最初的实现是 json.Marshal +
// FNV，实测单行 Container 约 9.5µs（1448 B/op、4 allocs/op）——通用反射、字符串
// 转义、大缓冲分配都在一起。30w 容器即约 2.9s，且该计算发生在 AppConfigMu 写
// 锁内，会把全部读请求一起阻塞。
//
// 这里换成「按类型预编译编码计划」：首次遇到某类型时用 reflect 把结构体字段
// 下标按 Kind 分组、把切片元素与 map 值编译成子树，之后每行只做 v.Field(idx) +
// 定长写入，没有 FieldByName、没有 json 转义、没有大缓冲分配。哈希用具体类型
// fpHasher 内联实现 FNV-1a（不用 hash.Hash64 接口）——否则每次 write 都要经接口
// 逃逸，单行 Container 会变成上百次堆分配。
//
// 完备性由 TestFingerprintCoversEveryField 兜底：它用反射逐个叶子字段改值并断言
// 指纹必然改变。因为计划与测试取自同一份 reflect 元数据，任何字段漏进计划都会
// 被该用例直接抓住 —— 新增字段不需要手工登记。

type fpNode struct {
	kind   reflect.Kind
	elem   *fpNode // Slice/Array/Ptr 的元素计划
	keyVal *[2]*fpNode
	opaque bool // 含非导出字段的结构体 / 未支持类型：退化为 %v 编码

	// 结构体字段下标，按 Kind 分组，避免每个字段都走一次 Kind 分支。
	strIdx    []int
	intIdx    []int
	uintIdx   []int
	floatIdx  []int
	boolIdx   []int
	subFields []fpField // 需要递归的子节点（结构体/切片/map/opaque）
}

type fpField struct {
	idx  int
	node *fpNode
}

var fpPlans sync.Map // reflect.Type -> *fpNode

func fpPlanFor(t reflect.Type) *fpNode {
	if p, ok := fpPlans.Load(t); ok {
		return p.(*fpNode)
	}
	p := buildFPPlan(t)
	fpPlans.Store(t, p)
	return p
}

func buildFPPlan(t reflect.Type) *fpNode {
	switch t.Kind() {
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).PkgPath != "" {
				// 非导出字段无法安全递归（子值的 Interface() 会 panic），
				// 整个结构体退化为 %v 编码：仍然完备，只是慢。
				return &fpNode{kind: reflect.Struct, opaque: true}
			}
		}
		n := &fpNode{kind: reflect.Struct}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			switch f.Type.Kind() {
			case reflect.String:
				n.strIdx = append(n.strIdx, i)
			case reflect.Bool:
				n.boolIdx = append(n.boolIdx, i)
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				n.intIdx = append(n.intIdx, i)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				n.uintIdx = append(n.uintIdx, i)
			case reflect.Float32, reflect.Float64:
				n.floatIdx = append(n.floatIdx, i)
			default:
				n.subFields = append(n.subFields, fpField{idx: i, node: fpPlanFor(f.Type)})
			}
		}
		return n
	case reflect.Slice, reflect.Array:
		return &fpNode{kind: t.Kind(), elem: fpPlanFor(t.Elem())}
	case reflect.Ptr:
		return &fpNode{kind: reflect.Ptr, elem: fpPlanFor(t.Elem())}
	case reflect.Map:
		kv := [2]*fpNode{fpPlanFor(t.Key()), fpPlanFor(t.Elem())}
		return &fpNode{kind: reflect.Map, keyVal: &kv}
	case reflect.String:
		return &fpNode{kind: reflect.String}
	case reflect.Bool:
		return &fpNode{kind: reflect.Bool}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return &fpNode{kind: reflect.Int}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &fpNode{kind: reflect.Uint}
	case reflect.Float32, reflect.Float64:
		return &fpNode{kind: reflect.Float64}
	default:
		return &fpNode{kind: t.Kind(), opaque: true}
	}
}

// fpHasher 是内联的 FNV-1a 64bit：具体类型、无接口、无堆分配。
type fpHasher struct{ sum uint64 }

const (
	fpFNVOffset = 14695981039346656037
	fpFNVPrime  = 1099511628211
)

func newFPHasher() fpHasher { return fpHasher{sum: fpFNVOffset} }

func (h *fpHasher) writeByte(b byte) {
	h.sum ^= uint64(b)
	h.sum *= fpFNVPrime
}

// writeUint64 按小端逐字节写入，保证定长整型的编码彼此无歧义。
func (h *fpHasher) writeUint64(u uint64) {
	for i := 0; i < 8; i++ {
		h.writeByte(byte(u))
		u >>= 8
	}
}

// writeString 写长度前缀 + 原始字节，长度前缀保证相邻字段不会产生歧义
// （如 ("a","bc") 与 ("ab","c") 必须编码不同）。
func (h *fpHasher) writeString(s string) {
	h.writeUint64(uint64(len(s)))
	for i := 0; i < len(s); i++ {
		h.writeByte(s[i])
	}
}

// writeMarker 写 1 字节标记，用于区分 nil / 非 nil、true / false 等分支。
func (h *fpHasher) writeMarker(m byte) { h.writeByte(m) }

const (
	fpMarkFalse byte = 0
	fpMarkTrue  byte = 1
	fpMarkNil   byte = 2
	fpMarkSome  byte = 3
)

// fingerprint 计算值的稳定指纹（FNV-1a 64bit，十六进制）。
func fingerprint(v any) string {
	h := newFPHasher()
	fpFeed(&h, fpPlanFor(reflect.TypeOf(v)), reflect.ValueOf(v))
	return strconv.FormatUint(h.sum, 16)
}

func fpFeed(h *fpHasher, n *fpNode, v reflect.Value) {
	if n == nil {
		return
	}
	switch n.kind {
	case reflect.Struct:
		if n.opaque {
			h.writeString(fmt.Sprintf("%v", v.Interface()))
			return
		}
		for _, i := range n.strIdx {
			h.writeString(v.Field(i).String())
		}
		for _, i := range n.intIdx {
			h.writeUint64(uint64(v.Field(i).Int()))
		}
		for _, i := range n.uintIdx {
			h.writeUint64(v.Field(i).Uint())
		}
		for _, i := range n.floatIdx {
			h.writeUint64(math.Float64bits(v.Field(i).Float()))
		}
		for _, i := range n.boolIdx {
			if v.Field(i).Bool() {
				h.writeMarker(fpMarkTrue)
			} else {
				h.writeMarker(fpMarkFalse)
			}
		}
		for _, f := range n.subFields {
			fpFeed(h, f.node, v.Field(f.idx))
		}
	case reflect.String:
		h.writeString(v.String())
	case reflect.Bool:
		if v.Bool() {
			h.writeMarker(fpMarkTrue)
		} else {
			h.writeMarker(fpMarkFalse)
		}
	case reflect.Int:
		h.writeUint64(uint64(v.Int()))
	case reflect.Uint:
		h.writeUint64(v.Uint())
	case reflect.Float64:
		h.writeUint64(math.Float64bits(v.Float()))
	case reflect.Slice, reflect.Array:
		l := v.Len()
		h.writeUint64(uint64(l))
		for i := 0; i < l; i++ {
			fpFeed(h, n.elem, v.Index(i))
		}
	case reflect.Map:
		keys := v.MapKeys()
		// 稳定顺序：按键的 %v 排序（map[string]X 即字典序）。map 通常很小（如 Tags）。
		sort.Slice(keys, func(i, j int) bool {
			return fmt.Sprint(keys[i].Interface()) < fmt.Sprint(keys[j].Interface())
		})
		h.writeUint64(uint64(len(keys)))
		for _, k := range keys {
			fpFeed(h, n.keyVal[0], k)
			fpFeed(h, n.keyVal[1], v.MapIndex(k))
		}
	case reflect.Ptr:
		if v.IsNil() {
			h.writeMarker(fpMarkNil)
			return
		}
		h.writeMarker(fpMarkSome)
		fpFeed(h, n.elem, v.Elem())
	default:
		// interface / struct{未导出} / 其它未支持类型：退化为 %v。
		h.writeString(fmt.Sprintf("%v", v.Interface()))
	}
}
