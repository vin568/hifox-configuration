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

// stubRepo 造一个最小但完整的能力目录（master + retro + 技能 + 可执行脚本 + 云端指针）。
func stubRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()

	writeFile(t, repo, "super-agent/capabilities/agents/songjiang/capability.yaml", agentYAML("songjiang", "master"))
	writeFile(t, repo, "super-agent/capabilities/agents/songjiang/instructions.md", "# 宋江\n")
	writeFile(t, repo, "super-agent/capabilities/agents/jiangjing/capability.yaml", agentYAML("jiangjing", "retro"))
	writeFile(t, repo, "super-agent/capabilities/agents/jiangjing/instructions.md", "# 蒋敬\n")

	writeFile(t, repo, "super-agent/capabilities/skills/cloud-dispatch/capability.yaml",
		"key: cloud-dispatch\nname: 云端派活\ndescription: d\nwhenToUse: w\n")
	writeFile(t, repo, "super-agent/capabilities/skills/cloud-dispatch/doc.md", "# 云端派活\n")

	writeFile(t, repo, "super-agent/capabilities/scripts/probe.yaml",
		"key: probe\nname: 探针\ndescription: d\npath: super-agent/direct/probe.sh\n")
	script := writeFile(t, repo, "super-agent/direct/probe.sh", "#!/bin/sh\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	writeFile(t, repo, "super-agent/capabilities/cloud/wuyong.yaml",
		"key: wuyong\nname: 吴用\nspace: HiFox\nnote: 统筹入口\n")
	writeFile(t, repo, "config/hifox/agents/wuyong/agent.yaml", "key: wuyong\n")

	return repo
}

func TestLoadOK(t *testing.T) {
	repo := stubRepo(t)
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(r.Agents) != 2 || len(r.Skills) != 1 || len(r.Scripts) != 1 || len(r.Cloud) != 1 {
		t.Fatalf("条目数不对: agents=%d skills=%d scripts=%d cloud=%d",
			len(r.Agents), len(r.Skills), len(r.Scripts), len(r.Cloud))
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
	if r.Skills[0].Doc != filepath.Join(repo, "super-agent", "capabilities", "skills", "cloud-dispatch", "doc.md") {
		t.Errorf("Skill.Doc = %q", r.Skills[0].Doc)
	}
	if r.Cloud[0].Space != "HiFox" || r.Cloud[0].Note != "统筹入口" {
		t.Errorf("Cloud = %+v", r.Cloud[0])
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
	if err := os.Remove(filepath.Join(repo, "super-agent/capabilities/skills/cloud-dispatch/doc.md")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	r, err := Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertContains(t, r.Check(repo), "skills/cloud-dispatch")
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

func TestCloudPointerMissing(t *testing.T) {
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

func TestCloudPointerInApifox(t *testing.T) {
	repo := stubRepo(t)
	if err := os.RemoveAll(filepath.Join(repo, "config", "hifox", "agents", "wuyong")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	writeFile(t, repo, "config/apifox/agents/wuyong/agent.yaml", "key: wuyong\n")
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
	writeFile(t, repo, "super-agent/capabilities/cloud/wuyong.yaml", "key: [unterminated\n")
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
		"## 云端能力", "## 本地能力", "## 技能", "## 直连脚本",
		"songjiang", "jiangjing", "cloud-dispatch", "probe", "wuyong",
	} {
		if !strings.Contains(idx, want) {
			t.Errorf("Index 缺少 %q:\n%s", want, idx)
		}
	}
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
