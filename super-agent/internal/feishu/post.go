package feishu

import "encoding/json"

// postPayload 是 im/v1 富文本消息的请求体。用结构体而不是拼字符串：
// 正文里的引号、换行、中文都由 json.Marshal 负责转义。
type postPayload struct {
	ZhCn postLocale `json:"zh_cn"`
}

type postLocale struct {
	Title   string       `json:"title"`
	Content [][]postElem `json:"content"`
}

type postElem struct {
	Tag      string `json:"tag"`
	Text     string `json:"text,omitempty"`
	ImageKey string `json:"image_key,omitempty"`
}

// buildPostContent 造一条「正文 + 图片」的富文本：第一段是文字（为空则只发图），
// 其后每张图一段。飞书 post 的图片元素 tag 是 img，字段是 image_key。
func buildPostContent(text string, imageKeys []string) (string, error) {
	content := make([][]postElem, 0, len(imageKeys)+1)
	if text != "" {
		content = append(content, []postElem{{Tag: "text", Text: text}})
	}
	for _, key := range imageKeys {
		content = append(content, []postElem{{Tag: "img", ImageKey: key}})
	}
	b, err := json.Marshal(postPayload{ZhCn: postLocale{Content: content}})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// buildImageContent 是单图消息（msg_type=image）的请求体。
func buildImageContent(imageKey string) string {
	b, err := json.Marshal(map[string]string{"image_key": imageKey})
	if err != nil {
		// imageKey 是 string；Marshal 不会失败，兜底返回同形 JSON。
		return `{"image_key":""}`
	}
	return string(b)
}
