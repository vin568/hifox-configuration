package capability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 共享技能根 + agent 专属技能根都要交给 CLI；专属根只给声明了它的那个 agent。
// 直接把真源目录交出去，不复制——SKILL.md 本身就是标准形态。
func TestSkillRoots(t *testing.T) {
	repo := t.TempDir()
	writeSkill(t, repo, "super-agent/capabilities/skills/platform-dispatch", "platform-dispatch", "平台派活", "p")
	writeSkill(t, repo, "super-agent/capabilities/agents/songjiang/skills/direct-tiers", "direct-tiers", "直连三级", "d")

	shared := filepath.Join(repo, "super-agent", "capabilities", "skills")
	own := filepath.Join(repo, "super-agent", "capabilities", "agents", "songjiang", "skills")

	got := SkillRoots(repo, Agent{Key: "songjiang"})
	if len(got) != 2 || got[0] != shared || got[1] != own {
		t.Fatalf("songjiang 的技能根 = %v, want [%s %s]", got, shared, own)
	}
	// 没有专属目录的 agent 只拿共享根：不能把别人的技能也塞给它。
	if got := SkillRoots(repo, Agent{Key: "zhugui"}); len(got) != 1 || got[0] != shared {
		t.Fatalf("zhugui 的技能根 = %v, want [%s]", got, shared)
	}
}

// 空目录不进发现面：一个没有 SKILL.md 的目录会让 CLI 白扫一次，也掩盖「技能没写对」。
func TestSkillRootsSkipsEmpty(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "super-agent", "capabilities", "skills", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := SkillRoots(repo, Agent{Key: "songjiang"}); len(got) != 0 {
		t.Fatalf("空技能目录不该进发现面: %v", got)
	}
}

// 覆盖配置只含 skills.customDirectories，且多根按顺序写出。
func TestWriteOverlay(t *testing.T) {
	out := t.TempDir()
	path, err := WriteOverlay(out, []string{"/repo/skills", "/repo/agents/x/skills"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "skills:\n  customDirectories:\n    - /repo/skills\n    - /repo/agents/x/skills\n"
	if string(data) != want {
		t.Fatalf("overlay = %q, want %q", data, want)
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
	roots := []string{"/repo/skills", "/repo/agents/x/skills"}
	path, err := WriteOverlay(dir, roots)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteOverlay(dir, roots); err != nil {
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
	if _, err := WriteOverlay(dir, []string{"/repo/skills"}); err != nil {
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
	if _, err := WriteOverlay(t.TempDir(), nil); err == nil {
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
	if !strings.Contains(idx, "- skill-name（共享）") {
		t.Fatalf("索引应打印 frontmatter name:\n%s", idx)
	}
	if strings.Contains(idx, "- dir-key（共享）") {
		t.Fatalf("索引不该打印目录名:\n%s", idx)
	}
}
