package hifox

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

// 用真实平台 agent 列表验证注册表 15 个名字都能解析（只读）。
// 需要网络与 hifox 登录：设 SUPERCTL_LIVE=1 才跑，CI 默认跳过。
func TestRegistryNamesResolveAgainstLivePlatform(t *testing.T) {
	if os.Getenv("SUPERCTL_LIVE") != "1" {
		t.Skip("设 SUPERCTL_LIVE=1 才跑线上校验")
	}
	out, err := exec.Command("hifox", "agent", "list", "--include-archived", "--output", "json").Output()
	if err != nil {
		t.Skip("hifox 不可用，跳过线上校验")
	}
	var raw []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Skipf("agent list 解析失败: %v", err)
	}
	refs := make([]AgentRef, 0, len(raw))
	for i, r := range raw {
		refs = append(refs, AgentRef{ID: string(rune('a' + i%26)), Name: r.Name})
	}
	declared := map[string]string{
		"wuyong": "吴用", "linchong": "林冲", "wusong": "武松", "daizong": "戴宗", "peixuan": "裴宣",
		"odin": "奥丁", "yanqing": "燕青", "xiaorang": "萧让", "tanglong": "汤隆", "baldur": "巴德尔",
		"thor": "托尔", "loki": "洛基", "eir": "埃尔", "skadi": "斯卡蒂", "maintainer-reviewer": "伊登",
	}
	for key, name := range declared {
		if _, err := ResolveDelegate(refs, name, "staging"); err != nil {
			t.Errorf("%s（%s）在线上解析失败: %v", key, name, err)
		}
	}
}
