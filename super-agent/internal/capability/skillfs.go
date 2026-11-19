package capability

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillMount 是一个 agent 的技能发现面：发现根 + 要排除的共享技能名。
//
// customDirectories 是目录粒度：共享根一旦挂上，目录里的每个技能都会被底层发现。
// omp 的 ignoredSkills 按名字过滤，正好用来把「放在共享根、但没声明给这个 agent」
// 的技能挡在发现面外。不用 includeSkills：它是全局允许清单，会把用户/项目技能
// 一起挡掉；这里只想控制本仓共享根的分发范围。
type SkillMount struct {
	Roots   []string
	Ignored []string
}

// SkillMounts 返回一个 agent 的发现根与要排除的共享技能名：
// 共享根在前，agent 专属在后；专属技能自动挂载，不需要 skills 声明。
//
// 共享 = 存放位置，不是分发对象：capabilities/skills/<key> 里的技能只有在该 agent
// 的 skills.yaml 里声明了才可见（ADR 0029）。
func SkillMounts(repoDir string, a Agent) SkillMount {
	root := filepath.Join(repoDir, "super-agent", "capabilities")
	sharedRoot := filepath.Join(root, "skills")
	ownRoot := filepath.Join(root, "agents", a.Key, "skills")

	sharedKeys := skillDirKeys(sharedRoot)
	declared := make(map[string]bool, len(a.Skills))
	for _, k := range a.Skills {
		declared[k] = true
	}
	var mount SkillMount
	// 只挂「至少声明了一个」的共享根：一个都没声明就根本不进发现面，连
	// ignoredSkills 都不用写（也避免按名字误伤用户自己的同名技能）。
	hasDeclared := false
	for _, k := range sharedKeys {
		if declared[k] {
			hasDeclared = true
			break
		}
	}
	if hasDeclared {
		mount.Roots = append(mount.Roots, sharedRoot)
		for _, k := range sharedKeys {
			if !declared[k] {
				mount.Ignored = append(mount.Ignored, k)
			}
		}
	}
	if len(skillDirKeys(ownRoot)) > 0 {
		mount.Roots = append(mount.Roots, ownRoot)
	}
	return mount
}

// skillDirKeys 列出技能根下带 SKILL.md 的目录（按名字排序，输出稳定）。
// 空目录/没有 SKILL.md 的目录不进发现面：底层白扫一次，也掩盖「技能没写对」。
func skillDirKeys(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var keys []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "SKILL.md")); err == nil {
			keys = append(keys, e.Name())
		}
	}
	sort.Strings(keys)
	return keys
}

// WriteOverlay 写一份只含技能发现面的 omp 覆盖配置，返回其路径。
//
// 用覆盖文件而不是改用户全局配置：运行时物料不该污染开发者的 ~/.omp；
// 多根是必须的——共享与专属是两组目录，复制合并会重新造出副本。
func WriteOverlay(outDir string, m SkillMount) (string, error) {
	// 空 roots 是调用方的错（skillArgs 在空时不追加 --config），写一份「指向零个目录」
	// 的覆盖配置只会让人以为技能挂上了却什么也没挂。
	if len(m.Roots) == 0 {
		return "", errors.New("技能覆盖配置需要至少一个发现根")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(outDir, "omp-skills.yml")
	var b strings.Builder
	b.WriteString("skills:\n  customDirectories:\n")
	for _, r := range m.Roots {
		fmt.Fprintf(&b, "    - %s\n", yamlScalar(r))
	}
	if len(m.Ignored) > 0 {
		b.WriteString("  ignoredSkills:\n")
		for _, k := range m.Ignored {
			fmt.Fprintf(&b, "    - %s\n", yamlScalar(k))
		}
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
