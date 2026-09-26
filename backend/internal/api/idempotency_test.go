package api

import "testing"

func TestContainerIdempotencyLifecycle(t *testing.T) {
	const key = "container-create-42"
	const name = "host-42"

	// 未见过的键不应命中
	if _, ok := containerIdemLookup(key); ok {
		t.Fatalf("unexpected idempotency hit for fresh key")
	}

	// 记录后应命中并返回对应容器名
	containerIdemStore(key, name)
	if got, ok := containerIdemLookup(key); !ok || got != name {
		t.Fatalf("expected idempotency hit with name %q, got ok=%v name=%q", name, ok, got)
	}

	// 记录中的容器已删除后，可清理旧记录，下次命中失败
	containerIdemRemove(key)
	if _, ok := containerIdemLookup(key); ok {
		t.Fatalf("idempotency entry should be removed")
	}
}

func TestNormalizeIdempotencyKey(t *testing.T) {
	if normalizeIdempotencyKey("  abc  ") != "abc" {
		t.Fatalf("expected trimmed key")
	}
	if normalizeIdempotencyKey("") != "" {
		t.Fatalf("expected empty key stays empty")
	}
}