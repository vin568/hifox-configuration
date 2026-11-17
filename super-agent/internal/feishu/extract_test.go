package feishu

import "testing"

func TestExtractText(t *testing.T) {
	mentions := []mentionInfo{
		{Key: "@_user_1", ID: "ou_alice", Name: "Alice"},
		{Key: "@_user_2", ID: "ou_bob"}, // no display name
		{Key: "@_user_3"},               // neither name nor id
		{Key: "@_user_10", ID: "ou_carol", Name: "Carol"},
	}

	tests := []struct {
		name    string
		msgType string
		content string
		want    string
	}{
		{
			"plain text",
			"text",
			`{"text":"hello world"}`,
			"hello world",
		},
		{
			"text with mention rewrite",
			"text",
			`{"text":"hi @_user_1 and @_user_2"}`,
			"hi @Alice and @ou_bob",
		},
		{
			// @_user_10 不能被 @_user_1 的替换打碎（最长键优先）。
			"longest mention key wins",
			"text",
			`{"text":"hi @_user_10 and @_user_1"}`,
			"hi @Carol and @Alice",
		},
		{
			"mention with neither name nor id degrades to placeholder",
			"text",
			`{"text":"hi @_user_3"}`,
			"hi @成员",
		},
		{
			"post rich text plain shape",
			"post",
			`{"title":"T","content":[[{"tag":"text","text":"a"},{"tag":"text","text":"b"}],[{"tag":"text","text":"c"}]]}`,
			"T\nab\nc",
		},
		{
			"post rich text locale shape",
			"post",
			`{"zh_cn":{"title":"你好","content":[[{"tag":"text","text":"第一行"},{"tag":"at","user_id":"@_user_1"}]]}}`,
			"你好\n第一行@Alice",
		},
		{
			"card degrades to text summary",
			"interactive",
			`{"elements":[{"tag":"div","text":{"tag":"lark_md","text":"卡片内容"}}]}`,
			"卡片内容",
		},
		{
			"pure media placeholder",
			"image",
			`{"image_key":"img_v2_123"}`,
			"[媒体消息]",
		},
		{
			"post with no text runs",
			"post",
			`{"content":[[{"tag":"img","image_key":"x"}]]}`,
			"[媒体消息]",
		},
		{
			"invalid json text degrades",
			"text",
			`not-json`,
			"not-json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractText(tt.msgType, tt.content, mentions)
			if got != tt.want {
				t.Fatalf("extractText(%s) = %q, want %q", tt.msgType, got, tt.want)
			}
		})
	}
}

func TestBuildTextContent(t *testing.T) {
	got := buildTextContent("你好\nworld")
	want := `{"text":"你好\nworld"}`
	if got != want {
		t.Fatalf("buildTextContent = %s, want %s", got, want)
	}
}
