package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"super-agent/internal/capability"
	"super-agent/internal/hifox"
	"super-agent/internal/platform"
)

// cmdCapabilities 打印能力注册表索引（与注入主控的那份同源），--check 查漂移，
// 带 --live 再拿平台真值对账 environments/（需要 hifox 登录，故默认不跑）。
func cmdCapabilities(args []string) error {
	fs := flag.NewFlagSet("capabilities", flag.ContinueOnError)
	check := fs.Bool("check", false, "校验注册表漂移（key/文档/脚本/平台指针/master 唯一/config 同步/部署记录）")
	live := fs.Bool("live", false, "在 --check 之外拉平台真值对账 environments/ 的部署记录（需要 hifox 登录）")
	cfgPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if rest := fs.Args(); len(rest) > 0 {
		return fmt.Errorf("capabilities 不接受位置参数 %q（查漂移用 --check）", strings.Join(rest, " "))
	}
	if *live && !*check {
		return errors.New("--live 需要与 --check 同用")
	}
	cfg, _, err := loadConfig([]string{"--config", *cfgPath})
	if err != nil {
		return err
	}
	reg, err := capability.Load(cfg.RepoDir)
	if err != nil {
		return err
	}
	if !*check {
		fmt.Println(reg.Index())
		return nil
	}

	offline := reg.Check(cfg.RepoDir)
	var liveProblems []string
	if *live {
		// 指针的 org 必须是完整 UUID（离线 Check 已钉住），环境的 organizationId 同写法；
		// 这里只是把同一个值当 HIFOX_ORGANIZATION_ID 透传给 ExecRunner，让两边落在同一组织。
		ctx := context.Background()
		liveProblems = capability.CheckLive(cfg.RepoDir, func(plat, org string) ([]hifox.AgentRef, error) {
			if plat != platform.Hifox {
				return nil, fmt.Errorf("平台 %s 还没有线上对账实现", plat)
			}
			return hifox.NewForOrg(org).AgentListAll(ctx)
		})
	}
	for _, p := range offline {
		fmt.Fprintln(os.Stderr, "漂移:", p)
	}
	for _, p := range liveProblems {
		fmt.Fprintln(os.Stderr, "线上漂移:", p)
	}
	if len(offline) == 0 && len(liveProblems) == 0 && !*live {
		fmt.Println("能力注册表健康：无漂移")
		return nil
	}
	fmt.Printf("离线 %d 处 / 线上 %d 处漂移\n", len(offline), len(liveProblems))
	if len(offline)+len(liveProblems) > 0 {
		return errors.New("能力注册表有漂移（见上）")
	}
	return nil
}
