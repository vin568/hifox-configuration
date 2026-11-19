package capability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 共享技能根 + agent 专属技能根都要交给 CLI；共享根里的技能只有被声明了才可见。
// 直接把真源目录交出去，不复制——SKILL.md 本身就是标准形态。
func TestSkillMounts(t *testing.T) {
	repo := t.TempDir()
	writeSkill(t, repo, "super-agent/capabilities/skills/platform-dispatch", "platform-dispatch", "平台派活", "p")
	writeSkill(t, repo, "super-agent/capabilities/skills/agent-report", "agent-report", "回报主控", "r")
	writeSkill(t, repo, "super-agent/capabilities/agents/songjiang/skills/direct-tiers", "direct-tiers", "直连三级", "d")

	shared := filepath.Join(repo, "super-agent", "capabilities", "skills")
	own := filepath.Join(repo, "super-agent", "capabilities", "agents", "songjiang", "skills")

	// 声明了 platform-dispatch：共享根挂上，未声明的 agent-report 被排除。
	m := SkillMounts(repo, Agent{Key: "songjiang", Skills: []string{"platform-dispatch"}})
	if len(m.Roots) != 2 || m.Roots[0] != shared || m.Roots[1] != own {
		t.Fatalf("songjiang 的技能根 = %v, want [%s %s]", m.Roots, shared, own)
	}
	if len(m.Ignored) != 1 || m.Ignored[0] != "agent-report" {
		t.Fatalf("songjiang 应只排除未声明的 agent-report: %v", m.Ignored)
	}

	// 没有专属目录、也没声明的 agent：共享根根本不进发现面（不写 ignored，避免按
	// 名字误伤用户自己的同名技能）。
	m = SkillMounts(repo, Agent{Key: "zhugui"})
	if len(m.Roots) != 0 || len(m.Ignored) != 0 {
		t.Fatalf("未声明任何共享技能时不该挂共享根: %+v", m)
	}

	// 只声明一个：共享根挂上，另一个进排除清单。
	m = SkillMounts(repo, Agent{Key: "zhugui", Skills: []string{"agent-report"}})
	if len(m.Roots) != 1 || m.Roots[0] != shared {
		t.Fatalf("部分声明时共享根应挂上: %v", m.Roots)
	}
	if strings.Join(m.Ignored, ",") != "platform-dispatch" {
		t.Fatalf("未声明的共享技能应被排除: %v", m.Ignored)
	}

	// 全部声明：没有排除项。
	m = SkillMounts(repo, Agent{Key: "zhugui", Skills: []string{"agent-report", "platform-dispatch"}})
	if len(m.Ignored) != 0 {
		t.Fatalf("全部声明时不该有排除项: %v", m.Ignored)
	}
}

// 空目录不进发现面：一个没有 SKILL.md 的目录会让 CLI 白扫一次，也掩盖「技能没写对」。
func TestSkillMountsSkipsEmpty(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "super-agent", "capabilities", "skills", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if m := SkillMounts(repo, Agent{Key: "songjiang"}); len(m.Roots) != 0 || len(m.Ignored) != 0 {
		t.Fatalf("空技能目录不该进发现面: %+v", m)
	}
}

// 覆盖配置含 customDirectories 与 ignoredSkills；没有排除项时不写空段。
func TestWriteOverlay(t *testing.T) {
	out := t.TempDir()
	path, err := WriteOverlay(out, SkillMount{
		Roots:   []string{"/repo/skills", "/repo/agents/x/skills"},
		Ignored: []string{"agent-report"},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "skills:\n  customDirectories:\n    - /repo/skills\n    - /repo/agents/x/skills\n  ignoredSkills:\n    - agent-report\n"
	if string(data) != want {
		t.Fatalf("overlay = %q, want %q", data, want)
	}

	// 没有排除项时不留空标题。
	path, err = WriteOverlay(out, SkillMount{Roots: []string{"/repo/skills"}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "ignoredSkills") {
		t.Fatalf("没有排除项不该写 ignoredSkills:\n%s", data)
	}
}

// SKILL.md 的 frontmatter 是唯一身份来源：缺 name/description 的必须报漂移，
// 因为底层 CLI 会整份跳过它（装了也看不见）。
func TestCheckSkillFrontmatter(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/skills/broken/SKILL.md", "---\ndescription: 只有描述\n---\n\n# broken\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, r.Check(repo), "skills/broken")
}

func TestCheckSkillMissingFile(t *testing.T) {
	repo := stubRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "super-agent/capabilities/skills/empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, r.Check(repo), "缺 SKILL.md")
}

// 同名技能在共享与专属两处声明：底层按 name 注册，重名只有一个能被裸名加载。
func TestCheckDuplicateSkillName(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/skills/dup/SKILL.md",
		"---\nname: platform-dispatch\ndescription: 另一个同名\n---\n\n# dup\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, r.Check(repo), "技能名都是")
}

// frontmatter 只认 name/description，多写字段不该让解析失败（omp 也忽略它们）。
func TestParseFrontmatterToleratesExtraFields(t *testing.T) {
	name, desc, err := parseFrontmatter("---\nname: x\ndescription: y\nversion: 2\n---\nbody\n")
	if err != nil || name != "x" || desc != "y" {
		t.Fatalf("parseFrontmatter = %q %q %v", name, desc, err)
	}
	if _, _, err := parseFrontmatter("no frontmatter"); err == nil {
		t.Fatal("没有 frontmatter 应报错")
	}
	if _, _, err := parseFrontmatter("---\nname: x\n"); err == nil {
		t.Fatal("未闭合应报错")
	}
}

// description 里的冒号/引号要安全转义（写进 overlay 路径时同理）。
func TestYAMLScalar(t *testing.T) {
	cases := []struct{ in, want string }{
		{"简单描述", "简单描述"},
		{"有: 冒号", `"有: 冒号"`},
		{`有"引号"`, `"有\"引号\""`},
		{"多  空白\n换行", "多 空白 换行"},
		{"", `""`},
	}
	for _, c := range cases {
		if got := yamlScalar(c.in); got != c.want {
			t.Errorf("yamlScalar(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 技能名带非 kebab-case 字符时 omp 会拒收；注册表侧提前发现，别等启动才静默失效。
func TestSkillNameIsKebabCase(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/skills/bad/SKILL.md",
		"---\nname: Bad Name\ndescription: d\n---\n\n# bad\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, r.Check(repo), "kebab-case")
}

// ensureTopo 在每次注入前都跑：覆盖配置内容没变就不能重写（否则每次注入都动一次
// 状态目录里的文件，白白制造 mtime 抖动与变更检测噪音）。
func TestWriteOverlaySkipsIdentical(t *testing.T) {
	dir := t.TempDir()
	mount := SkillMount{Roots: []string{"/repo/skills", "/repo/agents/x/skills"}}
	path, err := WriteOverlay(dir, mount)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteOverlay(dir, mount); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(old) {
		t.Fatalf("内容没变却重写了覆盖配置: %v -> %v", old, st.ModTime())
	}
	// 根变了就必须真的写。
	if _, err := WriteOverlay(dir, SkillMount{Roots: []string{"/repo/skills"}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "agents/x") {
		t.Fatalf("根变化时应重写:\n%s", data)
	}
}

// 缺 description：底层 CLI 整份跳过，--check 是唯一能看见的地方。name 有、description
// 没有是比缺 name 更隐蔽的一种（名字能显示，技能却加载不了）。
func TestCheckSkillMissingDescription(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/skills/nodesc/SKILL.md",
		"---\nname: nodesc\n---\n\n# nodesc\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, r.Check(repo), "缺 description")
}

// SKILL.md 的 frontmatter 读不出来（无 --- / 未闭合 / 坏 YAML）时，这条技能会从
// 索引里静默消失；--check 必须把它报成漂移——这是 ADR 0025 说的唯一可见处。
func TestCheckSkillBadFrontmatter(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"missing", "# 没有 frontmatter\n", "frontmatter"},
		{"unclosed", "---\nname: x\n", "frontmatter"},
		{"bad-yaml", "---\nname: [bad\n---\n\n# x\n", "frontmatter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := stubRepo(t)
			writeFile(t, repo, "super-agent/capabilities/skills/broken/SKILL.md", c.body)
			r, err := Load(repo)
			if err != nil {
				t.Fatal(err)
			}
			assertContains(t, r.Check(repo), "skills/broken")
			assertContains(t, r.Check(repo), c.want)
		})
	}
}

// 专属技能的漂移标签要带 owner 前缀，别让「哪个 agent 的哪份技能坏了」丢失。
func TestCheckSkillBadFrontmatterOwned(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/agents/songjiang/skills/broken/SKILL.md", "# 没有 frontmatter\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, r.Check(repo), "agents/songjiang/skills/broken")
}

// 空 roots 是调用方的错：写一份「指向零个目录」的覆盖配置只会让人以为技能挂上了。
func TestWriteOverlayRejectsEmptyRoots(t *testing.T) {
	if _, err := WriteOverlay(t.TempDir(), SkillMount{}); err == nil {
		t.Fatal("空 roots 应报错")
	}
}

// 共享根与 agent 专属根的同名技能：底层按 name 注册，重名时裸名只剩一个。
func TestCheckDuplicateSkillNameAcrossRoots(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/agents/songjiang/skills/platform-dispatch/SKILL.md",
		"---\nname: platform-dispatch\ndescription: 专属同名\n---\n\n# dup\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	problems := strings.Join(r.Check(repo), "\n")
	for _, want := range []string{"skills/platform-dispatch", "agents/songjiang/skills/platform-dispatch"} {
		if !strings.Contains(problems, want) {
			t.Fatalf("重名错误应同时指出两处 %s:\n%s", want, problems)
		}
	}
}

// 纯大写（无空格）也必须报 kebab-case：底层不加载，空格不是唯一失败原因。
func TestSkillNameUppercaseRejected(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/skills/bad/SKILL.md",
		"---\nname: PlatformDispatch\ndescription: d\n---\n\n# bad\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, r.Check(repo), "kebab-case")
}

// 索引按目录 key 显示、omp 按 frontmatter name 注册：两者不一致时索引给出的名字
// 加载不到，必须报漂移。
func TestCheckSkillNameDiffersFromDir(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/skills/mismatch/SKILL.md",
		"---\nname: other-name\ndescription: d\n---\n\n# x\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, r.Check(repo), "与目录名")
}

// 索引必须打印底层注册用的名字（frontmatter name），不是目录名：目录名与 name
// 不一致时 --check 虽报漂移，索引也不能给出一个加载不到的名字。
func TestIndexPrintsFrontmatterName(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/skills/dir-key/SKILL.md",
		"---\nname: skill-name\ndescription: d\n---\n\n# x\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	idx := r.Index()
	if !strings.Contains(idx, "- skill-name（共享 → 未指派）") {
		t.Fatalf("索引应打印 frontmatter name:\n%s", idx)
	}
	if strings.Contains(idx, "- dir-key（共享 → 未指派）") {
		t.Fatalf("索引不该打印目录名:\n%s", idx)
	}
}

// 空 skills.yaml 等于不挂共享技能，不是解析失败：先建空文件再填是常态。
func TestDeclaredSkillsEmptyFile(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/agents/songjiang/skills.yaml", "\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Check(repo); len(got) != 0 {
		t.Fatalf("空声明不该报漂移: %v", got)
	}
}

// 只有注释的 skills.yaml 也该视为空，而不是解析失败。
func TestDeclaredSkillsCommentOnly(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/agents/songjiang/skills.yaml", "# 暂时不挂共享技能\n")
	r, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Check(repo); len(got) != 0 {
		t.Fatalf("只有注释的声明不该报漂移: %v", got)
	}
}

// 坏 YAML 是硬错误：写了却没生效必须当场看见，而不是静默当成没写。
func TestDeclaredSkillsMalformed(t *testing.T) {
	repo := stubRepo(t)
	writeFile(t, repo, "super-agent/capabilities/agents/songjiang/skills.yaml", "[bad\n")
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "skills.yaml") {
		t.Fatalf("坏 skills.yaml 必须报错: %v", err)
	}
}

// 声明了共享根里不存在的技能：不挂共享根（不能静默把别的技能挂上）。
func TestSkillMountsDeclaredMissing(t *testing.T) {
	repo := t.TempDir()
	writeSkill(t, repo, "super-agent/capabilities/skills/agent-report", "agent-report", "回报主控", "r")
	m := SkillMounts(repo, Agent{Key: "worker", Skills: []string{"missing"}})
	if len(m.Roots) != 0 || len(m.Ignored) != 0 {
		t.Fatalf("未知声明不该挂共享根: %+v", m)
	}
}
