package capability

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"super-agent/internal/hifox"
)

// 全绿路径：环境绑的 agent 在平台上名字/空间/派活许可都对得上，平台指针也能按名解析。
func TestCheckLiveGreen(t *testing.T) {
	repo := stubRepo(t)
	live := hifox.AgentRef{ID: "agent-1", Name: "吴用", SpaceIDs: []string{"space-1"}, AllowTaskAssignment: true}
	calls := map[string]int{}
	got := CheckLive(repo, func(_ /*platform*/, org string) ([]hifox.AgentRef, error) {
		calls[org]++
		return []hifox.AgentRef{live}, nil
	})
	if len(got) != 0 {
		t.Fatalf("应无漂移: %v", got)
	}
	// 环境按 organizationId 查一次；平台指针不单独查（名字核对复用已部署实例的读面）。
	if calls["11111111-1111-4111-8111-111111111111"] != 1 {
		t.Errorf("每个 org 应只查一次: %v", calls)
	}
}

// 五条平台断言各自命中：缺 id、名字变了、空间不含、已归档、平台不许派任务。
func TestCheckLiveFlagsPlatformDrift(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "environments/dev/state.yaml", `version: 1
agents:
  wuyong:
    id: agent-1
    deployedName: 吴用
  ghost:
    id: agent-missing
    deployedName: 无影
  renamed:
    id: agent-2
    deployedName: 吴用
  otherspace:
    id: agent-3
    deployedName: 武松
  retired:
    id: agent-4
    deployedName: 裴宣
  locked:
    id: agent-5
    deployedName: 戴宗
`)
	got := CheckLive(repo, func(_ /*platform*/, org string) ([]hifox.AgentRef, error) {
		if org != "11111111-1111-4111-8111-111111111111" {
			return nil, nil
		}
		return []hifox.AgentRef{
			{ID: "agent-1", Name: "吴用", SpaceIDs: []string{"space-1"}, AllowTaskAssignment: true},
			{ID: "agent-2", Name: "吴用改", SpaceIDs: []string{"space-1"}, AllowTaskAssignment: true},
			{ID: "agent-3", Name: "武松", SpaceIDs: []string{"space-other"}, AllowTaskAssignment: true},
			{ID: "agent-4", Name: "裴宣", SpaceIDs: []string{"space-1"}, AllowTaskAssignment: true, ArchivedAt: "2026-01-01T00:00:00Z"},
			{ID: "agent-5", Name: "戴宗", SpaceIDs: []string{"space-1"}},
		}, nil
	})
	for _, want := range []string{
		"environments/dev/agents/ghost：平台查不到这个 id（agent-missing）",
		`environments/dev/agents/renamed：平台名字是 "吴用改"，state.yaml 记的是 "吴用"`,
		"environments/dev/agents/otherspace：平台 spaceIds [space-other] 不含环境 spaceId space-1",
		"environments/dev/agents/retired：已归档（2026-01-01T00:00:00Z）",
		"environments/dev/agents/locked：平台不允许派任务（allowTaskAssignment=false）",
	} {
		assertContains(t, got, want)
	}
	// 对得上的那条不该被牵连。
	for _, p := range got {
		if strings.Contains(p, "agents/wuyong：") {
			t.Errorf("全对的条目不该报: %s", p)
		}
	}
}

// 已部署实例的线上名字与平台目录声明的名字不是同一个身份：state.yaml 与线上可以完全
// 一致（平台侧改名或被人顶替），只有这一条对账看得出来。
func TestCheckLiveFlagsDeployedNameNotMatchingCatalog(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "environments/dev/state.yaml",
		"version: 1\nagents:\n  wuyong:\n    id: agent-1\n    deployedName: 卢克 Luke\n"+
			"  wuyong-staging:\n    id: agent-2\n    deployedName: 吴用 (staging)\n")
	got := CheckLive(repo, func(_ /*platform*/, org string) ([]hifox.AgentRef, error) {
		return []hifox.AgentRef{
			{ID: "agent-1", Name: "卢克 Luke", SpaceIDs: []string{"space-1"}, AllowTaskAssignment: true},
			{ID: "agent-2", Name: "吴用 (staging)", SpaceIDs: []string{"space-1"}, AllowTaskAssignment: true},
		}, nil
	})
	assertContains(t, got, `environments/dev/agents/wuyong：线上名字 "卢克 Luke" 与 platforms/hifox/wuyong.yaml 声明的 "吴用" 不是同一个身份`)
	// 同层级的 staging 副本自己那条对账必须干净：它是常规部署，不是漂移。
	for _, p := range got {
		if strings.Contains(p, "agents/wuyong-staging：") {
			t.Errorf("同名多实例不该报漂移: %s", p)
		}
	}
}

// 指针写的组织必须就是该环境所在的组织：写错（或环境换了组织而指针没跟），派活会落到
// 另一个组织，那边既没有这个部署实例、也不该由这份记录管。
func TestCheckLiveFlagsPointerOrgMismatch(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/platforms/hifox/wuyong.yaml",
		"key: wuyong\nname: 吴用\nspace: HiFox\norg: 11111111-1111-1111-1111-111111111111\nnote: 统筹入口\n")
	got := CheckLive(repo, func(_ /*platform*/, _ /*org*/ string) ([]hifox.AgentRef, error) {
		return []hifox.AgentRef{{ID: "agent-1", Name: "吴用", SpaceIDs: []string{"space-1"}, AllowTaskAssignment: true}}, nil
	})
	assertContains(t, got, `platforms/hifox/wuyong.yaml：org 是 "11111111-1111-1111-1111-111111111111"，环境 dev 的 organizationId 是 "11111111-1111-4111-8111-111111111111"`)
}

// 指针 org 与环境 organizationId 一致时不报（两边都写完整 UUID 是正常形态）。
func TestCheckLiveAcceptsPointerOrgMatchingEnvironment(t *testing.T) {
	repo := stubRepo(t)
	const orgID = "86555f76-3f5b-4f7a-93c6-406074110a59"
	writeFile(t, repo, "environments/dev/environment.yaml",
		"organizationId: "+orgID+"\nspaceId: space-1\nconfigSet: hifox\ncli:\n  app: hifox\n")
	writeFile(t, repo, "super-agent/capabilities/platforms/hifox/wuyong.yaml",
		"key: wuyong\nname: 吴用\nspace: HiFox\norg: "+orgID+"\nnote: 统筹入口\n")
	got := CheckLive(repo, func(_ /*platform*/, _ /*org*/ string) ([]hifox.AgentRef, error) {
		return []hifox.AgentRef{{ID: "agent-1", Name: "吴用", SpaceIDs: []string{"space-1"}, AllowTaskAssignment: true}}, nil
	})
	if len(got) != 0 {
		t.Fatalf("org 对得上应无漂移: %v", got)
	}
}

// agent list 失败只记一条并继续别的组织，不 panic、不牵连他组。
func TestCheckLiveSurvivesAgentListFailure(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "environments/other/environment.yaml",
		"organizationId: 22222222-2222-4222-8222-222222222222\nspaceId: space-2\nconfigSet: hifox\ncli:\n  app: hifox\n")
	writeFile(t, repo, "environments/other/state.yaml",
		"version: 1\nagents:\n  wuyong:\n    id: agent-9\n    deployedName: 吴用\n")
	got := CheckLive(repo, func(_ /*platform*/, org string) ([]hifox.AgentRef, error) {
		switch org {
		case "11111111-1111-4111-8111-111111111111":
			return nil, errors.New("boom")
		case "22222222-2222-4222-8222-222222222222":
			return []hifox.AgentRef{{ID: "agent-9", Name: "吴用", SpaceIDs: []string{"space-2"}, AllowTaskAssignment: true}}, nil
		}
		return nil, nil
	})
	assertContains(t, got, "org 11111111-1111-4111-8111-111111111111：agent list 失败：boom")
	n := 0
	for _, p := range got {
		if strings.Contains(p, "agent list 失败") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("失败只应报一条，got %d: %v", n, got)
	}
	for _, p := range got {
		if strings.Contains(p, "environments/other/") {
			t.Errorf("别的组织不该被牵连: %s", p)
		}
	}
}

// 无 state.yaml 或没绑 agent 的环境（examples/ 模板）不必去查平台。
func TestCheckLiveSkipsEnvWithoutAgents(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "environments/example/environment.yaml",
		"organizationId: 99999999-9999-4999-8999-999999999999\nspaceId: space-9\nconfigSet: hifox\n")
	queried := false
	CheckLive(repo, func(_ /*platform*/, org string) ([]hifox.AgentRef, error) {
		if org == "99999999-9999-4999-8999-999999999999" {
			queried = true
		}
		return nil, nil
	})
	if queried {
		t.Error("无 state.yaml 的环境不该被查")
	}
}

// 读不懂的 environment.yaml 记成问题行，不中断对账。
func TestCheckLiveReportsBrokenEnvironmentYaml(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "environments/bad/environment.yaml", "organizationId: [oops\n")
	got := CheckLive(repo, func(string, string) ([]hifox.AgentRef, error) { return nil, nil })
	assertContains(t, got, "environments/bad/environment.yaml：解析失败：")
}

// 别的平台（将来 sharkly）只声明不查询：报一条「尚无线上对账实现」，不拿 hifox 的
// lister 去查它，也不牵连 hifox 的对账。
func TestCheckLiveReportsUnsupportedPlatform(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/platforms/sharkly/wuyong.yaml",
		"key: wuyong\nname: 吴用\nspace: HF\nnote: 兄弟平台\n")
	platforms := map[string]bool{}
	got := CheckLive(repo, func(platform, _ /*org*/ string) ([]hifox.AgentRef, error) {
		platforms[platform] = true
		return []hifox.AgentRef{{ID: "agent-1", Name: "吴用", SpaceIDs: []string{"space-1"}, AllowTaskAssignment: true}}, nil
	})
	assertContains(t, got, "platforms/sharkly/：尚无线上对账实现")
	if platforms["sharkly"] {
		t.Errorf("不该拿 hifox 的 lister 去查别的平台: %v", platforms)
	}
	if len(got) != 1 {
		t.Errorf("hifox 侧应无漂移，got %v", got)
	}
}

// 环境没写 cli.app 就推不出平台：如实报一条，不瞎猜平台去查。
func TestCheckLiveReportsEnvWithoutPlatform(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "environments/legacy/environment.yaml",
		"organizationId: org-3\nspaceId: space-3\nconfigSet: hifox\n")
	writeFile(t, repo, "environments/legacy/state.yaml",
		"version: 1\nagents:\n  wuyong:\n    id: agent-3\n    deployedName: 吴用\n")
	got := CheckLive(repo, func(string, string) ([]hifox.AgentRef, error) { return nil, nil })
	assertContains(t, got, "environments/legacy/environment.yaml：没有 cli.app，推不出平台，跳过线上对账")
}

// 用真实平台数据对账本仓（需要网络与 hifox 登录）：设 SUPERCTL_LIVE=1 才跑，
// 与 internal/hifox/live_resolve_test.go 同一门控。这是诊断口不是断言口——
// 线上可能有真实漂移（如平台改名），漂移内容原样打出来供人核对。
func TestCheckLiveAgainstRealRepo(t *testing.T) {
	if os.Getenv("SUPERCTL_LIVE") != "1" {
		t.Skip("设 SUPERCTL_LIVE=1 才跑线上校验")
	}
	repo := filepath.Join("..", "..", "..")
	got := CheckLive(repo, func(_ /*platform*/, org string) ([]hifox.AgentRef, error) {
		refs, err := hifox.NewForOrg(org).AgentListAll(context.Background())
		if err != nil {
			return nil, err
		}
		return refs, nil
	})
	for _, p := range got {
		t.Logf("线上漂移: %s", p)
	}
	if len(got) == 0 {
		t.Log("线上无漂移")
	}
}
