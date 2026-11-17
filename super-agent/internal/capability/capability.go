// Package capability 读取本仓的本地能力声明目录（super-agent/capabilities/），
// 生成主控每轮都能读的紧凑索引，并提供漂移检查。
//
// 目录即声明：
//
//	agents/<key>/{capability.yaml,instructions.md}
//	skills/<key>/{capability.yaml,doc.md}
//	scripts/<key>.yaml
//	cloud/<key>.yaml
//
// 云端能力的真源仍是 config/（config/hifox/agents/<key>、config/apifox/agents/<key>）；
// cloud/ 条目只写指针，Check 校验指针在 config/ 里确实存在。
package capability

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

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
	Instructions  string   `yaml:"instructions"` // 解析后的绝对路径
}

// Skill 是可复用的方法论文档（何时用 + 正文）。
type Skill struct {
	Key         string `yaml:"key"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	WhenToUse   string `yaml:"whenToUse"`
	Doc         string `yaml:"doc"` // 解析后的绝对路径
}

// Script 是直连白名单里的一个可执行脚本。
type Script struct {
	Key         string `yaml:"key"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Path        string `yaml:"path"` // 相对 repoDir 或绝对
}

// Cloud 是 hifox 云端 agent 的指针，真源在 config/。
type Cloud struct {
	Key   string `yaml:"key"`
	Name  string `yaml:"name"`
	Space string `yaml:"space"`
	Org   string `yaml:"org"` // 平台组织 slug 或完整 UUID；空 = 用 CLI 当前组织
	Note  string `yaml:"note"`
}

// Registry 是一次加载得到的全部声明，切片按 key 排序。
type Registry struct {
	Root    string
	Agents  []Agent
	Skills  []Skill
	Scripts []Script
	Cloud   []Cloud
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

// Index 生成主控读的能力索引：四段，一项一行，只给名字、一句话与文档路径。
func (r *Registry) Index() string {
	var b strings.Builder

	b.WriteString("## 云端能力\n\n")
	if len(r.Cloud) == 0 {
		b.WriteString("（无）\n")
	}
	for _, c := range r.Cloud {
		fmt.Fprintf(&b, "- %s %s（%s）：%s\n", c.Key, c.Name, c.Space, c.Note)
	}

	b.WriteString("\n## 本地能力\n\n")
	if len(r.Agents) == 0 {
		b.WriteString("（无）\n")
	}
	for _, a := range r.Agents {
		fmt.Fprintf(&b, "- %s %s（%s/%s，kind %s）：%s\n", a.Key, a.Name, a.Role, a.Workspace, a.Kind, a.Description)
	}

	b.WriteString("\n## 技能\n\n")
	if len(r.Skills) == 0 {
		b.WriteString("（无）\n")
	}
	for _, s := range r.Skills {
		fmt.Fprintf(&b, "- %s %s：%s（何时用：%s；文档：%s）\n", s.Key, s.Name, s.Description, s.WhenToUse, s.Doc)
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

// CloudByKey 按 key 取云端指针条目。
func (r *Registry) CloudByKey(key string) (Cloud, bool) {
	for _, c := range r.Cloud {
		if c.Key == key {
			return c, true
		}
	}
	return Cloud{}, false
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

	out := make([]string, 0, len(fatal)+len(problems)+len(reg.Agents)+len(reg.Scripts)+len(reg.Cloud))
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

	for _, s := range reg.Skills {
		if _, err := os.Stat(s.Doc); err != nil {
			out = append(out, fmt.Sprintf("skills/%s：说明文档 %s 不存在", s.Key, s.Doc))
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

	for _, c := range reg.Cloud {
		var found string
		for _, space := range []string{"hifox", "apifox"} {
			p := filepath.Join(repoDir, "config", space, "agents", c.Key, "agent.yaml")
			if _, err := os.Stat(p); err == nil {
				found = p
				break
			}
		}
		if found == "" {
			out = append(out, fmt.Sprintf("cloud/%s.yaml：config 里找不到 config/hifox/agents/%s/agent.yaml 或 config/apifox/agents/%s/agent.yaml", c.Key, c.Key, c.Key))
		}
	}

	return out
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
		reg.Agents = append(reg.Agents, a)
	}

	for _, e := range readDir(root, "skills", &fatal) {
		if !e.IsDir() {
			continue
		}
		key := e.Name()
		dir := filepath.Join(root, "skills", key)
		var s Skill
		if err := readYAML(filepath.Join(dir, "capability.yaml"), &s); err != nil {
			fatal = append(fatal, err)
			continue
		}
		if s.Key != key {
			problems = append(problems, fmt.Sprintf("skills/%s/capability.yaml：key 是 %q，与目录名不符", key, s.Key))
			s.Key = key
		}
		name := s.Doc
		if name == "" {
			name = "doc.md"
		}
		s.Doc = filepath.Join(dir, name)
		reg.Skills = append(reg.Skills, s)
	}

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

	for _, e := range readDir(root, "cloud", &fatal) {
		if e.IsDir() {
			continue
		}
		key, ok := yamlKey(e.Name())
		if !ok {
			continue
		}
		var c Cloud
		if err := readYAML(filepath.Join(root, "cloud", e.Name()), &c); err != nil {
			fatal = append(fatal, err)
			continue
		}
		if c.Key != key {
			problems = append(problems, fmt.Sprintf("cloud/%s：key 是 %q，与文件名不符", e.Name(), c.Key))
			c.Key = key
		}
		reg.Cloud = append(reg.Cloud, c)
	}

	sort.Slice(reg.Agents, func(i, j int) bool { return reg.Agents[i].Key < reg.Agents[j].Key })
	sort.Slice(reg.Skills, func(i, j int) bool { return reg.Skills[i].Key < reg.Skills[j].Key })
	sort.Slice(reg.Scripts, func(i, j int) bool { return reg.Scripts[i].Key < reg.Scripts[j].Key })
	sort.Slice(reg.Cloud, func(i, j int) bool { return reg.Cloud[i].Key < reg.Cloud[j].Key })
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

// readYAML 解析一个声明文件，错误带路径便于人读。
func readYAML(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	// KnownFields：声明里拼错的字段当场报错。登记表是唯一真源，静默忽略拼写错误
	// 等于让「写了但没生效」的声明长期潜伏。
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
