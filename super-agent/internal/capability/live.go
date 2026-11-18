package capability

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"super-agent/internal/hifox"
	"super-agent/internal/platform"
)

// AgentLister 按平台 + 组织列平台 agent；org 为空表示 CLI 当前组织。注入是为了让
// 线上对账保持纯函数（单测零网络），调用方用 hifox.ExecRunner(org) 包一层。
type AgentLister func(platform, org string) ([]hifox.AgentRef, error)

// deployedAgent 是 state.yaml 里 agents.<key> 的部署记录子集。
type deployedAgent struct {
	ID           string `yaml:"id"`
	DeployedName string `yaml:"deployedName"`
}

// envState 是一个 environments/<名字>/ 目录的核对视图：environment.yaml 的绑定
// 加 state.yaml 的部署记录。没有 state.yaml（如 environments/example）就是「未部署」。
// Platform 是 environment.yaml 的 cli.app——这个环境部署在哪个平台 CLI 上。
type envState struct {
	Name      string
	OrgID     string
	SpaceID   string
	ConfigSet string
	Platform  string
	Agents    map[string]deployedAgent
}

// envManifest / envStateFile 是 environments/ 两个真值文件的读子集。都不开
// KnownFields：两份 schema 归 x-deploy 管，含 crew/skills/workflows/automations，
// 认全它们只会让运行时对账跟着 x-deploy 的每次扩字段一起碎。
type envManifest struct {
	OrganizationID string `yaml:"organizationId"`
	SpaceID        string `yaml:"spaceId"`
	ConfigSet      string `yaml:"configSet"`
	Cli            struct {
		App string `yaml:"app"`
	} `yaml:"cli"`
}

type envStateFile struct {
	Agents map[string]deployedAgent `yaml:"agents"`
}

// CheckLive 拿平台真值与 environments/ 的声明对账（--check --live），返回给人读的
// 问题列表。问题行前缀统一 environments/<env>/agents/<key>：或 platforms/<平台>/<key>.yaml：。
// 只有 hifox 有 lister；其他平台声明了指针就报一条「尚无线上对账实现」。
func CheckLive(repoDir string, listAgents AgentLister) []string {
	envs, problems := loadEnvironments(repoDir)
	out := append([]string(nil), problems...)

	// 平台目录是「声明名」的来源：已部署实例的线上名字必须仍是同一个身份。
	reg, err := Load(repoDir)
	if err != nil {
		out = append(out, fmt.Sprintf("读能力注册表失败：%v", err))
		return out
	}
	names := make(map[string]string)
	orgs := make(map[string]string) // key -> 指针声明的组织（空 = 跟随 CLI 当前组织）
	unsupported := make(map[string]bool)
	for _, c := range reg.Platforms {
		if c.Platform != platform.Hifox {
			if !unsupported[c.Platform] {
				unsupported[c.Platform] = true
				out = append(out, fmt.Sprintf("platforms/%s/：尚无线上对账实现", c.Platform))
			}
			continue
		}
		names[c.Key] = c.Name
		if c.Org != "" {
			orgs[c.Key] = c.Org
		}
	}

	// 同一平台 + 同一 org 只查一次：environments 记 UUID、指针条目可能写 slug，
	// 两种写法各自成组；查失败只报一条，不按条目/环境刷屏。
	type listKey struct{ platform, org string }
	type listResult struct {
		refs []hifox.AgentRef
		err  error
	}
	lists := map[listKey]listResult{}
	failed := map[listKey]bool{}
	fetch := func(plat, org string) ([]hifox.AgentRef, bool) {
		k := listKey{plat, org}
		res, ok := lists[k]
		if !ok {
			refs, err := listAgents(plat, org)
			res = listResult{refs: refs, err: err}
			lists[k] = res
		}
		if res.err != nil {
			if !failed[k] {
				failed[k] = true
				out = append(out, fmt.Sprintf("org %s：agent list 失败：%v", orgLabel(org), res.err))
			}
			return nil, false
		}
		return res.refs, true
	}

	for _, env := range envs {
		// 无 state.yaml 或没绑任何 agent 的环境（examples/模板）不参与线上对账。
		if len(env.Agents) == 0 {
			continue
		}
		// 平台归属来自 environment.yaml 的 cli.app：推不出平台就没法查，如实报一条。
		if env.Platform == "" {
			out = append(out, fmt.Sprintf("environments/%s/environment.yaml：没有 cli.app，推不出平台，跳过线上对账", env.Name))
			continue
		}
		if env.Platform != platform.Hifox {
			out = append(out, fmt.Sprintf("environments/%s/：平台 %q 尚无线上对账实现", env.Name, env.Platform))
			continue
		}
		refs, ok := fetch(env.Platform, env.OrgID)
		if !ok {
			continue // 该组织查不到，已记一条；其余组织继续
		}
		byID := make(map[string]hifox.AgentRef, len(refs))
		for _, r := range refs {
			byID[r.ID] = r
		}
		for _, key := range sortedKeys(env.Agents) {
			dep := env.Agents[key]
			prefix := fmt.Sprintf("environments/%s/agents/%s：", env.Name, key)
			live, found := byID[dep.ID]
			if !found {
				out = append(out, prefix+fmt.Sprintf("平台查不到这个 id（%s）", dep.ID))
				continue
			}
			if live.Name != dep.DeployedName {
				out = append(out, prefix+fmt.Sprintf("平台名字是 %q，state.yaml 记的是 %q", live.Name, dep.DeployedName))
			}
			// 线上实例还得与平台目录声明的名字是同一个身份：平台侧改名（或被别的
			// agent 顶替）时 state 与线上可以完全一致，只有这一条看得出来。
			if name, ok := names[key]; ok && !hifox.PlatformNameMatches(name, live.Name) {
				out = append(out, prefix+fmt.Sprintf("线上名字 %q 与 platforms/%s/%s.yaml 声明的 %q 不是同一个身份", live.Name, env.Platform, key, name))
			}
			// 指针写的组织必须就是该环境所在的组织：写错（或环境换了组织而指针没跟）
			// 会让派活落到另一个组织，那边既没有这个部署实例、也不该由这份记录管。
			if org, ok := orgs[key]; ok && org != env.OrgID {
				out = append(out, fmt.Sprintf("%s：org 是 %q，环境 %s 的 organizationId 是 %q",
					pointerLabel(PlatformAgent{Platform: env.Platform, Key: key}), org, env.Name, env.OrgID))
			}
			if !containsString(live.SpaceIDs, env.SpaceID) {
				out = append(out, prefix+fmt.Sprintf("平台 spaceIds %v 不含环境 spaceId %s", live.SpaceIDs, env.SpaceID))
			}
			if live.ArchivedAt != "" {
				out = append(out, prefix+fmt.Sprintf("已归档（%s）", live.ArchivedAt))
			}
			if !live.AllowTaskAssignment {
				out = append(out, prefix+"平台不允许派任务（allowTaskAssignment=false）")
			}
		}
	}

	// 不做「平台指针名在平台上唯一可解析」的断言：同层级双实例（prod/staging）本来
	// 就命中多个，派活时用 --instance 消歧是常规路径，不是漂移。

	return out
}

// loadEnvironments 列出 environments/ 下每个环境，按目录名排序。目录不存在视为
// 「这个仓没有环境」；读不懂的 yaml 记成问题行，不阻断其余环境对账。
func loadEnvironments(repoDir string) ([]envState, []string) {
	entries, err := os.ReadDir(filepath.Join(repoDir, "environments"))
	if err != nil {
		return nil, nil
	}
	var envs []envState
	var problems []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		env := envState{Name: e.Name()}
		dir := filepath.Join(repoDir, "environments", e.Name())

		if data, err := os.ReadFile(filepath.Join(dir, "environment.yaml")); err == nil {
			var m envManifest
			if err := yaml.Unmarshal(data, &m); err != nil {
				problems = append(problems, fmt.Sprintf("environments/%s/environment.yaml：解析失败：%v", env.Name, err))
			} else {
				env.OrgID, env.SpaceID, env.ConfigSet, env.Platform = m.OrganizationID, m.SpaceID, m.ConfigSet, m.Cli.App
			}
		} else if !os.IsNotExist(err) {
			problems = append(problems, fmt.Sprintf("environments/%s/environment.yaml：读取失败：%v", env.Name, err))
		}

		if data, err := os.ReadFile(filepath.Join(dir, "state.yaml")); err == nil {
			var s envStateFile
			if err := yaml.Unmarshal(data, &s); err != nil {
				problems = append(problems, fmt.Sprintf("environments/%s/state.yaml：解析失败：%v", env.Name, err))
			} else {
				env.Agents = s.Agents
			}
		} else if !os.IsNotExist(err) {
			problems = append(problems, fmt.Sprintf("environments/%s/state.yaml：读取失败：%v", env.Name, err))
		}

		envs = append(envs, env)
	}
	sort.Slice(envs, func(i, j int) bool { return envs[i].Name < envs[j].Name })
	return envs, problems
}

// orgLabel 给「当前组织」（org 为空）一个人话名字。
func orgLabel(org string) string {
	if strings.TrimSpace(org) == "" {
		return "（当前组织）"
	}
	return org
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]deployedAgent) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
