// Package capability 读取本仓的本地能力声明目录（super-agent/capabilities/），
// 生成主控每轮都能读的紧凑索引，并提供漂移检查。
//
// 目录即声明：
//
//	agents/<key>/{capability.yaml,instructions.md}
//	agents/<key>/skills/<key>/SKILL.md    （该 agent 专属的技能）
//	skills/<key>/SKILL.md                 （多个 agent 共享的技能）
//	scripts/<key>.yaml
//	platforms/<platform>/<key>.yaml
//
// 平台能力的真源仍是 config/（config/hifox/agents/<key>、config/apifox/agents/<key>）；
// platforms/<平台>/ 条目只写指针，Check 校验指针在 config/ 里确实存在。
package capability

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"super-agent/internal/hifox"
)

// ReportSkill 是回报主控通道的共享技能名：每个 role=job 的本地能力都必须声明它，
// 否则被派出去之后没有「怎么把卡点与关键结果推回主控」的纪律（ADR 0029）。
const ReportSkill = "agent-report"

// Agent 是 <workspace> 里一个常驻或按需拉起的能力 agent。
type Agent struct {
	Key           string   `yaml:"key"`
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	Role          string   `yaml:"role"`      // master | retro | job
	Workspace     string   `yaml:"workspace"` // master | crew | jobs
	Kind          string   `yaml:"kind"`
	Args          []string `yaml:"args"`
	MaxConcurrent int      `yaml:"maxConcurrent"`
	// Skills 是显式声明的共享技能（capabilities/skills/<key>），来自同目录的
	// skills.yaml，不是 capability.yaml 的字段。共享根里的技能默认不挂给任何
	// agent：不声明就看不见——「共享」是存放位置，不是分发对象。agent 专属技能
	// （agents/<key>/skills/）不需要声明，自动挂载。
	//
	// 单独成文件是为了滚动升级：capability.yaml 按 KnownFields 解析，旧版运行时
	// 遇到新字段会整份解析失败；新文件旧版根本不读，additive 变更不打断在跑的进程。
	Skills       []string `yaml:"-"`
	Instructions string   `yaml:"instructions"` // 解析后的绝对路径
}

// Skill 是一份可加载的技能：SKILL.md 是唯一载体（Agent Skills 开放标准形态，
// frontmatter 的 name/description 由模型用来决定要不要读正文），没有第二份元数据文件——
// 同一份目录可以原样搬进 config/ 交给 x-deploy 部署。
//
// Owner 非空表示这是某个 agent 专属的技能（agents/<owner>/skills/<key>/）；空表示共享。
type Skill struct {
	Key   string
	Owner string
	// frontmatter 里的两个字段（读出来是为了索引与 --check，不改写正文）。
	Name        string
	Description string
}

// Script 是直连白名单里的一个可执行脚本。
type Script struct {
	Key         string `yaml:"key"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Path        string `yaml:"path"` // 相对 repoDir 或绝对
}

// PlatformAgent 是某个平台（CLI 应用实例，如 hifox；将来 sharkly）上一个 agent 的
// 指针，真源在 config/<配置集合>/。
type PlatformAgent struct {
	Platform string `yaml:"-"` // 目录名 platforms/<platform>/，以目录为准
	Key      string `yaml:"key"`
	Name     string `yaml:"name"`
	Space    string `yaml:"space"`
	Org      string `yaml:"org"` // 平台组织完整 UUID（slug 会被平台判 404）；空 = 用 CLI 当前组织
	Note     string `yaml:"note"`
}

// Registry 是一次加载得到的全部声明，切片按 key 排序（平台能力先按平台、再按 key）。
type Registry struct {
	Root      string
	Agents    []Agent
	Skills    []Skill
	Scripts   []Script
	Platforms []PlatformAgent
}

// Load 读 <repoDir>/super-agent/capabilities。声明内容缺失或解析失败是硬错误；
// key 与目录名不符这类漂移留给 Check 报告，不阻断主控拿索引。
func Load(repoDir string) (*Registry, error) {
	root, err := filepath.Abs(filepath.Join(repoDir, "super-agent", "capabilities"))
	if err != nil {
		return nil, err
	}
	reg, fatal, _ := scan(root)
	if len(fatal) > 0 {
		return nil, errors.Join(fatal...)
	}
	return reg, nil
}

// Master 返回 role: master 的 agent。
func (r *Registry) Master() (Agent, bool) {
	for _, a := range r.Agents {
		if a.Role == "master" {
			return a, true
		}
	}
	return Agent{}, false
}

// Retro 返回 role: retro 的 agent。
func (r *Registry) Retro() (Agent, bool) {
	for _, a := range r.Agents {
		if a.Role == "retro" {
			return a, true
		}
	}
	return Agent{}, false
}

// Index 生成主控读的能力索引：四段，一项一行，只给名字、归属与一句话（不给文档路径）。
func (r *Registry) Index() string {
	var b strings.Builder

	b.WriteString("## 平台能力\n\n")
	if len(r.Platforms) == 0 {
		b.WriteString("（无）\n")
	}
	for _, c := range r.Platforms {
		fmt.Fprintf(&b, "- [%s] %s %s（%s）：%s\n", c.Platform, c.Key, c.Name, c.Space, c.Note)
	}

	b.WriteString("\n## 本地能力\n\n")
	if len(r.Agents) == 0 {
		b.WriteString("（无）\n")
	}
	for _, a := range r.Agents {
		fmt.Fprintf(&b, "- %s %s（%s/%s，kind %s）：%s\n", a.Key, a.Name, a.Role, a.Workspace, a.Kind, a.Description)
	}

	b.WriteString("\n## 技能（已注册为 skill，按需加载）\n\n")
	if len(r.Skills) == 0 {
		b.WriteString("（无）\n")
	}
	assigned := map[string][]string{}
	for _, a := range r.Agents {
		for _, key := range a.Skills {
			assigned[key] = append(assigned[key], a.Key)
		}
	}
	for _, s := range r.Skills {
		scope := "共享"
		if s.Owner != "" {
			scope = s.Owner + " 专属"
		} else if names := assigned[orKey(s.Name, s.Key)]; len(names) > 0 {
			// 共享技能不是天然对所有 agent 可见：挂载范围以 skills.yaml 的声明
			// 为准，索引把它写出来，避免「共享」被读成「人人都有」。
			scope = "共享 → " + strings.Join(names, "、")
		} else {
			scope = "共享 → 未指派"
		}
		// 打印 frontmatter 的 name（底层按它注册）；--check 强制 name == 目录 key，
		// 不一致会直接报漂移，所以索引名字与加载名字始终是同一个。
		fmt.Fprintf(&b, "- %s（%s）：%s\n", orKey(s.Name, s.Key), scope, oneLine(s.Description))
	}

	b.WriteString("\n## 直连脚本\n\n")
	if len(r.Scripts) == 0 {
		b.WriteString("（无）\n")
	}
	for _, s := range r.Scripts {
		fmt.Fprintf(&b, "- %s %s：%s（%s）\n", s.Key, s.Name, s.Description, s.Path)
	}

	return b.String()
}

// PlatformByKey 按平台 + key 取平台能力指针条目。
func (r *Registry) PlatformByKey(platform, key string) (PlatformAgent, bool) {
	for _, c := range r.Platforms {
		if c.Platform == platform && c.Key == key {
			return c, true
		}
	}
	return PlatformAgent{}, false
}

// AgentByKey 按 key 取 agent 条目。
func (r *Registry) AgentByKey(key string) (Agent, bool) {
	for _, a := range r.Agents {
		if a.Key == key {
			return a, true
		}
	}
	return Agent{}, false
}

// Check 重新扫一遍目录并核对漂移，返回直接给人读的问题列表；空表示一致。
func (r *Registry) Check(repoDir string) []string {
	root := r.Root
	if root == "" {
		root = filepath.Join(repoDir, "super-agent", "capabilities")
	}
	reg, fatal, problems := scan(root)

	out := make([]string, 0, len(fatal)+len(problems)+len(reg.Agents)+len(reg.Scripts)+len(reg.Platforms))
	for _, err := range fatal {
		out = append(out, err.Error())
	}
	out = append(out, problems...)

	var masters, retros []string
	for _, a := range reg.Agents {
		switch a.Role {
		case "master":
			masters = append(masters, a.Key)
		case "retro":
			retros = append(retros, a.Key)
		}
		if _, err := os.Stat(a.Instructions); err != nil {
			out = append(out, fmt.Sprintf("agents/%s：说明文档 %s 不存在", a.Key, a.Instructions))
		}
	}
	switch len(masters) {
	case 1:
	case 0:
		out = append(out, "agents：缺少 role: master 的 agent，必须恰好一条")
	default:
		out = append(out, fmt.Sprintf("agents：role: master 必须恰好一条，当前 %d 条：%s", len(masters), strings.Join(masters, ", ")))
	}
	if len(retros) > 1 {
		out = append(out, fmt.Sprintf("agents：role: retro 最多一条，当前 %d 条：%s", len(retros), strings.Join(retros, ", ")))
	}

	// 技能完整性：frontmatter 缺 name/description 时底层 CLI 会整份跳过——那等于技能
	// 装而不用，且没有任何报错。在这里拦下，是唯一能看见它的地方。
	for _, s := range reg.Skills {
		label := skillLabel(s)
		if s.Name == "" {
			out = append(out, fmt.Sprintf("%s：SKILL.md 的 frontmatter 缺 name", label))
		}
		if s.Description == "" {
			out = append(out, fmt.Sprintf("%s：SKILL.md 的 frontmatter 缺 description（模型靠它判断要不要加载）", label))
		}
		// 底层只认 kebab-case：别的写法它照收但加载失败，我们这边先看见。
		if s.Name != "" && !isKebabCase(s.Name) {
			out = append(out, fmt.Sprintf("%s：技能名 %q 不是 kebab-case（底层只认小写字母/数字/连字符）", label, s.Name))
		}
		// 索引与 omp 都按 frontmatter name；目录 key 只是位置身份，要求两者相等，
		// 让「名字」只有一个真源（否则换目录后就地产生第二套名字）。位置即身份。
		if s.Name != "" && s.Name != s.Key {
			out = append(out, fmt.Sprintf("%s：SKILL.md 的 name %q 与目录名 %q 不一致（位置即身份：目录名必须等于技能名）", label, s.Name, s.Key))
		}
	}
	// 同名技能不能在两处声明：底层按 name 注册，重名只有一个能被裸名加载。
	seen := map[string]string{}
	for _, s := range reg.Skills {
		name := orKey(s.Name, s.Key)
		if prev, ok := seen[name]; ok {
			out = append(out, fmt.Sprintf("%s 与 %s 的技能名都是 %q：底层按 name 注册，重名会被加前缀，裸名只剩一个", prev, skillLabel(s), name))
			continue
		}
		seen[name] = skillLabel(s)
	}

	// 共享技能的挂载范围由 agent 的 skills 声明决定（不是声明了就一定存在，也不是
	// 存在了就人人都有）。两条都核对：声明必须命中共享根；job agent 必须声明回报通道。
	shared := map[string]bool{}
	for _, s := range reg.Skills {
		if s.Owner == "" {
			shared[orKey(s.Name, s.Key)] = true
		}
	}
	for _, a := range reg.Agents {
		declared := map[string]bool{}
		for _, key := range a.Skills {
			if declared[key] {
				out = append(out, fmt.Sprintf("agents/%s：共享技能 %q 重复声明", a.Key, key))
				continue
			}
			declared[key] = true
			if !shared[key] {
				out = append(out, fmt.Sprintf("agents/%s：声明的共享技能 %q 不存在（应放在 skills/%s/SKILL.md）", a.Key, key, key))
			}
		}
		if a.Role == "job" && !declared[ReportSkill] {
			out = append(out, fmt.Sprintf("agents/%s：role: job 必须声明共享技能 %q（被派出去后要有回报主控的纪律，见 ADR 0029）", a.Key, ReportSkill))
		}
	}

	for _, s := range reg.Scripts {
		p := s.Path
		if p == "" {
			out = append(out, fmt.Sprintf("scripts/%s.yaml：缺少 path", s.Key))
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(repoDir, p)
		}
		fi, err := os.Stat(p)
		if err != nil {
			out = append(out, fmt.Sprintf("scripts/%s.yaml：脚本 %s 不存在", s.Key, p))
			continue
		}
		if fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
			out = append(out, fmt.Sprintf("scripts/%s.yaml：脚本 %s 不可执行", s.Key, p))
		}
	}

	// 脚本能力的「登记才可见」在这里有执行面：direct/ 里有、声明里没有的脚本报漂移。
	// 白名单本身仍是提示词纪律（ADR 0019），这里只保证「登记表不落后于目录」。
	declared := make(map[string]bool, len(reg.Scripts))
	for _, s := range reg.Scripts {
		if s.Path != "" {
			declared[filepath.Base(s.Path)] = true
		}
	}
	if entries, err := os.ReadDir(filepath.Join(repoDir, "super-agent", "direct")); err == nil {
		for _, e := range entries {
			if e.IsDir() || strings.EqualFold(e.Name(), "README.md") {
				continue
			}
			if !declared[e.Name()] {
				stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
				out = append(out, fmt.Sprintf("direct/%s：脚本未在 capabilities/scripts/%s.yaml 登记（主控按登记表才看得见它）", e.Name(), stem))
			}
		}
	}

	// 平台归属：environments 声明「哪个平台覆盖哪个配置集合」（cli.app + configSet），
	// 双向同步与部署记录都以它为准。没写 cli.app 的环境推不出平台，不参与归属。
	envs, envProblems := loadEnvironments(repoDir)
	// 环境的 organizationId 也必须是完整 UUID：它被当作 HIFOX_ORGANIZATION_ID 透传给
	// 平台 CLI（`--check --live` 真值查询就用它），写 slug 会以「agent list 失败」
	// 的形式冒出来，看不到根因。
	for _, env := range envs {
		if env.OrgID != "" && !isOrgUUID(env.OrgID) {
			out = append(out, fmt.Sprintf("environments/%s/environment.yaml：organizationId 是 %q，必须是平台组织完整 UUID（slug 在 HIFOX_ORGANIZATION_ID 下会 404）", env.Name, env.OrgID))
		}
	}
	covered := map[string]map[string]bool{} // 配置集合 -> 覆盖它的平台
	for _, env := range envs {
		if env.Platform == "" || env.ConfigSet == "" {
			continue
		}
		if covered[env.ConfigSet] == nil {
			covered[env.ConfigSet] = map[string]bool{}
		}
		covered[env.ConfigSet][env.Platform] = true
	}
	configSets := configSetsOnDisk(repoDir)

	// 平台指针 → config：指针声明的 agent 必须在 config/ 里有真源。
	configSpaces := map[string]map[string]bool{} // key -> config 里声明了它的配置集合
	for _, c := range reg.Platforms {
		if found := findConfigAgent(repoDir, configSets, c.Key); found != "" {
			if configSpaces[c.Key] == nil {
				configSpaces[c.Key] = map[string]bool{}
			}
			configSpaces[c.Key][found] = true
		} else {
			out = append(out, fmt.Sprintf("%s：config 里找不到 %s", pointerLabel(c), candidatesLabel(configSets, c.Key)))
		}
		// org 必须是完整 UUID：HIFOX_ORGANIZATION_ID 喂 slug 会被平台判 404
		// ORGANIZATION_NOT_FOUND（2026-11 实测）——派活时才发现就晚了。
		if c.Org != "" && !isOrgUUID(c.Org) {
			out = append(out, fmt.Sprintf("%s：org 是 %q，必须写平台组织完整 UUID（slug 在 HIFOX_ORGANIZATION_ID 下会 404）", pointerLabel(c), c.Org))
		}
	}

	// 双向同步（ADR 0023）：config/ 是平台 agent 的真源，主控能力目录必须跟着走。
	// 这里管 config→platform 方向：覆盖该集合的每个平台都得有一份指针；
	// platform→config 方向由上面的指针检查负责。
	for _, space := range configSets {
		agentDir := filepath.Join(repoDir, "config", space, "agents")
		entries, err := os.ReadDir(agentDir)
		if err != nil {
			continue // 该集合不存在不算漂移：不是每个仓都带全部集合
		}
		var keys []string
		for _, e := range entries {
			if e.IsDir() {
				keys = append(keys, e.Name())
			}
		}
		if len(keys) == 0 {
			continue // 这个集合还没有 agent，谈不上「同步了吗」
		}
		platforms := sortedSet(covered[space])
		if len(platforms) == 0 {
			// 平台归属只在 environment.yaml 的 cli.app。没有任何环境声明这个集合部署在
			// 哪个平台，config→平台方向的指针同步就无从对账——静默跳过等于让这条规则
			// 失效（agent 加了、指针对不上，谁也不报）。一条集合一条，不按 agent 刷。
			out = append(out, fmt.Sprintf("config/%s/：没有环境声明它部署在哪个平台（environments/*/environment.yaml 缺 cli.app 或 configSet），config→平台指针同步无法对账", space))
			continue
		}
		for _, key := range keys {
			for _, platform := range platforms {
				if _, ok := reg.PlatformByKey(platform, key); !ok {
					out = append(out, fmt.Sprintf("config/%s/agents/%s：缺 super-agent/capabilities/platforms/%s/%s.yaml（config 的 agent 变更必须同步到主控能力目录，见 ADR 0023）", space, key, platform, key))
				}
			}
		}
	}

	// name 一致：注册表里的名字就是派活解析平台 ref 的主干，与 config 声明不同名
	// 会让「注册表说的 agent」和「config 部署的 agent」悄悄变成两个。
	for _, c := range reg.Platforms {
		spaces := sortedSet(configSpaces[c.Key])
		if len(spaces) == 0 {
			continue // 指针缺失已在别处报过，这里没有可比对的 config
		}
		rel := fmt.Sprintf("config/%s/agents/%s/agent.yaml", spaces[0], c.Key)
		name, err := readConfigAgentName(filepath.Join(repoDir, filepath.FromSlash(rel)))
		if err != nil {
			out = append(out, fmt.Sprintf("%s：读 %s 失败：%v", pointerLabel(c), rel, err))
			continue
		}
		if !hifox.NameMatches(c.Name, name) {
			out = append(out, fmt.Sprintf("%s：name 是 %q，%s 里是 %q", pointerLabel(c), c.Name, rel, name))
		}
	}

	// 有部署记录：环境只绑部分 agent 是常态（不算漂移），所以该平台下的环境里
	// 任一 environments/*/state.yaml 的 agents.<key> 有记录就算部署过。
	// 但只对「至少有一个环境声明了它」的平台查：没有任何环境接入该平台时，
	// 「没有部署记录」是必然的，报出来只是噪声（平台还没接）。
	out = append(out, envProblems...)
	declaredPlatforms := map[string]bool{}   // 至少被一个环境的 cli.app 点名的平台
	deployed := map[string]map[string]bool{} // 平台 -> 部署过的 key
	for _, env := range envs {
		if env.Platform == "" {
			continue
		}
		declaredPlatforms[env.Platform] = true
		if deployed[env.Platform] == nil {
			deployed[env.Platform] = map[string]bool{}
		}
		for key := range env.Agents {
			deployed[env.Platform][key] = true
		}
	}
	for _, c := range reg.Platforms {
		if !declaredPlatforms[c.Platform] {
			continue
		}
		if !deployed[c.Platform][c.Key] {
			out = append(out, fmt.Sprintf("%s：environments/ 下没有任何部署记录（声明了但没部署）", pointerLabel(c)))
		}
	}

	return out
}

// pointerLabel 是一条平台能力指针在人话里的位置（问题行前缀）。
func pointerLabel(c PlatformAgent) string {
	return fmt.Sprintf("platforms/%s/%s.yaml", c.Platform, c.Key)
}

// candidatesLabel 拼「指针该在 config 的哪里找到真源」的候选列表（报错用）。
func candidatesLabel(configSets []string, key string) string {
	if len(configSets) == 0 {
		return "config/ 下任何配置集合里的 agents/" + key + "/agent.yaml"
	}
	paths := make([]string, 0, len(configSets))
	for _, space := range configSets {
		paths = append(paths, fmt.Sprintf("config/%s/agents/%s/agent.yaml", space, key))
	}
	return strings.Join(paths, " 或 ")
}

// findConfigAgent 在 config/ 的各个配置集合里找该 key 的真源，返回配置集合名（空 = 没找到）。
func findConfigAgent(repoDir string, configSets []string, key string) string {
	for _, space := range configSets {
		if _, err := os.Stat(filepath.Join(repoDir, "config", space, "agents", key, "agent.yaml")); err == nil {
			return space
		}
	}
	return ""
}

// configSetsOnDisk 列出 config/ 下的配置集合（目录），排序；没有 config/ 视为没有集合。
func configSetsOnDisk(repoDir string) []string {
	entries, err := os.ReadDir(filepath.Join(repoDir, "config"))
	if err != nil {
		return nil
	}
	var sets []string
	for _, e := range entries {
		if e.IsDir() {
			sets = append(sets, e.Name())
		}
	}
	sort.Strings(sets)
	return sets
}

// sortedSet 把一个字符串集合排成有序切片（报错与遍历都要确定性顺序）。
func sortedSet(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// configAgent 是 config/ 下 agent.yaml 的核对子集；这里只取对账要用的 name。
type configAgent struct {
	Name string `yaml:"name"`
}

// readConfigAgentName 解析 config agent.yaml 的 name。不用 KnownFields：那份
// schema 归 x-deploy 管，字段远多于这里要的一项，认全它只会让对账跟着它碎。
func readConfigAgentName(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var a configAgent
	if err := yaml.Unmarshal(data, &a); err != nil {
		return "", err
	}
	return a.Name, nil
}

// isOrgUUID 判定组织标识是不是完整 UUID（8-4-4-4-12 十六进制）。
// slug 只做人读，喂给 HIFOX_ORGANIZATION_ID 会被平台判 404。
func isOrgUUID(s string) bool {
	groups := []int{8, 4, 4, 4, 12}
	parts := strings.Split(s, "-")
	if len(parts) != len(groups) {
		return false
	}
	for i, p := range parts {
		if len(p) != groups[i] {
			return false
		}
		for _, r := range p {
			switch {
			case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
			default:
				return false
			}
		}
	}
	return true
}

// scan 扫四类目录。fatal 是读不到/解析不了的硬错误，problems 是可继续的漂移。
func scan(root string) (*Registry, []error, []string) {
	reg := &Registry{Root: root}
	var fatal []error
	var problems []string

	for _, e := range readDir(root, "agents", &fatal) {
		if !e.IsDir() {
			continue
		}
		key := e.Name()
		dir := filepath.Join(root, "agents", key)
		var a Agent
		if err := readYAML(filepath.Join(dir, "capability.yaml"), &a); err != nil {
			fatal = append(fatal, err)
			continue
		}
		if a.Key != key {
			problems = append(problems, fmt.Sprintf("agents/%s/capability.yaml：key 是 %q，与目录名不符", key, a.Key))
			a.Key = key
		}
		name := a.Instructions
		if name == "" {
			name = "instructions.md"
		}
		a.Instructions = filepath.Join(dir, name)
		if err := readDeclaredSkills(filepath.Join(dir, "skills.yaml"), &a); err != nil {
			fatal = append(fatal, err)
			continue
		}
		reg.Skills = append(reg.Skills, scanSkills(filepath.Join(dir, "skills"), key, &problems)...)
		reg.Agents = append(reg.Agents, a)
	}

	// 共享技能：skills/<key>/SKILL.md。目录名是位置身份，必须等于 frontmatter 的
	// name（Check 强制）；技能名本身取 frontmatter。
	reg.Skills = append(reg.Skills, scanSkills(filepath.Join(root, "skills"), "", &problems)...)

	for _, e := range readDir(root, "scripts", &fatal) {
		if e.IsDir() {
			continue
		}
		key, ok := yamlKey(e.Name())
		if !ok {
			continue
		}
		var s Script
		if err := readYAML(filepath.Join(root, "scripts", e.Name()), &s); err != nil {
			fatal = append(fatal, err)
			continue
		}
		if s.Key != key {
			problems = append(problems, fmt.Sprintf("scripts/%s：key 是 %q，与文件名不符", e.Name(), s.Key))
			s.Key = key
		}
		reg.Scripts = append(reg.Scripts, s)
	}

	for _, p := range readDir(root, "platforms", &fatal) {
		if !p.IsDir() {
			continue
		}
		platform := p.Name()
		for _, e := range readDir(filepath.Join(root, "platforms"), platform, &fatal) {
			if e.IsDir() {
				continue
			}
			key, ok := yamlKey(e.Name())
			if !ok {
				continue
			}
			var c PlatformAgent
			if err := readYAML(filepath.Join(root, "platforms", platform, e.Name()), &c); err != nil {
				fatal = append(fatal, err)
				continue
			}
			c.Platform = platform
			if c.Key != key {
				problems = append(problems, fmt.Sprintf("platforms/%s/%s：key 是 %q，与文件名不符", platform, e.Name(), c.Key))
				c.Key = key
			}
			reg.Platforms = append(reg.Platforms, c)
		}
	}

	sort.Slice(reg.Agents, func(i, j int) bool { return reg.Agents[i].Key < reg.Agents[j].Key })
	sort.Slice(reg.Skills, func(i, j int) bool { return reg.Skills[i].Key < reg.Skills[j].Key })
	sort.Slice(reg.Scripts, func(i, j int) bool { return reg.Scripts[i].Key < reg.Scripts[j].Key })
	sort.Slice(reg.Platforms, func(i, j int) bool {
		if reg.Platforms[i].Platform != reg.Platforms[j].Platform {
			return reg.Platforms[i].Platform < reg.Platforms[j].Platform
		}
		return reg.Platforms[i].Key < reg.Platforms[j].Key
	})
	return reg, fatal, problems
}

// readDir 读一个分类目录；目录不存在视为该类为空，其他错误记 fatal。
func readDir(root, name string, fatal *[]error) []os.DirEntry {
	entries, err := os.ReadDir(filepath.Join(root, name))
	if err != nil {
		if !os.IsNotExist(err) {
			*fatal = append(*fatal, fmt.Errorf("read %s/%s dir: %w", root, name, err))
		}
		return nil
	}
	return entries
}

// yamlKey 取 <key>.yaml / <key>.yml 的文件名主干；其他文件不算声明。
func yamlKey(name string) (string, bool) {
	for _, ext := range []string{".yaml", ".yml"} {
		if strings.HasSuffix(name, ext) {
			return strings.TrimSuffix(name, ext), true
		}
	}
	return "", false
}

// readDeclaredSkills 读 agent 声明的共享技能（skills.yaml，裸 YAML 列表）。
// 文件不存在或为空 = 不挂任何共享技能；存在但解析失败是硬错误（写了却没生效最难排查）。
func readDeclaredSkills(path string, a *Agent) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil
	}
	if err := decodeYAML(path, data, &a.Skills); err != nil && !errors.Is(err, io.EOF) {
		return err // 只有注释时 Decode 返回 EOF：那等于没有文档，不是坏声明
	}
	return nil
}

// readYAML 解析一个声明文件，错误带路径便于人读。
func readYAML(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return decodeYAML(path, data, v)
}

// decodeYAML 统一解析口径：KnownFields 让拼错的字段当场报错。登记表是唯一真源，
// 静默忽略拼写错误等于让「写了但没生效」的声明长期潜伏。
func decodeYAML(path string, data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// scanSkills 扫一个技能根（共享 skills/ 或某 agent 的 skills/），返回 SKILL.md 条目。
// 缺 SKILL.md 的目录不算技能（可能是附属文件目录），但会记一条漂移——否则「我加了技能
// 却没生效」永远没有解释。
func scanSkills(dir, owner string, problems *[]string) []Skill {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		key := e.Name()
		path := filepath.Join(dir, key, "SKILL.md")
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			*problems = append(*problems, fmt.Sprintf("%s：缺 SKILL.md（技能目录必须有一个）", relLabel(owner, key)))
			continue
		}
		name, description, ferr := parseFrontmatter(string(data))
		if ferr != nil {
			*problems = append(*problems, fmt.Sprintf("%s：SKILL.md 的 frontmatter %v", relLabel(owner, key), ferr))
			continue
		}
		out = append(out, Skill{
			Key: key, Owner: owner,
			Name: name, Description: description,
		})
	}
	return out
}

// parseFrontmatter 取 SKILL.md 顶部 YAML frontmatter 里的 name/description。
// 只认这两个字段：omp 也只看它们，多写的字段会被忽略——这里跟着忽略，不制造假契约。
func parseFrontmatter(body string) (name, description string, err error) {
	if !strings.HasPrefix(body, "---\n") {
		return "", "", errors.New("缺失（文件必须以 --- 开头）")
	}
	rest := body[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", errors.New("未闭合（缺少结束的 ---）")
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if uerr := yaml.Unmarshal([]byte(rest[:end]), &fm); uerr != nil {
		return "", "", fmt.Errorf("解析失败: %w", uerr)
	}
	return strings.TrimSpace(fm.Name), strings.TrimSpace(fm.Description), nil
}

// relLabel 是技能在报错里的可读位置：agents/<owner>/skills/<key> 或 skills/<key>。
func relLabel(owner, key string) string {
	if owner == "" {
		return "skills/" + key
	}
	return "agents/" + owner + "/skills/" + key
}

// skillLabel 是已加载技能的同类标签。
func skillLabel(s Skill) string { return relLabel(s.Owner, s.Key) }

func orKey(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// isKebabCase 判定技能名是否是底层 CLI 接受的形态（小写字母/数字/连字符，1–64 字符）。
func isKebabCase(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}
