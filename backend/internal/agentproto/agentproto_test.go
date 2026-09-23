package agentproto

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseRegistrationRequestValid(t *testing.T) {
	raw := []byte(`{
		"protocol":"v2",
		"node_id":"n1","token":"t",
		"name":"node1","address":"http://1.2.3.4:8999",
		"version":"v1.1.37",
		"capability":{"drivers":["dir","zfs"],"arch":"amd64","os_name":"linux","cpu_count":4,"ram_total_mb":8192,"disk_total_gb":100},
		"last_command_id":5
	}`)
	r, err := ParseRegistrationRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.NodeID != "n1" || r.Token != "t" || r.LastCommandID != 5 {
		t.Fatalf("parse = %+v", r)
	}
	if len(r.Capability.Drivers) != 2 {
		t.Fatalf("drivers = %v", r.Capability.Drivers)
	}
}

func TestParseRegistrationRequestMissingNodeID(t *testing.T) {
	raw := []byte(`{"protocol":"v2","token":"t"}`)
	if _, err := ParseRegistrationRequest(raw); err == nil {
		t.Fatal("expected error for missing node_id")
	}
}

func TestParseRegistrationRequestRejectsV1(t *testing.T) {
	raw := []byte(`{"protocol":"v1","node_id":"n1","token":"t"}`)
	if _, err := ParseRegistrationRequest(raw); err != ErrUnsupportedProtocol {
		t.Fatalf("expected ErrUnsupportedProtocol, got %v", err)
	}
}

func TestComputeIdempotencyKeyStable(t *testing.T) {
	cmd := Command{ID: 1, Type: CmdContainerStart, Target: "7"}
	cmd.Payload = []byte(`{"x":1}`)
	key1 := ComputeIdempotencyKey(cmd)
	// 重算必须一致。
	key2 := ComputeIdempotencyKey(cmd)
	if key1 != key2 {
		t.Fatalf("idempotency key not stable: %s vs %s", key1, key2)
	}
	if len(key1) != 32 {
		t.Fatalf("key length = %d, want 32", len(key1))
	}
	// 不同 payload 必须不同。
	cmd.Payload = []byte(`{"x":2}`)
	if ComputeIdempotencyKey(cmd) == key1 {
		t.Fatal("different payload produced same key")
	}
}

func TestChannelEnqueuePollAndComplete(t *testing.T) {
	ch := NewChannel()
	payload := []byte(`{"image":"deb"}`)
	cmd1 := ch.Enqueue(CmdContainerCreate, "", payload)
	cmd2 := ch.Enqueue(CmdContainerStart, "5", nil)
	cmd3 := ch.Enqueue(CmdContainerStop, "5", nil)

	if cmd1.ID != 1 || cmd2.ID != 2 || cmd3.ID != 3 {
		t.Fatalf("expected monotonic IDs 1,2,3, got %d %d %d", cmd1.ID, cmd2.ID, cmd3.ID)
	}

	// Poll from 0: returns all three (LatestID=3).
	resp := ch.Poll(0)
	if len(resp.Commands) != 3 || resp.LatestID != 3 {
		t.Fatalf("Poll(0) = %+v", resp)
	}
	// Poll from 1: returns 2 and 3.
	resp = ch.Poll(1)
	if len(resp.Commands) != 2 {
		t.Fatalf("Poll(1) count = %d, want 2", len(resp.Commands))
	}
	if resp.Commands[0].ID != 2 || resp.Commands[1].ID != 3 {
		t.Fatalf("Poll(1) ids = %v", []int64{resp.Commands[0].ID, resp.Commands[1].ID})
	}

	ch.MarkCompleted(cmd1.ID, cmd1.IdempotencyKey)
	if !ch.IsCompleted(cmd1.IdempotencyKey) {
		t.Fatal("MarkCompleted must set idempotency flag")
	}
	resp = ch.Poll(0)
	if len(resp.Commands) != 2 {
		t.Fatalf("after MarkCompleted Poll(0) = %d, want 2", len(resp.Commands))
	}
}

func TestChannelReconnectCatchupNoDuplicateNoLoss(t *testing.T) {
	// 模拟 P1-3 验收："断网 5 分钟恢复后无指令丢失、无重复"。
	ch := NewChannel()
	// 控制面下发 3 条指令。
	ch.Enqueue(CmdContainerStart, "5", nil)
	ch.Enqueue(CmdContainerStop, "5", nil)
	first := ch.Enqueue(CmdContainerRestart, "5", nil)
	// Agent 拉取并完成 a+b，cursor 推进到 2。
	resp := ch.Poll(0)
	ch.MarkCompleted(resp.Commands[0].ID, resp.Commands[0].IdempotencyKey)
	ch.MarkCompleted(resp.Commands[1].ID, resp.Commands[1].IdempotencyKey)
	// "断网"：此期间控制面再下发 2 条。
	d := ch.Enqueue(CmdContainerStart, "7", nil)
	e := ch.Enqueue(CmdContainerStop, "7", nil)
	// Agent 重连后从 cursor=2 拉取：应得到 c、d、e，不丢不重。
	resp = ch.Poll(2)
	gotIDs := []int64{}
	for _, c := range resp.Commands {
		gotIDs = append(gotIDs, c.ID)
	}
	wantIDs := []int64{first.ID, d.ID, e.ID}
	if len(gotIDs) != 3 || gotIDs[0] != wantIDs[0] || gotIDs[1] != wantIDs[1] || gotIDs[2] != wantIDs[2] {
		t.Fatalf("reconnect Poll = %v, want %v", gotIDs, wantIDs)
	}
}

func TestChannelDuplicateCommandIdempotent(t *testing.T) {
	ch := NewChannel()
	cmd := ch.Enqueue(CmdNoop, "x", []byte(`{}`))
	ch.MarkCompleted(cmd.ID, cmd.IdempotencyKey)
	// 同 IdempotencyKey 不可重复派发（Channel 设计：MarkCompleted 从 pending 删
	// 除，但 Poll 不检查 complete map；上层 Agent 按 IsCompleted 自判）。
	// Agent 重发 cmd 的 IdempotencyKey 时，本地 IsCompleted=true → 跳过。
	if !ch.IsCompleted(cmd.IdempotencyKey) {
		t.Fatal("IsCompleted must report true after MarkCompleted")
	}
}

func TestFormatRegistrationResponseRoundtrip(t *testing.T) {
	r := RegistrationResponse{Accepted: true, NextHeartbeat: 10 * time.Second}
	data, err := FormatRegistrationResponse(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"accepted":true`)) {
		t.Fatalf("output missing accepted: %s", data)
	}
}

func TestFormatCommandReceiptRoundtrip(t *testing.T) {
	rec := CommandReceipt{ID: 1, Status: "completed", Progress: 100}
	data, err := FormatCommandReceipt(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"id":1`)) {
		t.Fatalf("output missing id: %s", data)
	}
}

func TestNextIDIsMonotonic(t *testing.T) {
	ch := NewChannel()
	for i := int64(1); i <= 5; i++ {
		if ch.NextID() != i {
			t.Fatalf("NextID = %d, want %d", ch.NextID(), i)
		}
		ch.Enqueue(CmdNoop, "", nil)
	}
}

// 简易并发：100 goroutine 同时 Poll + Enqueue，确认无 panic + 数据不损坏。
func TestChannelConcurrentSafety(t *testing.T) {
	ch := NewChannel()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			ch.Enqueue(CmdNoop, "x", nil)
		}()
		go func() {
			defer wg.Done()
			_ = ch.Poll(0)
		}()
	}
	wg.Wait()
	if ch.NextID() < 51 {
		t.Fatalf("expected >=51 enqueued, got NextID=%d", ch.NextID())
	}
}

// 防止 import 折叠。
var _ = strings.TrimSpace
var _ = json.RawMessage(nil)