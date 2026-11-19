package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"super-agent/internal/boot"
	"super-agent/internal/capability"
	"super-agent/internal/config"
)

// runtimeCmdPath 是能给主控与能力 agent 使用的 superctl 绝对路径。手册里到处写裸
// `superctl`，而本机 superctl 未必在 PATH 上（实测 which superctl 为空），照着敲会
// 扑空——把真实路径给它是唯一能让手册落地的办法。
//
// 不能无条件用 os.Executable()：serve/ensure 经常是 `go run` 起的，os.Executable()
// 指向 go-build 的临时产物，进程一换就消失；注进 prime 的路径下一次必扑空（实测
// 主控第一次 reply 就撞上 No such file）。临时构建一律回退到仓库里构建好的 bin，
// 取不到就留空（那段不打印），绝不灌一条死路径。
func runtimeCmdPath(cfg config.Config) string {
	exe, err := os.Executable()
	if err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		if !isEphemeralBuildPath(exe) {
			return exe
		}
	}
	return stableRuntimeCmd(cfg.RepoDir)
}

// isEphemeralBuildPath 判定路径是否来自 go run/go test 的临时构建目录：这两类产物
// 的寿命跟着父进程，不能写进跨会话的注入。
func isEphemeralBuildPath(p string) bool {
	if p == "" {
		return true
	}
	// 两边的软链接都解开再比：调用方取到的是 EvalSymlinks 后的路径，调用方自己
	// 可能只是把原始路径传进来。解析失败（路径不存在）就按原样比较。
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	if strings.Contains(p, string(filepath.Separator)+"go-build") {
		return true
	}
	tmp := os.TempDir()
	if tmp == "" {
		return false
	}
	// macOS 的 /var/folders 会解析成 /private/var/folders；此时按解析后的 tmp 再比一次。
	if resolved, err := filepath.EvalSymlinks(tmp); err == nil &&
		strings.HasPrefix(p, filepath.Clean(resolved)+string(filepath.Separator)) {
		return true
	}
	return strings.HasPrefix(p, filepath.Clean(tmp)+string(filepath.Separator))
}

// stableRuntimeCmd 在仓库里找构建好的 superctl（README 的 `go build -o bin/superctl`）。
func stableRuntimeCmd(repoDir string) string {
	for _, p := range []string{
		filepath.Join(repoDir, "super-agent", "bin", "superctl"),
		filepath.Join(repoDir, "bin", "superctl"),
	} {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p
		}
	}
	return ""
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
			RuntimeCmd:      runtimeCmdPath(cfg),
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
		exp.RetroPrime = boot.PrimeBanner + "\n\n" + string(doc)
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
	mount := capability.SkillMounts(cfg.RepoDir, a)
	if len(mount.Roots) == 0 {
		return nil, nil
	}
	overlay, err := capability.WriteOverlay(filepath.Join(cfg.SkillsDir(), a.Key), mount)
	if err != nil {
		return nil, fmt.Errorf("写 %s 的技能覆盖配置: %w", a.Key, err)
	}
	return []string{"--config", overlay}, nil
}
