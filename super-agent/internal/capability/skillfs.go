package capability

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SkillRoots 返回要给底层 CLI 的技能发现根（绝对路径，去重、保序）：
// 共享技能目录在前，agent 专属在后。
//
// 直接把真源目录交给 CLI 的 skills.customDirectories，不复制：SKILL.md 已是标准形态，
// 复制只会造出第二份可编辑的副本（ADR 0025 修订）。
func SkillRoots(repoDir string, a Agent) []string {
	root := filepath.Join(repoDir, "super-agent", "capabilities")
	shared := filepath.Join(root, "skills")
	own := filepath.Join(root, "agents", a.Key, "skills")
	var out []string
	if hasSkillDirs(shared) {
		out = append(out, shared)
	}
	if hasSkillDirs(own) {
		out = append(out, own)
	}
	return out
}

// hasSkillDirs 判定目录下是否有至少一个 SKILL.md（空目录不值得放进发现面）。
func hasSkillDirs(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "SKILL.md")); err == nil {
			return true
		}
	}
	return false
}

// WriteOverlay 写一份只含技能发现根的 omp 覆盖配置，返回其路径。
//
// 用覆盖文件而不是改用户全局配置：运行时物料不该污染开发者的 ~/.omp；
// 多根是必须的——共享与专属是两组目录，复制合并会重新造出副本。
func WriteOverlay(outDir string, roots []string) (string, error) {
	// 空 roots 是调用方的错（skillArgs 在空时不追加 --config），写一份「指向零个目录」
	// 的覆盖配置只会让人以为技能挂上了却什么也没挂。
	if len(roots) == 0 {
		return "", errors.New("技能覆盖配置需要至少一个发现根")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(outDir, "omp-skills.yml")
	var b strings.Builder
	b.WriteString("skills:\n  customDirectories:\n")
	for _, r := range roots {
		fmt.Fprintf(&b, "    - %s\n", yamlScalar(r))
	}
	// ensureTopo 在每次注入前都跑：内容没变就别动文件（mtime 抖动、无关的
	// 变更检测噪音都没必要）。
	if cur, err := os.ReadFile(path); err == nil && string(cur) == b.String() {
		return path, nil
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// yamlScalar 把一个可能含冒号/引号/换行的字符串写成安全的单行 YAML 标量。
func yamlScalar(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return `""`
	}
	needsQuote := strings.ContainsAny(s, ":#'\"[]{}&*!|>%@`,?") ||
		strings.HasPrefix(s, " ") || strings.HasPrefix(s, "- ")
	if !needsQuote {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
