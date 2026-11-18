package feishu

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// DefaultMediaRetention 是落盘附件的保留期：够复现、够转派即可，不长期囤用户的原图。
const DefaultMediaRetention = 30 * 24 * time.Hour

// downloadTimeout 限制单条资源的下载：一张图卡住不该把整条注入拖死。
const downloadTimeout = 60 * time.Second

// mediaSweepInterval 两次过期清理的最小间隔（清理只在本进程内节流，够用）。
const mediaSweepInterval = 24 * time.Hour

// resourceGetter 是 im/v1 messages/:message_id/resources/:file_key 的窄接口：
// 生产走官方 SDK（tenant token 生命周期由它管），测试给假的。
type resourceGetter interface {
	Get(ctx context.Context, messageID, fileKey, resType string) (io.Reader, string /*contentType*/, error)
}

type sdkResourceGetter struct{ client *lark.Client }

func (g sdkResourceGetter) Get(ctx context.Context, messageID, fileKey, resType string) (io.Reader, string, error) {
	req := larkim.NewGetMessageResourceReqBuilder().
		MessageId(messageID).
		FileKey(fileKey).
		Type(resType).
		Build()
	resp, err := g.client.Im.V1.MessageResource.Get(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("feishu resource %s: %w", fileKey, err)
	}
	if !resp.Success() {
		return nil, "", fmt.Errorf("feishu resource %s: code=%d msg=%s request_id=%s",
			fileKey, resp.Code, resp.Msg, resp.RequestId())
	}
	return resp.File, resp.ApiResp.Header.Get("Content-Type"), nil
}

// mediaStore 把消息里的图片/文件落到本地，注入文本里只出现路径（ADR 0024）：
// 主控不必「看图」，派活时把路径当附件传下去就行；落盘也留下可复现的证据。
//
// 目录 0700、文件 0600，不进 git；超过保留期的文件在下次保存时顺手清掉。
//
// 飞书 SDK 对每条消息起一个 goroutine，Save 会被并发调用：swept 由 sweepMu
// 保护，节流判断与置位在同一临界区里，并发到达时最多一个 goroutine 做清理。
type mediaStore struct {
	dir       string
	retention time.Duration
	get       resourceGetter
	now       func() time.Time
	sweepMu   sync.Mutex
	swept     time.Time
}

func newMediaStore(dir string, get resourceGetter, retention time.Duration) *mediaStore {
	if retention <= 0 {
		retention = DefaultMediaRetention
	}
	return &mediaStore{dir: dir, get: get, retention: retention, now: time.Now}
}

// Save 下载一个资源到 <dir>/<message_id>/<key>.<ext>，返回本地路径。
// 失败只是返回错误：调用方保留 key 占位，绝不因为附件丢整条消息。
func (s *mediaStore) Save(ctx context.Context, messageID string, m mediaRef) (string, error) {
	s.sweepIfDue()
	r, contentType, err := s.get.Get(ctx, messageID, m.Key, m.Kind)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(s.dir, safeName(messageID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, safeName(m.Key)+"."+extFor(contentType, m.Name))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, nil
}

// sweepIfDue 删掉超过保留期的落盘附件。清理失败不阻塞下载。
func (s *mediaStore) sweepIfDue() {
	now := s.now()
	s.sweepMu.Lock()
	if !s.swept.IsZero() && now.Sub(s.swept) < mediaSweepInterval {
		s.sweepMu.Unlock()
		return
	}
	s.swept = now
	s.sweepMu.Unlock()
	deadline := now.Add(-s.retention)
	err := filepath.WalkDir(s.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || !info.ModTime().Before(deadline) {
			return nil
		}
		if rerr := os.Remove(path); rerr != nil {
			slog.Warn("feishu: 清理过期附件失败", "path", path, "error", rerr)
		}
		return nil
	})
	if err != nil {
		slog.Warn("feishu: 清理过期附件失败", "dir", s.dir, "error", err)
	}
}

// extFor 定扩展名：优先响应头里的 content-type（飞书下载不给文件名），
// 其次消息体里的 file_name，最后兜底 .bin。
func extFor(contentType, name string) string {
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	switch ct {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "image/bmp":
		return "bmp"
	}
	if ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), "."); ext != "" && len(ext) <= 8 {
		return ext
	}
	if exts, err := mime.ExtensionsByType(ct); err == nil && len(exts) > 0 {
		return strings.TrimPrefix(exts[0], ".")
	}
	return "bin"
}

// safeName 把消息 id / 资源 key 收敛成安全的单层文件名：它来自平台，但会直接
// 参与拼路径，不留 ../ 之类的机会。
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
		if b.Len() >= 96 {
			break
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		out = "resource"
	}
	return out
}
