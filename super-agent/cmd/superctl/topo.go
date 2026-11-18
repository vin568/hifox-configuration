package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"super-agent/internal/boot"
	"super-agent/internal/capability"
	"super-agent/internal/config"
)

// runtimeCmdPath 是当前这个 superctl 可执行文件的绝对路径。手册里到处写裸 `superctl`，
// 而本机 superctl 未必在 PATH 上（实测 which superctl 为空），主控照着手册敲会扑空——
// 把真实路径给它是唯一能让手册落地的办法。取不到就留空（那段不打印）。
func runtimeCmdPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}

// deriveAgentNames 从能力注册表取主控/复盘 agent 的 key（注册表是它们的唯一声明处）。
func deriveAgentNames(cfg config.Config) (master, retro string, err error) {
	reg, err := capability.Load(cfg.RepoDir)
	if err != nil {
		return "", "", fmt.Errorf("能力注册表加载失败: %w", err)
	}
	m, ok := reg.Master()
	if !ok {
		return "", "", errors.New("能力注册表里没有 role=master 的 agent（super-agent/capabilities/agents/）")
	}
	if r, ok := reg.Retro(); ok {
		return m.Key, r.Key, nil
	}
	return m.Key, "", nil
}

// expectedTopo 由能力注册表派生 boot 的目标拓扑（ADR 0021）：主控与复盘 agent 不
// 再硬编码在配置里，注册表是它们的唯一声明处；prime 文本 = 该 agent 的手册 + 能力索引 + 路径。
func expectedTopo(cfg config.Config) (boot.Expected, error) {
	reg, err := capability.Load(cfg.RepoDir)
	if err != nil {
		return boot.Expected{}, fmt.Errorf("能力注册表加载失败: %w", err)
	}
	master, ok := reg.Master()
	if !ok {
		return boot.Expected{}, errors.New("能力注册表里没有 role=master 的 agent（super-agent/capabilities/agents/）")
	}
	manual, err := os.ReadFile(master.Instructions)
	if err != nil {
		return boot.Expected{}, fmt.Errorf("读主控手册 %s: %w", master.Instructions, err)
	}
	kind := master.Kind
	if kind == "" {
		kind = "omp"
	}
	// 技能：把注册表里的 SKILL.md 真源目录直接交给底层 CLI 的 skills.customDirectories
	// （不复制——副本就是第二份可编辑的真源），每个 agent 一份覆盖配置：共享根 + 它
	// 自己的专属根。模型于是能按 description 自己判断何时读正文，不必靠手册写路径。
	skillOpts, err := skillArgs(cfg, master)
	if err != nil {
		return boot.Expected{}, err
	}
	masterArgs := append(append(append([]string{}, master.Args...), skillOpts...), cfg.MasterAgentArgs...)
	exp := boot.Expected{
		RepoDir:          cfg.RepoDir,
		MasterAgent:      master.Key,
		Kind:             kind,
		MasterArgs:       masterArgs,
		DynamicWS:        "jobs",
		MasterSessionDir: newSessionDir(cfg, "start"),
		MasterPrime: boot.PrimePrompt(string(manual), boot.Paths{
			CapabilitiesDir: cfg.CapabilitiesDir(),
			ActiveDir:       cfg.ActiveDir(),
			ArchivedDir:     cfg.ArchivedDir(),
			ResultsDir:      cfg.ResultsDir(),
			BriefsDir:       cfg.BriefsDir(),
			MediaDir:        cfg.MediaDir(),
			InflightPath:    cfg.InflightPath(),
			RuntimeCmd:      runtimeCmdPath(),
			Index:           reg.Index(),
		}),
		PrimeMarkerDir: cfg.PrimeMarkerDir(),
	}
	if retro, ok := reg.Retro(); ok {
		doc, err := os.ReadFile(retro.Instructions)
		if err != nil {
			return boot.Expected{}, fmt.Errorf("读复盘手册 %s: %w", retro.Instructions, err)
		}
		retroSkillOpts, rerr := skillArgs(cfg, retro)
		if rerr != nil {
			return boot.Expected{}, rerr
		}
		retroArgs := append(append([]string{}, retro.Args...), retroSkillOpts...)
		exp.RetroAgent = retro.Key
		exp.RetroPrime = string(doc)
		exp.RetroArgs = retroArgs
		exp.RetroSessionDir = newSessionDir(cfg, "start")
	} else {
		// 没有复盘 agent 也要能起：Ensure 对空 RetroAgent 不做动作。
		exp.RetroAgent = ""
	}
	return exp, nil
}

// skillArgs 给一个 agent 拼技能发现参数：把共享技能根与它自己的技能根写进一份
// omp 覆盖配置，作为 `--config` 追加到启动参数。没有技能就返回空。
//
// 不复制 SKILL.md：真源目录直接交给底层 CLI（skills.customDirectories）。复制会造出
// 第二份可编辑的副本，也会让「哪份才是真源」再次含糊（ADR 0025 修订）。
func skillArgs(cfg config.Config, a capability.Agent) ([]string, error) {
	roots := capability.SkillRoots(cfg.RepoDir, a)
	if len(roots) == 0 {
		return nil, nil
	}
	overlay, err := capability.WriteOverlay(filepath.Join(cfg.SkillsDir(), a.Key), roots)
	if err != nil {
		return nil, fmt.Errorf("写 %s 的技能覆盖配置: %w", a.Key, err)
	}
	return []string{"--config", overlay}, nil
}
