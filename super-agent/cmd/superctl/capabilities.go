package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"apifox.github.com/vin568/hifox-configuration/super-agent/internal/capability"
)

// cmdCapabilities 打印能力注册表索引（与注入主控的那份同源），--check 查漂移。
func cmdCapabilities(args []string) error {
	fs := flag.NewFlagSet("capabilities", flag.ContinueOnError)
	check := fs.Bool("check", false, "校验注册表漂移（key/文档/脚本/云端指针/master 唯一）")
	cfgPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if rest := fs.Args(); len(rest) > 0 {
		return fmt.Errorf("capabilities 不接受位置参数 %q（查漂移用 --check）", strings.Join(rest, " "))
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
	problems := reg.Check(cfg.RepoDir)
	if len(problems) == 0 {
		fmt.Println("能力注册表健康：无漂移")
		return nil
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "漂移:", p)
	}
	return errors.New("能力注册表有漂移（见上）")
}
