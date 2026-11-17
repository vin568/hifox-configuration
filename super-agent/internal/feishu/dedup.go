package feishu

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// dedupLine is one journal record. A claim marks a message as being processed;
// done marks it finished. A claim without a done line is reprocessable once it
// has aged past expire (crash between claim and done).
type dedupLine struct {
	MessageID string `json:"message_id"`
	TS        int64  `json:"ts"`
	Done      bool   `json:"done"`
}

// dedupStore is the two-phase on-disk dedup journal. It is append-only: Feishu
// may redeliver an event for days, so the journal is never truncated here
// (operational rotation can compact it offline).
type dedupStore struct {
	mu     sync.Mutex
	path   string
	expire time.Duration
	state  map[string]dedupLine // message_id -> latest record
	file   *os.File
}

func newDedupStore(path string, expire time.Duration) (*dedupStore, error) {
	if expire <= 0 {
		expire = DefaultDedupExpire
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	s := &dedupStore{path: path, expire: expire, state: map[string]dedupLine{}}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	s.file = f
	if err := s.load(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

// load replays the journal. The last record per message wins; claims older
// than expire without a done are treated as reprocessable (dropped from state)
// so the redelivery after a crash is admitted instead of silently swallowed.
func (s *dedupStore) load() error {
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var l dedupLine
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.MessageID == "" {
			continue
		}
		if !l.Done && time.Since(time.Unix(l.TS, 0)) > s.expire {
			continue // stale claim: reprocess allowed
		}
		s.state[l.MessageID] = l
	}
	return sc.Err()
}

// claim reports whether the caller may process messageID, and journals the
// claim so a concurrent or later redelivery is refused.
func (s *dedupStore) claim(messageID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, seen := s.state[messageID]; seen {
		return false
	}
	rec := dedupLine{MessageID: messageID, TS: time.Now().Unix()}
	if err := s.append(rec); err != nil {
		// The journal is the durability of the claim; without it a crash would
		// reprocess the message, which is the failure mode dedup exists to
		// prevent — refuse instead of proceeding unjournaled.
		slog.Error("feishu dedup: cannot append claim", "error", err)
		return false
	}
	s.state[messageID] = rec
	return true
}

// markDone journals the successful completion of a claimed message.
func (s *dedupStore) markDone(messageID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.state[messageID]
	if !ok || rec.Done {
		return
	}
	rec.Done = true
	if err := s.append(rec); err != nil {
		// The in-memory state already blocks this process; only a restart loses.
		slog.Error("feishu dedup: cannot append done", "error", err)
		return
	}
	s.state[messageID] = rec
}

func (s *dedupStore) append(l dedupLine) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	_, err = s.file.Write(append(b, '\n'))
	return err
}

func (s *dedupStore) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Close()
}
