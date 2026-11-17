package main

import (
	"errors"
	"fmt"
	"os"

	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/boot"
	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/capability"
	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/config"
)

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
	exp := boot.Expected{
		RepoDir:          cfg.RepoDir,
		MasterAgent:      master.Key,
		Kind:             kind,
		MasterArgs:       append(append([]string{}, master.Args...), cfg.MasterAgentArgs...),
		DynamicWS:        "jobs",
		MasterSessionDir: newSessionDir(cfg, "start"),
		MasterPrime: boot.PrimePrompt(string(manual), boot.Paths{
			CapabilitiesDir: cfg.CapabilitiesDir(),
			ActiveDir:       cfg.ActiveDir(),
			ArchivedDir:     cfg.ArchivedDir(),
			ResultsDir:      cfg.ResultsDir(),
			InflightPath:    cfg.InflightPath(),
			Index:           reg.Index(),
		}),
		PrimeMarkerDir: cfg.PrimeMarkerDir(),
	}
	if retro, ok := reg.Retro(); ok {
		doc, err := os.ReadFile(retro.Instructions)
		if err != nil {
			return boot.Expected{}, fmt.Errorf("读复盘手册 %s: %w", retro.Instructions, err)
		}
		exp.RetroAgent = retro.Key
		exp.RetroPrime = string(doc)
		exp.RetroArgs = retro.Args
		exp.RetroSessionDir = newSessionDir(cfg, "start")
	} else {
		// 没有复盘 agent 也要能起：Ensure 对空 RetroAgent 不做动作。
		exp.RetroAgent = ""
	}
	return exp, nil
}
