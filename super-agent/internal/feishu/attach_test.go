package feishu

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGetter 冒充 im/v1 资源接口：记下请求参数，按 key 给内容。
// Save 会被并发调用（SDK 每条消息一个 goroutine），记录侧必须自己加锁。
type fakeGetter struct {
	mu    sync.Mutex
	calls []string
	body  string
	ctype string
	err   error
	// errByKey 按资源 key 报错：一条消息多张图时可以造「第一张失败、第二张成功」。
	errByKey map[string]error
	// readErr 让响应体读到一半报错（模拟 200 后连接中断）。
	readErr error
}

// errReader 先吐一部分再报错，用于测 io.Copy 的失败分支。
type errReader struct {
	body string
	err  error
	read bool
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, r.err
	}
	r.read = true
	n := copy(p, r.body)
	return n, nil
}

func (g *fakeGetter) Get(_ context.Context, messageID, fileKey, resType string) (io.Reader, string, error) {
	g.mu.Lock()
	g.calls = append(g.calls, messageID+"|"+fileKey+"|"+resType)
	body, ctype, err, readErr := g.body, g.ctype, g.err, g.readErr
	if e, ok := g.errByKey[fileKey]; ok {
		err = e
	}
	g.mu.Unlock()
	if err != nil {
		return nil, "", err
	}
	if readErr != nil {
		return &errReader{body: body, err: readErr}, ctype, nil
	}
	return strings.NewReader(body), ctype, nil
}

// 落盘：路径按消息分目录、扩展名按 content-type（飞书下载不给文件名），权限收在 0700/0600。
func TestMediaStoreSave(t *testing.T) {
	dir := t.TempDir()
	g := &fakeGetter{body: "IMGDATA", ctype: "image/jpeg"}
	s := newMediaStore(dir, g, 0)

	path, err := s.Save(context.Background(), "om_x1", mediaRef{Kind: "image", Key: "img_v3_abc"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "om_x1", "img_v3_abc.jpg")
	if path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "IMGDATA" {
		t.Fatalf("落盘内容不对: %q %v", data, err)
	}
	if len(g.calls) != 1 || g.calls[0] != "om_x1|img_v3_abc|image" {
		t.Fatalf("请求参数不对: %v", g.calls)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("文件权限 = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("目录权限 = %o, want 700", perm)
	}
}

// 扩展名：飞书不给文件名时认 content-type；给了 file_name 时用它；都不认就 .bin。
func TestExtFor(t *testing.T) {
	cases := []struct{ ctype, name, want string }{
		{"image/jpeg", "", "jpg"},
		{"image/png; charset=binary", "", "png"},
		{"application/octet-stream", "崩溃日志.txt", "txt"},
		{"", "clip.mp4", "mp4"},
		// application/octet-stream 命中的是 mime 扩展表里的 .bin，不是最后的兜底分支；
		// 真正测兜底要用注册表里没有的类型，否则 return "bin" 那行永远不执行。
		{"application/octet-stream", "", "bin"},
		{"application/x-nope", "", "bin"},
	}
	for _, c := range cases {
		if got := extFor(c.ctype, c.name); got != c.want {
			t.Errorf("extFor(%q, %q) = %q, want %q", c.ctype, c.name, got, c.want)
		}
	}
}

// 下载失败必须原样上报：调用方据此保留 key 占位，不能让附件失败变成静默丢消息。
func TestMediaStoreSaveReportsFailure(t *testing.T) {
	s := newMediaStore(t.TempDir(), &fakeGetter{err: errors.New("boom")}, 0)
	if _, err := s.Save(context.Background(), "om_x1", mediaRef{Kind: "image", Key: "k"}); err == nil {
		t.Fatal("下载失败必须返回错误")
	}
}

// 过期清理：超保留期的落盘附件被删，未过期的留着；节流后同一进程内不重复扫。
func TestMediaStoreSweepsExpiredFiles(t *testing.T) {
	dir := t.TempDir()
	g := &fakeGetter{body: "x", ctype: "image/png"}
	now := time.Now()
	s := newMediaStore(dir, g, 24*time.Hour)
	s.now = func() time.Time { return now }

	old, err := s.Save(context.Background(), "om_old", mediaRef{Kind: "image", Key: "k_old"})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.Save(context.Background(), "om_new", mediaRef{Kind: "image", Key: "k_new"})
	if err != nil {
		t.Fatal(err)
	}
	// 把旧文件的时间推到保留期之外，再把时钟推过清理间隔（真实触发路径，不直接改
	// 私有字段：节流判断本身也要被这条用例盖住）。
	past := now.Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	now = now.Add(mediaSweepInterval + time.Hour)
	// 未过期的那份要按推进后的时钟重新落一次修改时间：文件 mtime 是真实时钟写的，
	// 只推 now 会让它也显得过期（那是夹具问题，不是清理逻辑）。
	if err := os.Chtimes(fresh, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(context.Background(), "om_new2", mediaRef{Kind: "image", Key: "k_new2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("过期附件应被删掉: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("未过期附件不该被删: %v", err)
	}
}

// 资源 key 直接参与拼路径：目录穿越类的 key 不能逃出落盘根目录。
func TestSafeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"img_v3_abc-def", "img_v3_abc-def"},
		{"../../etc/passwd", "_.._etc_passwd"},
		{"", "resource"},
		{".", "resource"},
	}
	for _, c := range cases {
		if got := safeName(c.in); got != c.want {
			t.Errorf("safeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 非图片资源（文件/录屏/语音）走的是接口的 type=file 分支：写死成 image 会让
// 日志与录屏要么被 API 拒绝、要么注入里没有可转派的本地路径。
func TestMediaStoreSaveFileKind(t *testing.T) {
	dir := t.TempDir()
	g := &fakeGetter{body: "LOGDATA", ctype: "text/plain"}
	s := newMediaStore(dir, g, 0)

	path, err := s.Save(context.Background(), "om_f1", mediaRef{Kind: "file", Key: "file_v2_9", Name: "崩溃日志.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "om_f1", "file_v2_9.txt"); path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
	// 接口的 type 参数必须是 file，不是 image。
	if len(g.calls) != 1 || g.calls[0] != "om_f1|file_v2_9|file" {
		t.Fatalf("请求参数不对: %v", g.calls)
	}
	// 占位改写：文件走 localToken 的文件分支，不是 [图片 …]。
	text := replaceMediaToken("[文件 崩溃日志.txt file_v2_9]", mediaRef{Kind: "file", Key: "file_v2_9", Name: "崩溃日志.txt"}, path)
	if want := "[文件 " + path + "]"; text != want {
		t.Fatalf("文件占位改写 = %q, want %q", text, want)
	}
}

// 清理节流：保留期内的两次 Save 只该扫一次（ADR 0024 承诺「同一进程最多每天扫一次」）。
func TestMediaStoreSweepsAtMostOncePerInterval(t *testing.T) {
	dir := t.TempDir()
	g := &fakeGetter{body: "x", ctype: "image/png"}
	base := time.Now()
	s := newMediaStore(dir, g, 24*time.Hour)
	s.now = func() time.Time { return base }

	// 造一个过期文件，观察它是否被扫掉。
	old, err := s.Save(context.Background(), "om_old", mediaRef{Kind: "image", Key: "k_old"})
	if err != nil {
		t.Fatal(err)
	}
	past := base.Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	// 同一时刻的第二次 Save：距上次清理 0 秒，不该再扫，过期文件仍在。
	if _, err := s.Save(context.Background(), "om_2", mediaRef{Kind: "image", Key: "k2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("节流期内不该重复清理（过期文件还在）: %v", err)
	}
	// 隔了一天：又该扫了。
	s.now = func() time.Time { return base.Add(mediaSweepInterval + time.Hour) }
	if _, err := s.Save(context.Background(), "om_3", mediaRef{Kind: "image", Key: "k3"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("过了节流间隔应重新清理: %v", err)
	}
}

// 飞书 SDK 对每条消息起一个 goroutine：Save 会被并发调用，共享状态（清理节流）
// 必须无数据竞争。注意：验证这条用例要带 -race（README 的验证命令已固定），
// 普通 go test 下它只能证明并发保存不报错。
func TestMediaStoreConcurrentSave(t *testing.T) {
	dir := t.TempDir()
	base := time.Now()
	s := newMediaStore(dir, &fakeGetter{body: "x", ctype: "image/png"}, 24*time.Hour)
	var mu sync.Mutex
	s.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		base = base.Add(time.Minute)
		return base
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			msg := fmt.Sprintf("om_c%d", n)
			if _, err := s.Save(context.Background(), msg, mediaRef{Kind: "image", Key: fmt.Sprintf("k%d", n)}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("并发 Save 失败: %v", err)
	}
}

// 保留期零值/负值走 DefaultMediaRetention（30 天）：这条兜底被删时，deadline 会等于
// now，第一次清理就把所有历史附件删光。
func TestMediaStoreRetentionDefault(t *testing.T) {
	for _, r := range []time.Duration{0, -time.Hour} {
		s := newMediaStore(t.TempDir(), &fakeGetter{}, r)
		if s.retention != DefaultMediaRetention {
			t.Fatalf("retention(%v) = %v, want %v", r, s.retention, DefaultMediaRetention)
		}
	}
}

// 下载读到一半断开：Save 必须报错、不能返回可用路径（截断的图片会被当成完整附件）。
func TestMediaStoreSaveReportsReadError(t *testing.T) {
	dir := t.TempDir()
	s := newMediaStore(dir, &fakeGetter{
		body:    "0123456789",
		ctype:   "image/png",
		readErr: errors.New("连接中断"),
	}, 0)
	path, err := s.Save(context.Background(), "om_r", mediaRef{Kind: "image", Key: "k_r"})
	if err == nil {
		t.Fatalf("读流失败必须报错，got path=%q", path)
	}
}

// 无 file_name 时靠 mime 表定扩展名：application/pdf → pdf（删掉回退分支会变 .bin）。
// 不用 video/mp4 断言：系统 mime 表对它的首选扩展是 .m4v，钉死平台相关顺序没有意义。
func TestExtForMimeRegistry(t *testing.T) {
	if got := extFor("application/pdf", ""); got != "pdf" {
		t.Fatalf("application/pdf 应得 pdf, got %q", got)
	}
}
