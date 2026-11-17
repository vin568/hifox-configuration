package feishu

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func newTestDedup(t *testing.T) *dedupStore {
	t.Helper()
	s, err := newDedupStore(filepath.Join(t.TempDir(), "dedup.jsonl"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.close() })
	return s
}

func TestDedupClaimDoneRoundtrip(t *testing.T) {
	s := newTestDedup(t)
	if !s.claim("m1") {
		t.Fatal("first claim should be admitted")
	}
	if s.claim("m1") {
		t.Fatal("redelivery before done should be refused")
	}
	s.markDone("m1")
	if s.claim("m1") {
		t.Fatal("redelivery after done should be refused")
	}
}

func TestDedupPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dedup.jsonl")
	s, err := newDedupStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s.claim("m1")
	s.markDone("m1")
	s.claim("m2") // claimed but never done
	s.close()

	s2, err := newDedupStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.close()
	if s2.claim("m1") {
		t.Fatal("done message must stay deduplicated after reopen")
	}
	if s2.claim("m2") {
		t.Fatal("fresh unfinished claim must still be refused within the expiry window")
	}
}

func TestDedupStaleClaimReprocessed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dedup.jsonl")
	// Pre-seed a claim older than the expiry window.
	stale := time.Now().Add(-2 * time.Minute).Unix()
	line := `{"message_id":"m_stale","ts":` + strconv.FormatInt(stale, 10) + `,"done":false}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := newDedupStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if !s.claim("m_stale") {
		t.Fatal("claim older than expiry with no done must be reprocessable")
	}
}

// done 记录永不因过期而豁免：飞书可能重投数天前的事件，过期 done 若被
// 视作可重处理，旧消息会重复注入。预置一条超期 done，重开后必须仍被拒。
func TestDedupDoneNeverExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dedup.jsonl")
	stale := time.Now().Add(-2 * time.Minute).Unix()
	line := `{"message_id":"m_old_done","ts":` + strconv.FormatInt(stale, 10) + `,"done":true}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := newDedupStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if s.claim("m_old_done") {
		t.Fatal("done must never expire into reprocessability")
	}
}

// last-record-wins：同 id 先一条超期 claim、后一条新鲜 claim，重开后新鲜
// claim 生效（first-wins 或 stale-drop 实现都会错放行）。
func TestDedupLastRecordWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dedup.jsonl")
	stale := time.Now().Add(-2 * time.Minute).Unix()
	fresh := time.Now().Unix()
	lines := `{"message_id":"m","ts":` + strconv.FormatInt(stale, 10) + `,"done":false}` + "\n" +
		`{"message_id":"m","ts":` + strconv.FormatInt(fresh, 10) + `,"done":false}` + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := newDedupStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if s.claim("m") {
		t.Fatal("fresh claim (last record) must win over the stale one")
	}
}

// claim 的持久化失败必须拒收：journal 是 claim 的持久层，写不进去就放行
// 等于崩溃后重投重处理——dedup 的存在意义被架空。
func TestDedupClaimAppendFailureRefuses(t *testing.T) {
	s := newTestDedup(t)
	s.close() // 关掉 journal 句柄，append 必失败
	if s.claim("m_write_fail") {
		t.Fatal("claim must be refused when the journal append fails")
	}
}

// markDone 必须真的把 done 记录写进 journal：claim 对未完成的 claim 也返回
// false，只有重开后看 state.Done 才能区分「写了 done」与「什么也没写」
// （把 markDone 改成 no-op 时，本用例是唯一会红的）。
func TestDedupMarkDonePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dedup.jsonl")
	s, err := newDedupStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !s.claim("m_done") {
		t.Fatal("claim should be admitted")
	}
	s.markDone("m_done")
	s.close()

	s2, err := newDedupStore(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.close()
	if rec, ok := s2.state["m_done"]; !ok || !rec.Done {
		t.Fatalf("markDone must persist a done record, state=%+v", s2.state)
	}
}
