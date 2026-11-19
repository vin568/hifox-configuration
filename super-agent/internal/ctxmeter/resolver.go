package ctxmeter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Resolver 解析模型窗口（ADR 0030）。优先级：harness 自报（调用方不经过这里）> omp
// 本地模型目录 > 显式 context_windows 覆盖。没有 models.dev 网络请求：tick 热路径不能因为外网抖动而变慢。
type Resolver struct {
	Overrides map[string]int64
	DB        *ModelDB
}

// Window 先按 "provider/model" 再按 "model" 查覆盖，最后查本地模型目录。
func (r *Resolver) Window(ctx context.Context, provider, model string) (int64, bool) {
	if r == nil {
		return 0, false
	}
	if r.DB != nil {
		if w, ok := r.DB.Lookup(ctx, provider, model); ok {
			return w, true
		}
	}
	if w, ok := lookupWindowMap(r.Overrides, provider, model); ok {
		return w, true
	}
	return 0, false
}

func lookupWindowMap(m map[string]int64, provider, model string) (int64, bool) {
	if len(m) == 0 || model == "" {
		return 0, false
	}
	for _, key := range []string{provider + "/" + model, model} {
		if w := m[key]; w > 0 {
			return w, true
		}
	}
	return 0, false
}

// DefaultModelDBPath 是 omp 的模型目录。opencode 由它自己的 adapter 报告窗口，
// 不走这里；路径不存在时 Lookup 返回 false，不阻塞测量。
func DefaultModelDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".omp", "agent", "models.db")
}

// ModelDB 从 omp 的 SQLite 模型缓存里取 contextWindow。schema 是 omp 的内部实现，
// 隔离在这里：读不到就退化为 unknown，绝不让运行时崩。
type ModelDB struct {
	Path string
	// Run 是测试接缝；nil 时 exec sqlite3。
	Run func(ctx context.Context, args ...string) ([]byte, error)

	mu    sync.Mutex
	cache map[string]int64
}

// Lookup 按 provider + model 取窗口。provider 为空时按 hifox 处理（当前唯一部署实例）。
func (m *ModelDB) Lookup(ctx context.Context, provider, model string) (int64, bool) {
	if m == nil || model == "" {
		return 0, false
	}
	path := m.Path
	if path == "" {
		path = DefaultModelDBPath()
	}
	if path == "" {
		return 0, false
	}
	if provider == "" {
		provider = "hifox"
	}
	key := provider + "/" + model
	m.mu.Lock()
	if m.cache != nil {
		if w, ok := m.cache[key]; ok {
			m.mu.Unlock()
			return w, true
		}
	}
	m.mu.Unlock()

	if strings.ContainsAny(provider, "'\"\\\x00") {
		return 0, false
	}
	query := fmt.Sprintf(
		"select models from model_cache where provider_id like '%s:%%' order by updated_at desc limit 1",
		provider,
	)
	out, err := m.run(ctx, path, query)
	if err != nil {
		return 0, false
	}
	windows := parseModelRows(out, provider)
	m.mu.Lock()
	if m.cache == nil {
		m.cache = make(map[string]int64, len(windows))
	}
	for k, v := range windows {
		m.cache[k] = v
	}
	w, ok := m.cache[key]
	m.mu.Unlock()
	return w, ok
}

func (m *ModelDB) run(ctx context.Context, path string, args ...string) ([]byte, error) {
	if m.Run != nil {
		return m.Run(ctx, args...)
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	full := append([]string{path}, args...)
	cmd := exec.CommandContext(cctx, "sqlite3", full...)
	return cmd.Output()
}

type modelEntry struct {
	ID            string `json:"id"`
	Provider      string `json:"provider"`
	ContextWindow int64  `json:"contextWindow"`
}

// parseModelRows 解析 model_cache.models：每行一个 JSON 数组，同 id 可能有多个
// materialization 版本，任一条有 contextWindow 即可。
func parseModelRows(data []byte, provider string) map[string]int64 {
	out := map[string]int64{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rows []modelEntry
		if json.Unmarshal([]byte(line), &rows) != nil {
			continue
		}
		for _, e := range rows {
			if e.ContextWindow <= 0 || e.ID == "" {
				continue
			}
			p := e.Provider
			if p == "" {
				p = provider
			}
			if p != provider {
				continue
			}
			out[p+"/"+e.ID] = e.ContextWindow
		}
	}
	return out
}
