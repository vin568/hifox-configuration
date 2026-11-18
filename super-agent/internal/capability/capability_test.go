package capability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile 在 repoDir 下按相对路径造桩。
func writeFile(t *testing.T, repoDir, rel, body string) string {
	t.Helper()
	p := filepath.Join(repoDir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func agentYAML(key, role string) string {
	return "key: " + key + "\nname: " + key + "\ndescription: d\nrole: " + role + "\nworkspace: master\nkind: omp\n"
}

// stubRepo 造一个最小但完整的能力目录（master + retro + 技能 + 可执行脚本 + 平台指针）。
func stubRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()

	writeFile(t, repo, "super-agent/capabilities/agents/songjiang/capability.yaml", agentYAML("songjiang", "master"))
	writeFile(t, repo, "super-agent/capabilities/agents/songjiang/instructions.md", "# 宋江\n")
	writeFile(t, repo, "super-agent/capabilities/agents/jiangjing/capability.yaml", agentYAML("jiangjing", "retro"))
	writeFile(t, repo, "super-agent/capabilities/agents/jiangjing/instructions.md", "# 蒋敬\n")

	writeFile(t, repo, "super-agent/capabilities/skills/platform-dispatch/capability.yaml",
		"key: platform-dispatch\nname: 平台派活\ndescription: d\nwhenToUse: w\n")
	writeFile(t, repo, "super-agent/capabilities/skills/platform-dispatch/doc.md", "# 平台派活\n")

	writeFile(t, repo, "super-agent/capabilities/scripts/probe.yaml",
		"key: probe\nname: 探针\ndescription: d\npath: super-agent/direct/probe.sh\n")
	script := writeFile(t, repo, "super-agent/direct/probe.sh", "#!/bin/sh\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	writeFile(t, repo, "super-agent/capabilities/platforms/hifox/wuyong.yaml",
		"key: wuyong\nname: 吴用\nspace: HiFox\nnote: 统筹入口\n")
	writeFile(t, repo, "config/hifox/agents/wuyong/agent.yaml", "key: wuyong\nname: 吴用\n")
	// 平台指针要有部署记录才算闭环（Check 的第三条），对账还要读 environment.yaml；
	// 平台归属来自 environment.yaml 的 cli.app。
	writeFile(t, repo, "environments/dev/environment.yaml",
		"organizationId: 11111111-1111-4111-8111-111111111111\nspaceId: space-1\nconfigSet: hifox\ncli:\n  app: hifox\n")
	writeFile(t, repo, "environments/dev/state.yaml",
		"version: 1\nagents:\n  wuyong:\n    id: agent-1\n    deployedName: 吴用\n")

	return repo
}

// withApifoxEnv 补一个绑 configSet apifox 的环境（同样落在 hifox 平台上）：
// config/apifox 的条目要不要指针，取决于有没有环境声明它在哪个平台。
func withApifoxEnv(t *testing.T, repo string) {
	t.Helper()
	writeFile(t, repo, "environments/staging/environment.yaml",
		"organizationId: 11111111-1111-4111-8111-111111111111\nspaceId: space-2\nconfigSet: apifox\ncli:\n  app: hifox\n")
}

func TestLoadOK(t *testing.T) {
	repo := stubRepo(t)
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(r.Agents) != 2 || len(r.Skills) != 1 || len(r.Scripts) != 1 || len(r.Platforms) != 1 {
		t.Fatalf("条目数不对: agents=%d skills=%d scripts=%d platforms=%d",
			len(r.Agents), len(r.Skills), len(r.Scripts), len(r.Platforms))
	}
	if r.Root != filepath.Join(repo, "super-agent", "capabilities") {
		t.Errorf("Root = %q", r.Root)
	}

	m, ok := r.Master()
	if !ok || m.Key != "songjiang" {
		t.Fatalf("Master = %+v, %v", m, ok)
	}
	want := filepath.Join(repo, "super-agent", "capabilities", "agents", "songjiang", "instructions.md")
	if m.Instructions != want {
		t.Errorf("Instructions = %q, want %q", m.Instructions, want)
	}
	if _, ok := r.Retro(); !ok {
		t.Errorf("Retro 未找到")
	}
	if r.Skills[0].Doc != filepath.Join(repo, "super-agent", "capabilities", "skills", "platform-dispatch", "doc.md") {
		t.Errorf("Skill.Doc = %q", r.Skills[0].Doc)
	}
	if r.Platforms[0].Space != "HiFox" || r.Platforms[0].Note != "统筹入口" || r.Platforms[0].Platform != "hifox" {
		t.Errorf("PlatformAgent = %+v", r.Platforms[0])
	}
	if p, ok := r.PlatformByKey("hifox", "wuyong"); !ok || p.Name != "吴用" {
		t.Errorf("PlatformByKey(hifox, wuyong) = %+v, %v", p, ok)
	}
	if _, ok := r.PlatformByKey("sharkly", "wuyong"); ok {
		t.Error("别的平台不该命中同 key 的指针")
	}
	if got := platformSet(r); len(got) != 1 || !got["hifox"] {
		t.Errorf("平台集合 = %v", got)
	}
	if got := r.Check(repo); len(got) != 0 {
		t.Errorf("Check 应为空, got %v", got)
	}
}

func TestLoadAgentsSortedByKey(t *testing.T) {
	repo := stubRepo(t)
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Agents[0].Key != "jiangjing" || r.Agents[1].Key != "songjiang" {
		t.Errorf("agents 未按 key 排序: %+v", r.Agents)
	}
}

func TestLoadMissingDir(t *testing.T) {
	if _, err := Load(t.TempDir()); err != nil {
		t.Fatalf("空仓应加载为空而非报错: %v", err)
	}
}

func TestKeyMismatch(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/agents/songjiang/capability.yaml", agentYAML("someoneelse", "master"))
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// key 以目录名为准，确保索引里不会出现目录外的名字。
	if _, ok := r.Master(); !ok || r.Agents[1].Key != "songjiang" {
		t.Errorf("key 未回落到目录名: %+v", r.Agents)
	}
	assertContains(t, r.Check(repo), "与目录名不符")
}

func TestMasterMissing(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/agents/songjiang/capability.yaml", agentYAML("songjiang", "job"))
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertContains(t, r.Check(repo), "缺少 role: master")
}

func TestMasterDuplicate(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/agents/linchong/capability.yaml", agentYAML("linchong", "master"))
	writeFile(t, repo, "super-agent/capabilities/agents/linchong/instructions.md", "# 林冲\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertContains(t, r.Check(repo), "role: master 必须恰好一条")
}

func TestRetroDuplicate(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/agents/wusong/capability.yaml", agentYAML("wusong", "retro"))
	writeFile(t, repo, "super-agent/capabilities/agents/wusong/instructions.md", "# 武松\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertContains(t, r.Check(repo), "role: retro 最多一条")
}

func TestMissingInstructions(t *testing.T) {
	repo := stubRepo(t)
	if err := os.Remove(filepath.Join(repo, "super-agent/capabilities/agents/songjiang/instructions.md")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertContains(t, r.Check(repo), "说明文档")
}

func TestMissingSkillDoc(t *testing.T) {
	repo := stubRepo(t)
	if err := os.Remove(filepath.Join(repo, "super-agent/capabilities/skills/platform-dispatch/doc.md")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertContains(t, r.Check(repo), "skills/platform-dispatch")
}

func TestScriptNotExecutable(t *testing.T) {
	repo := stubRepo(t)
	if err := os.Chmod(filepath.Join(repo, "super-agent/direct/probe.sh"), 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertContains(t, r.Check(repo), "不可执行")
}

func TestScriptMissing(t *testing.T) {
	repo := stubRepo(t)
	if err := os.Remove(filepath.Join(repo, "super-agent/direct/probe.sh")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertContains(t, r.Check(repo), "不存在")
}

func TestPlatformPointerMissing(t *testing.T) {
	repo := stubRepo(t)
	if err := os.RemoveAll(filepath.Join(repo, "config", "hifox", "agents", "wuyong")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertContains(t, r.Check(repo), "config 里找不到")
}

// 指针声明的 agent 落在哪个配置集合不重要（hifox/apifox 都是同一个平台上的 Space）：
// 只要那个集合有环境声明它在 hifox 上，config/apifox 的条目就该由 hifox 指针覆盖。
func TestPlatformPointerInOtherConfigSet(t *testing.T) {
	repo := stubRepo(t)
	withApifoxEnv(t, repo)
	if err := os.RemoveAll(filepath.Join(repo, "config", "hifox", "agents", "wuyong")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	writeFile(t, repo, "config/apifox/agents/wuyong/agent.yaml", "key: wuyong\nname: 吴用\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := r.Check(repo); len(got) != 0 {
		t.Errorf("Check 应为空, got %v", got)
	}
}

func TestLoadBadYAML(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/platforms/hifox/wuyong.yaml", "key: [unterminated\n")
	if _, err := Load(repo); err == nil {
		t.Fatalf("坏 YAML 应报错")
	}
}

func TestIndex(t *testing.T) {
	repo := stubRepo(t)
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	idx := r.Index()
	for _, want := range []string{
		"## 平台能力", "## 本地能力", "## 技能", "## 直连脚本",
		"- [hifox] wuyong 吴用（HiFox）：统筹入口",
		"songjiang", "jiangjing", "platform-dispatch", "probe",
	} {
		if !strings.Contains(idx, want) {
			t.Errorf("Index 缺少 %q:\n%s", want, idx)
		}
	}
}

// platformSet 取注册表里出现过的平台名（断言用；生产侧按 reg.Platforms 直接遍历即可）。
func platformSet(r *Registry) map[string]bool {
	set := map[string]bool{}
	for _, c := range r.Platforms {
		set[c.Platform] = true
	}
	return set
}

func assertContains(t *testing.T, problems []string, want string) {
	t.Helper()
	for _, p := range problems {
		if strings.Contains(p, want) {
			return
		}
	}
	t.Fatalf("问题列表里没有 %q: %v", want, problems)
}

// direct/ 里有、声明里没有的脚本必须报漂移：登记表不能落后于目录，
// 否则主控按登记表看不见它，而它仍然能被直接执行。
func TestCheckFlagsUnregisteredDirectScript(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/direct/orphan.sh", "#!/bin/sh\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	problems := r.Check(repo)
	found := false
	for _, p := range problems {
		if strings.Contains(p, "orphan.sh") && strings.Contains(p, "未在 capabilities/scripts/") {
			found = true
		}
	}
	if !found {
		t.Fatalf("未登记脚本应报漂移: %v", problems)
	}
}

// config/ 里新增、但没同步到主控能力目录的 agent 必须报漂移：真源在 config/，
// 注册表落后就意味着主控看不见这个 agent。指针该落在哪个平台由 environments 的
// cli.app 决定（config/apifox 也部署在 hifox 上）。
func TestCheckFlagsConfigAgentWithoutPlatformEntry(t *testing.T) {
	repo := stubRepo(t)
	withApifoxEnv(t, repo)
	writeFile(t, repo, "config/apifox/agents/zhugui/agent.yaml", "key: zhugui\nname: 朱贵\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertContains(t, r.Check(repo), "config/apifox/agents/zhugui：缺 super-agent/capabilities/platforms/hifox/zhugui.yaml")
}

// 集合没有环境声明平台归属时：
//  1. 必须如实报一条「没有环境声明它部署在哪个平台」——静默跳过等于这条同步规则失效；
//  2. 不按 agent 刷漂移（推不出平台目录就不该瞎报指针缺失）；
//  3. 没有任何环境接入的平台，其指针不报「没有部署记录」——那是必然的，不是漂移。
func TestCheckFlagsConfigSetWithoutPlatformBinding(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "config/apifox/agents/zhugui/agent.yaml", "key: zhugui\nname: 朱贵\n")
	writeFile(t, repo, "super-agent/capabilities/platforms/sharkly/odin.yaml", "key: odin\nname: 奥丁\nspace: HF\n")
	writeFile(t, repo, "config/apifox/agents/odin/agent.yaml", "key: odin\nname: 奥丁\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	problems := r.Check(repo)
	assertContains(t, problems, "config/apifox/：没有环境声明它部署在哪个平台")
	for _, p := range problems {
		if strings.Contains(p, "zhugui") {
			t.Errorf("没有平台绑定的配置集合不该报指针漂移: %s", p)
		}
		if strings.Contains(p, "platforms/sharkly/") && strings.Contains(p, "部署记录") {
			t.Errorf("没有任何环境接入的平台不该报「没部署」: %s", p)
		}
	}
}

// 平台归属按 environment.yaml 的 cli.app 走，不能写死成 hifox：集合声明在 sharkly 上时
// 指针必须落在 platforms/sharkly/，现存的 hifox 指针不算数。
func TestCheckFlagsPointerMissingForNonHifoxBinding(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "config/apifox/agents/loki/agent.yaml", "key: loki\nname: 洛基\n")
	writeFile(t, repo, "super-agent/capabilities/platforms/hifox/loki.yaml", "key: loki\nname: 洛基\nspace: AF\n")
	writeFile(t, repo, "environments/sharkly/environment.yaml",
		"organizationId: 99999999-9999-4999-8999-999999999999\nspaceId: space-9\nconfigSet: apifox\ncli:\n  app: sharkly\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertContains(t, r.Check(repo), "config/apifox/agents/loki：缺 super-agent/capabilities/platforms/sharkly/loki.yaml")
}

// 平台指针的 name 与 config 声明不同名 = 注册表说的 agent 与 config 部署的
// agent 悄悄变成两个，派活解析会对不上。
func TestCheckFlagsPlatformNameMismatch(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/platforms/hifox/wuyong.yaml", "key: wuyong\nname: 无名\nspace: HiFox\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertContains(t, r.Check(repo), `platforms/hifox/wuyong.yaml：name 是 "无名"，config/hifox/agents/wuyong/agent.yaml 里是 "吴用"`)
}

// config 里的平台名允许带英文后缀（「巴德尔 Baldur」），声明名取主干不算漂移——
// 与 hifox.NameMatches 同一判定，否则 7 个双名 agent 全成假漂移。
func TestCheckAcceptsConfigNameWithEnglishSuffix(t *testing.T) {
	repo := stubRepo(t)
	withApifoxEnv(t, repo)
	writeFile(t, repo, "config/apifox/agents/baldur/agent.yaml", "key: baldur\nname: 巴德尔 Baldur\n")
	writeFile(t, repo, "super-agent/capabilities/platforms/hifox/baldur.yaml", "key: baldur\nname: 巴德尔\nspace: AF\n")
	writeFile(t, repo, "environments/dev/state.yaml",
		"version: 1\nagents:\n  wuyong:\n    id: agent-1\n    deployedName: 吴用\n  baldur:\n    id: agent-2\n    deployedName: 巴德尔 Baldur\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := r.Check(repo); len(got) != 0 {
		t.Errorf("Check 应为空, got %v", got)
	}
}

// 同一个 key 被两个平台覆盖（将来 sharkly）时两边各一份指针，不算漂移。
func TestCheckAcceptsSameKeyOnTwoPlatforms(t *testing.T) {
	repo := stubRepo(t)
	withApifoxEnv(t, repo)
	writeFile(t, repo, "super-agent/capabilities/platforms/sharkly/wuyong.yaml", "key: wuyong\nname: 吴用\nspace: HF\n")
	writeFile(t, repo, "environments/sharkly/environment.yaml",
		"organizationId: 99999999-9999-4999-8999-999999999999\nspaceId: space-9\nconfigSet: hifox\ncli:\n  app: sharkly\n")
	writeFile(t, repo, "environments/sharkly/state.yaml",
		"version: 1\nagents:\n  wuyong:\n    id: agent-9\n    deployedName: 吴用\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := r.Check(repo); len(got) != 0 {
		t.Errorf("两平台各一份指针不该报漂移, got %v", got)
	}
	if got := platformSet(r); len(got) != 2 || !got["hifox"] || !got["sharkly"] {
		t.Errorf("平台集合 = %v", got)
	}
}

// 声明了却没有任何环境绑它 = 声明跑在了部署前面；环境只绑部分 agent 是常态，
// 所以别处有记录的条目不报。部署记录按该环境的 cli.app 归到对应平台。
func TestCheckFlagsPlatformWithoutDeployment(t *testing.T) {
	repo := stubRepo(t)
	withApifoxEnv(t, repo)
	writeFile(t, repo, "super-agent/capabilities/platforms/hifox/loki.yaml", "key: loki\nname: 洛基\nspace: AF\n")
	writeFile(t, repo, "config/apifox/agents/loki/agent.yaml", "key: loki\nname: 洛基 Loki\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	problems := r.Check(repo)
	assertContains(t, problems, "platforms/hifox/loki.yaml：environments/ 下没有任何部署记录")
	for _, p := range problems {
		if strings.Contains(p, "platforms/hifox/wuyong.yaml") {
			t.Errorf("有部署记录的条目不该报漂移: %s", p)
		}
	}
}

// 部署记录算在「该环境所在平台」名下：sharkly 环境的部署不能让 hifox 指针免检。
func TestCheckDeploymentIsPerPlatform(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/platforms/sharkly/loki.yaml", "key: loki\nname: 洛基\nspace: AF\n")
	writeFile(t, repo, "config/apifox/agents/loki/agent.yaml", "key: loki\nname: 洛基 Loki\n")
	// sharkly 得有环境接入，否则「这条指针没部署」只是「平台还没接」的噪声（不报）。
	writeFile(t, repo, "environments/sharkly/environment.yaml",
		"organizationId: 99999999-9999-4999-8999-999999999999\nspaceId: space-9\nconfigSet: apifox\ncli:\n  app: sharkly\n")
	writeFile(t, repo, "environments/dev/state.yaml",
		"version: 1\nagents:\n  wuyong:\n    id: agent-1\n    deployedName: 吴用\n  loki:\n    id: agent-2\n    deployedName: 洛基 Loki\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertContains(t, r.Check(repo), "platforms/sharkly/loki.yaml：environments/ 下没有任何部署记录")
}

// org 必须是完整 UUID：slug 喂给 HIFOX_ORGANIZATION_ID 会被平台判 404，
// 派活时才发现就晚了（2026-11 实测 rayfox → ORGANIZATION_NOT_FOUND）。
func TestCheckFlagsOrgSlug(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/platforms/hifox/wuyong.yaml",
		"key: wuyong\nname: 吴用\nspace: HF\norg: rayfox\nnote: 统筹入口\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertContains(t, r.Check(repo), `platforms/hifox/wuyong.yaml：org 是 "rayfox"，必须写平台组织完整 UUID`)

	writeFile(t, repo, "super-agent/capabilities/platforms/hifox/wuyong.yaml",
		"key: wuyong\nname: 吴用\nspace: HF\norg: 86555f76-3f5b-4f7a-93c6-406074110a59\nnote: 统筹入口\n")
	r, err = Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, p := range r.Check(repo) {
		if strings.Contains(p, "platforms/hifox/wuyong.yaml") {
			t.Errorf("UUID 不该报漂移: %s", p)
		}
	}
}

// isOrgUUID 的边界：只认完整的 8-4-4-4-12 十六进制，slug/缺段/长度错都拦下。
func TestIsOrgUUID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"完整 UUID", "86555f76-3f5b-4f7a-93c6-406074110a59", true},
		{"slug", "rayfox", false},
		{"非 hex", "86555f76-3f5b-4f7a-93c6-406074110a5z", false},
		{"分组长度错", "86555f7-3f5b-4f7a-93c6-406074110a59", false},
		{"缺段", "86555f76-3f5b-4f7a-93c6", false},
	}
	for _, c := range cases {
		if got := isOrgUUID(c.in); got != c.want {
			t.Errorf("%s: isOrgUUID(%q) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

// 环境的 organizationId 会被当 HIFOX_ORGANIZATION_ID 透传给平台 CLI，写 slug 时
// 只能以「agent list 失败」的形式冒出来——离线就该点名根因。
func TestCheckFlagsEnvironmentOrgSlug(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "environments/dev/environment.yaml",
		"organizationId: rayfox\nspaceId: space-1\nconfigSet: hifox\ncli:\n  app: hifox\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertContains(t, r.Check(repo), `environments/dev/environment.yaml：organizationId 是 "rayfox"，必须是平台组织完整 UUID`)
}

// 没有 state.yaml 的环境（examples/ 模板）是「未部署」，不是漂移。
func TestCheckToleratesEnvironmentWithoutState(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "environments/example/environment.yaml",
		"organizationId: 11111111-1111-4111-8111-111111111111\nspaceId: space-1\nconfigSet: hifox\ncli:\n  app: hifox\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := r.Check(repo); len(got) != 0 {
		t.Errorf("无 state.yaml 的环境不该报漂移: %v", got)
	}
}

// 目录级漂移：platforms/<平台>/<key>.yaml 的 key 与文件名不符要以目录为准回落。
func TestPlatformKeyMismatch(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/platforms/hifox/wuyong.yaml",
		"key: someoneelse\nname: 吴用\nspace: HiFox\nnote: 统筹入口\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p, ok := r.PlatformByKey("hifox", "wuyong"); !ok || p.Platform != "hifox" {
		t.Fatalf("key 未回落到文件名: %+v %v", p, ok)
	}
	assertContains(t, r.Check(repo), "与文件名不符")
}
