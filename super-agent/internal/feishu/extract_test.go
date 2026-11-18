package feishu

import (
	"reflect"
	"strings"
	"testing"
)

func TestExtractMessage(t *testing.T) {
	mentions := []mentionInfo{
		{Key: "@_user_1", ID: "ou_alice", Name: "Alice"},
		{Key: "@_user_2", ID: "ou_bob"}, // no display name
		{Key: "@_user_3"},               // neither name nor id
		{Key: "@_user_10", ID: "ou_carol", Name: "Carol"},
		// md 形态的消息没有 @_user_N 占位，key 为空：它若进替换表，
		// ReplaceAll(s, "", x) 会在每个字符之间插一遍，整条正文被打碎。
		{ID: "ou_md", Name: "Md"},
	}

	tests := []struct {
		name      string
		msgType   string
		content   string
		want      string
		wantMedia []mediaRef
	}{
		{
			"plain text",
			"text",
			`{"text":"hello world"}`,
			"hello world",
			nil,
		},
		{
			"text with mention rewrite",
			"text",
			`{"text":"hi @_user_1 and @_user_2"}`,
			"hi @Alice and @ou_bob",
			nil,
		},
		{
			// @_user_10 不能被 @_user_1 的替换打碎（最长键优先）。
			"longest mention key wins",
			"text",
			`{"text":"hi @_user_10 and @_user_1"}`,
			"hi @Carol and @Alice",
			nil,
		},
		{
			"mention with neither name nor id degrades to placeholder",
			"text",
			`{"text":"hi @_user_3"}`,
			"hi @成员",
			nil,
		},
		{
			"post rich text plain shape",
			"post",
			`{"title":"T","content":[[{"tag":"text","text":"a"},{"tag":"text","text":"b"}],[{"tag":"text","text":"c"}]]}`,
			"T\nab\nc",
			nil,
		},
		{
			"post rich text locale shape",
			"post",
			`{"zh_cn":{"title":"你好","content":[[{"tag":"text","text":"第一行"},{"tag":"at","user_id":"@_user_1"}]]}}`,
			"你好\n第一行@Alice",
			nil,
		},
		{
			// 复现 HF-16095 那条消息的形状：文字段落 + 末尾一张图，图片必须留下 key
			// 占位（旧实现静默丢弃，图片从此消失）。
			"post with trailing image keeps the key",
			"post",
			`{"title":"","content":[[{"tag":"text","text":"复现步骤："}],[{"tag":"img","image_key":"img_v3_abc","width":3384,"height":1904}]],"content_v2":[[{"tag":"text","text":"复现步骤："}],[{"tag":"img","image_key":"img_v3_abc"}]]}`,
			"复现步骤：\n[图片 img_v3_abc]",
			[]mediaRef{{Kind: "image", Key: "img_v3_abc"}},
		},
		{
			// content 缺失（或为空）时用 content_v2 兜底：两张都带图，不能双份。
			"content_v2 fallback when content is empty",
			"post",
			`{"content":[],"content_v2":[[{"tag":"text","text":"只有 v2"},{"tag":"img","image_key":"img_v3_v2"}]]}`,
			"只有 v2[图片 img_v3_v2]",
			[]mediaRef{{Kind: "image", Key: "img_v3_v2"}},
		},
		{
			"post file and media elements",
			"post",
			`{"content":[[{"tag":"file","file_key":"file_v2_1","file_name":"log.txt"},{"tag":"media","file_key":"file_v2_2","file_name":"clip.mp4"},{"tag":"audio","file_key":"file_v2_3"}]]}`,
			"[文件 log.txt file_v2_1][文件 clip.mp4 file_v2_2][文件 file_v2_3]",
			[]mediaRef{
				{Kind: "file", Key: "file_v2_1", Name: "log.txt"},
				{Kind: "file", Key: "file_v2_2", Name: "clip.mp4"},
				{Kind: "file", Key: "file_v2_3"},
			},
		},
		{
			// 没有 key 就下载不了，但也不能再静默消失——留一个无 key 占位。
			"post image without key still leaves a marker",
			"post",
			`{"content":[[{"tag":"text","text":"x"},{"tag":"img"}]]}`,
			"x[图片]",
			nil,
		},
		{
			"card degrades to text summary",
			"interactive",
			`{"elements":[{"tag":"div","text":{"tag":"lark_md","text":"卡片内容"}}]}`,
			"卡片内容",
			nil,
		},
		{
			"pure image message carries its key",
			"image",
			`{"image_key":"img_v2_123"}`,
			"[图片 img_v2_123]",
			[]mediaRef{{Kind: "image", Key: "img_v2_123"}},
		},
		{
			"pure file message carries its key",
			"file",
			`{"file_key":"file_v2_9","file_name":"崩溃日志.txt"}`,
			"[文件 崩溃日志.txt file_v2_9]",
			[]mediaRef{{Kind: "file", Key: "file_v2_9", Name: "崩溃日志.txt"}},
		},
		{
			"pure audio message degrades to a file ref",
			"audio",
			`{"file_key":"file_v2_audio","duration":3000}`,
			"[文件 file_v2_audio]",
			[]mediaRef{{Kind: "file", Key: "file_v2_audio"}},
		},
		{
			"post with no text runs",
			"post",
			`{"content":[[{"tag":"img","image_key":"x"}]]}`,
			"[图片 x]",
			[]mediaRef{{Kind: "image", Key: "x"}},
		},
		{
			"unknown media without key still degrades",
			"sticker",
			`{"whatever":1}`,
			"[媒体消息]",
			nil,
		},
		{
			"invalid json text degrades",
			"text",
			`not-json`,
			"not-json",
			nil,
		},
		{
			// 日志/堆栈就在 code_block 里：从前整块静默丢弃（实测只剩「报错：」）。
			"post code_block survives as a fence",
			"post",
			`{"content":[[{"tag":"text","text":"报错："}],[{"tag":"code_block","language":"GO","text":"panic: nil map"}]],"title":""}`,
			"报错：\n```go\npanic: nil map\n```",
			nil,
		},
		{
			"post hr survives as a separator",
			"post",
			`{"content":[[{"tag":"text","text":"a"}],[{"tag":"hr"}],[{"tag":"text","text":"b"}]]}`,
			"a\n---\nb",
			nil,
		},
		{
			// md 标签只在 content_v2，飞书推荐优先读它；正文与内联图片、@ 都要接住。
			"post md tag keeps text, inline image and mention",
			"post",
			`{"content_v2":[[{"tag":"md","text":"## 标题\n\n![img](img_v3_md)\n\n<at user_id=\"ou_alice\">Alice</at> 看这里"}]]}`,
			"## 标题\n\n[图片 img_v3_md]\n\n@Alice 看这里",
			[]mediaRef{{Kind: "image", Key: "img_v3_md"}},
		},
		{
			// 视频消息（msg_type=media）的 content 同时带视频 file_key 与封面
			// image_key：取封面等于把视频弄丢（实测旧实现下载的是封面）。
			"pure video message keeps the video, not the cover",
			"media",
			`{"file_key":"file_v2_video","image_key":"img_v3_cover","file_name":"复现.mp4","duration":2000}`,
			"[文件 复现.mp4 file_v2_video]",
			[]mediaRef{{Kind: "file", Key: "file_v2_video", Name: "复现.mp4"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, media := extractMessage(tt.msgType, tt.content, mentions)
			if got != tt.want {
				t.Fatalf("extractMessage(%s) = %q, want %q", tt.msgType, got, tt.want)
			}
			if !reflect.DeepEqual(media, tt.wantMedia) {
				t.Fatalf("extractMessage(%s) media = %+v, want %+v", tt.msgType, media, tt.wantMedia)
			}
		})
	}
}

// 占位改写：下载成功后注入文本里出现的是路径，key 不再出现。
func TestMediaTokenRewrite(t *testing.T) {
	text, media := extractMessage("post",
		`{"content":[[{"tag":"text","text":"见图"},{"tag":"img","image_key":"img_v3_abc"}]]}`, nil)
	if len(media) != 1 {
		t.Fatalf("应报到 1 个资源: %+v", media)
	}
	got := replaceMediaToken(text, media[0], "/tmp/media/om_1/img_v3_abc.jpg")
	want := "见图[图片 /tmp/media/om_1/img_v3_abc.jpg]"
	if got != want {
		t.Fatalf("改写后 = %q, want %q", got, want)
	}
}

func TestBuildTextContent(t *testing.T) {
	got := buildTextContent("你好\nworld")
	want := `{"text":"你好\nworld"}`
	if got != want {
		t.Fatalf("buildTextContent = %s, want %s", got, want)
	}
}

// md 形态的 @ 是 <at user_id="ou_x">Name</at>，不是 @_user_N 占位：不换掉，
// 注入里就是一段 XML，主控读不出是谁；@all 要落成「所有人」。
func TestMarkdownMentionRewrite(t *testing.T) {
	text, _ := extractMessage("text",
		`{"text":"<at user_id=\"ou_abc\">张三</at> 和 <at user_id=\"all\"></at> 看下"}`, nil)
	want := "@张三 和 @所有人 看下"
	if text != want {
		t.Fatalf("md 提及改写 = %q, want %q", text, want)
	}
}

// content_v2 必须优先于 content（飞书文档：md 标签只在 v2，且推荐优先读它）。
// 两份内容不同的用例才钉得住顺序——两份等价时交换顺序也测不出。
func TestExtractMessagePrefersContentV2(t *testing.T) {
	content := `{
		"content":[[{"tag":"text","text":"降级后的纯文本"}]],
		"content_v2":[[{"tag":"md","text":"## 标题\n\n![截图](img_v3_v2only)"}]]
	}`
	text, media := extractMessage("post", content, nil)
	if !strings.Contains(text, "## 标题") {
		t.Fatalf("应取 content_v2 的 md 正文:\n%s", text)
	}
	if strings.Contains(text, "降级后的纯文本") {
		t.Fatalf("不该退回 content:\n%s", text)
	}
	want := []mediaRef{{Kind: "image", Key: "img_v3_v2only"}}
	if !reflect.DeepEqual(media, want) {
		t.Fatalf("media = %+v, want %+v", media, want)
	}
}

// locale 形态同样以 v2 优先。
func TestExtractMessagePrefersLocaleContentV2(t *testing.T) {
	content := `{"zh_cn":{
		"title":"",
		"content":[[{"tag":"text","text":"旧文本"}]],
		"content_v2":[[{"tag":"md","text":"新文本 ![图](img_v3_l)"}]]
	}}`
	text, media := extractMessage("post", content, nil)
	if !strings.Contains(text, "新文本") || strings.Contains(text, "旧文本") {
		t.Fatalf("locale 形态应取 content_v2:\n%s", text)
	}
	if len(media) != 1 || media[0].Key != "img_v3_l" {
		t.Fatalf("media = %+v", media)
	}
}

// md 里的外链图（真实 URL，不是 image key）原样保留：只把 img_ 前缀的当资源，
// 否则会把普通图片链接误当 key 去下载，报错还留在文本里。
func TestMarkdownExternalImageUntouched(t *testing.T) {
	content := `{"content_v2":[[{"tag":"md","text":"![外链](https://example.com/a.png)"}]]}`
	text, media := extractMessage("post", content, nil)
	if text != "![外链](https://example.com/a.png)" {
		t.Fatalf("外链图应原样保留: %q", text)
	}
	if len(media) != 0 {
		t.Fatalf("外链图不该进资源队列: %+v", media)
	}
}

// emotion 标签保成可见占位（从前落到 default 分支静默消失）。
func TestExtractEmotionLabel(t *testing.T) {
	content := `{"content":[[{"tag":"text","text":"收到"},{"tag":"emotion","emoji_type":"SMILE"}]]}`
	text, _ := extractMessage("post", content, nil)
	if !strings.Contains(text, "[表情 SMILE]") {
		t.Fatalf("表情应有占位: %q", text)
	}
}
