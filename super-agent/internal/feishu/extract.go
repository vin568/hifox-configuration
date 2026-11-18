package feishu

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// mentionInfo is the flattened mention data the extractor needs: the
// placeholder key ("@_user_1"), the mentioned id, and the display name.
type mentionInfo struct {
	Key  string
	ID   string
	Name string
}

// mediaRef is one downloadable resource embedded in a message body: the key the
// message-resource endpoint takes plus the type it must be asked for.
type mediaRef struct {
	Kind string // "image" | "file"（endpoint 的 type 参数）
	Key  string // 消息体里的 image_key / file_key
	Name string // file_name（消息体带才有）
}

// token 是它在注入文本里的占位。带 key 是为了下载后能按同一 key 原地改写成
// 本地路径：key 在一条消息里唯一，替换不会误伤正文。
func (m mediaRef) token() string {
	switch {
	case m.Kind == "image":
		return "[图片 " + m.Key + "]"
	case m.Name != "":
		return "[文件 " + m.Name + " " + m.Key + "]"
	default:
		return "[文件 " + m.Key + "]"
	}
}

// localToken 是下载成功后的占位：人读得懂，派活时当附件路径原样带上。
func (m mediaRef) localToken(path string) string {
	if m.Kind == "image" {
		return "[图片 " + path + "]"
	}
	return "[文件 " + path + "]"
}

// replaceMediaToken 把消息文本里某条资源的 key 占位改写成它的本地路径。
func replaceMediaToken(text string, m mediaRef, path string) string {
	return strings.ReplaceAll(text, m.token(), m.localToken(path))
}

// extractMessage turns a Feishu message body into the plain text injected into
// the orchestrator, plus the media resources the body embeds (document order).
//
//   - msg_type "text": the {"text": "..."} field, with @_user_N placeholders
//     rewritten to @Name (or @ID when the name is absent).
//   - "post" rich text: the text runs concatenated (paragraphs newline-separated);
//     img / file / media / audio elements stay in place as keyed placeholders,
//     code_block becomes a fence, md keeps its markdown (inline ![alt](img_…)
//     images become placeholders, <at …> becomes @Name). content_v2 wins over
//     content: the md tag only lives there (Feishu docs recommend it).
//   - "interactive" cards and anything else with JSON: a degraded plain-text
//     summary collected from "text" fields.
//   - pure media (image / file / video / audio): the keyed placeholder, so the
//     download step has something to resolve; a video keeps its file_key rather
//     than the cover image_key. Only a body with neither text nor key degrades
//     to "[媒体消息]".
//
// content is the JSON-serialized message content string the event carries.
func extractMessage(msgType, content string, mentions []mentionInfo) (string, []mediaRef) {
	byKey := make(map[string]mentionInfo, len(mentions))
	byID := make(map[string]string, len(mentions))
	for _, m := range mentions {
		// key 空（md 形态的消息没有 @_user_N 占位）不能进表：ReplaceAll(s, "", x)
		// 会在每个字符之间插一遍，整条正文被打碎。
		if m.Key != "" {
			byKey[m.Key] = m
		}
		if m.ID != "" {
			byID[m.ID] = m.Name
		}
	}
	rewrite := func(s string) string {
		// md 形态（content_v2）的 @ 长这样：<at user_id="ou_x">Tom</at>。
		// 不换掉，注入里就是一段 XML，主控读不出是谁。
		s = atTagRe.ReplaceAllStringFunc(s, func(tag string) string {
			p := atTagRe.FindStringSubmatch(tag)
			name := strings.TrimSpace(p[2])
			if name == "" {
				name = byID[p[1]]
			}
			if p[1] == "all" {
				return "@所有人"
			}
			if name == "" {
				name = p[1]
			}
			return "@" + name
		})
		// Sort keys longest-first so @_user_10 is not mangled by @_user_1.
		keys := make([]string, 0, len(byKey))
		for k := range byKey {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
		for _, k := range keys {
			m := byKey[k]
			label := m.Name
			if label == "" {
				label = m.ID
			}
			if label == "" {
				label = "成员"
			}
			s = strings.ReplaceAll(s, k, "@"+label)
		}
		return s
	}

	switch msgType {
	case "text":
		var body struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(content), &body) == nil {
			return rewrite(body.Text), nil
		}
		return rewrite(strings.Trim(content, `"`)), nil

	case "post":
		var body struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal([]byte(content), &body)
		var lines []string
		var media []mediaRef
		if body.Title != "" {
			lines = append(lines, rewrite(body.Title))
		}
		// Post bodies are locale-keyed: {"content": [[...]]} or {"zh_cn": {"content": ...}}.
		// 新版消息还带一份同形的 content_v2，飞书文档明确「md 标签只在 content_v2，
		// 推荐优先读 content_v2；没有 md 标签时两份相同」——所以 v2 在前，content 兜底。
		var raw map[string]json.RawMessage
		if json.Unmarshal([]byte(content), &raw) != nil {
			return "[媒体消息]", nil
		}
		var extra []string
		for _, src := range []json.RawMessage{raw["content_v2"], raw["content"]} {
			if extra, media = postParts(src, rewrite); !emptyPost(extra, media) {
				break
			}
		}
		if emptyPost(extra, media) {
			// Locale-keyed shape: {"zh_cn": {"title": ..., "content": ...}}.
			// Pick the first locale object that carries content.
			for _, v := range raw {
				var loc struct {
					Title     string          `json:"title"`
					Content   json.RawMessage `json:"content"`
					ContentV2 json.RawMessage `json:"content_v2"`
				}
				if json.Unmarshal(v, &loc) != nil {
					continue
				}
				locLines, locMedia := postParts(loc.ContentV2, rewrite)
				if emptyPost(locLines, locMedia) {
					locLines, locMedia = postParts(loc.Content, rewrite)
				}
				if emptyPost(locLines, locMedia) {
					continue
				}
				if loc.Title != "" {
					lines = append(lines, rewrite(loc.Title))
				}
				extra, media = locLines, locMedia
				break
			}
		}
		lines = append(lines, extra...)
		out := strings.Join(lines, "\n")
		if strings.TrimSpace(out) == "" {
			return "[媒体消息]", media
		}
		return out, media

	default:
		// Cards and unknown types: degraded plain-text summary from any "text"
		// leaf; a body with no text falls back to the media it carries (pure
		// image / file / audio messages), and only then to the placeholder.
		var summary []string
		collectTextLeaves(content, &summary)
		if len(summary) > 0 {
			return rewrite(strings.Join(summary, " ")), nil
		}
		media := mediaOf(content)
		if len(media) == 0 {
			return "[媒体消息]", nil
		}
		tokens := make([]string, 0, len(media))
		for _, m := range media {
			tokens = append(tokens, m.token())
		}
		return strings.Join(tokens, "\n"), media
	}
}

// postParts 解析一段富文本段落数组，返回逐段文本与其中的资源。
// 缺失/null/形状不对都返回空（由调用方决定要不要换下一份内容兜底）。
func postParts(raw json.RawMessage, rewrite func(string) string) ([]string, []mediaRef) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var lines []string
	var media []mediaRef
	if err := collectPostBlocks(raw, &lines, &media, rewrite); err != nil {
		return nil, nil
	}
	return lines, media
}

// emptyPost 判定一段富文本是不是「什么都没有」：只有空行也算空。
func emptyPost(lines []string, media []mediaRef) bool {
	if len(media) > 0 {
		return false
	}
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return false
		}
	}
	return true
}

// mediaOf reads the resource keys a pure-media message body carries
// ({"image_key": ...} / {"file_key": ..., "file_name": ...}). file_key 优先：
// 视频消息（msg_type=media）的 content 同时带视频 file_key 和封面 image_key，
// 取 image_key 会把视频换成一张封面图（HF-16095 之外实测到的坑）。
func mediaOf(content string) []mediaRef {
	var body map[string]any
	if json.Unmarshal([]byte(content), &body) != nil {
		return nil
	}
	str := func(k string) string {
		s, _ := body[k].(string)
		return s
	}
	if key := str("file_key"); key != "" {
		return []mediaRef{{Kind: "file", Key: key, Name: str("file_name")}}
	}
	if key := str("image_key"); key != "" {
		return []mediaRef{{Kind: "image", Key: key}}
	}
	return nil
}

// fenced 把代码块原文包成 markdown 围栏（语言可缺省）。
func fenced(lang, text string) string {
	return "```" + strings.ToLower(lang) + "\n" + text + "\n```"
}

// mdImageRe 与 atTagRe 是 md 标签里的两种内联标记：图片 ![alt](image_key) 与
// @ 提及 <at user_id="ou_x">Name</at>。飞书扩展语法，不是普通 markdown。
var (
	mdImageRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)
	atTagRe   = regexp.MustCompile(`<at user_id="([^"]*)">([^<]*)</at>`)
)

func markdownMedia(text string, media *[]mediaRef) string {
	return mdImageRe.ReplaceAllStringFunc(text, func(m string) string {
		parts := mdImageRe.FindStringSubmatch(m)
		if len(parts) != 3 || !strings.HasPrefix(parts[2], "img_") {
			return m
		}
		ref := mediaRef{Kind: "image", Key: parts[2]}
		*media = append(*media, ref)
		return ref.token()
	})
}

// collectPostBlocks walks the rich-text paragraph array appending one line per
// paragraph with its text runs joined. Media elements keep their place in the
// line as keyed placeholders and are reported in media (document order), so the
// download step can resolve them and rewrite the placeholder in place.
func collectPostBlocks(raw json.RawMessage, lines *[]string, media *[]mediaRef, rewrite func(string) string) error {
	var paragraphs [][]json.RawMessage
	if err := json.Unmarshal(raw, &paragraphs); err != nil {
		return err
	}
	for _, para := range paragraphs {
		var runs []string
		for _, el := range para {
			var node map[string]any
			if json.Unmarshal(el, &node) != nil {
				continue
			}
			tag, _ := node["tag"].(string)
			str := func(k string) string {
				s, _ := node[k].(string)
				return s
			}
			switch tag {
			case "text":
				if s := str("text"); s != "" {
					runs = append(runs, s)
				}
			case "a":
				if s := str("text"); s != "" {
					runs = append(runs, s)
				}
				if s := str("href"); s != "" {
					runs = append(runs, s)
				}
			case "at":
				// The mention placeholder is rendered by rewrite; fall back to
				// a bare @ when the key is unknown.
				if s := str("user_id"); s != "" {
					runs = append(runs, s)
				}
			case "img":
				if key := str("image_key"); key != "" {
					ref := mediaRef{Kind: "image", Key: key}
					*media = append(*media, ref)
					runs = append(runs, ref.token())
				} else {
					runs = append(runs, "[图片]")
				}
			case "file", "media", "audio":
				if key := str("file_key"); key != "" {
					ref := mediaRef{Kind: "file", Key: key, Name: str("file_name")}
					*media = append(*media, ref)
					runs = append(runs, ref.token())
				} else {
					runs = append(runs, "[文件]")
				}
			case "emotion":
				if s := str("emoji_type"); s != "" {
					runs = append(runs, "[表情 "+s+"]")
				}
			case "code_block":
				// 日志/堆栈就在代码块里：整块原样保留成围栏，丢弃等于把复现材料吃掉。
				if s := str("text"); s != "" {
					runs = append(runs, fenced(str("language"), s))
				}
			case "md":
				// md 标签只出现在 content_v2（飞书文档：content 里它被转成 text/img）。
				// 它的正文自带 markdown，图片以 ![alt](image_key) 内联——两种都得接住，
				// 否则「优先用 content、兜底用 v2」的兜底路径会静默吞掉正文与图。
				if s := str("text"); s != "" {
					runs = append(runs, markdownMedia(s, media))
				}
			case "hr":
				runs = append(runs, "---")
			}
		}
		*lines = append(*lines, rewrite(strings.Join(runs, "")))
	}
	return nil
}

// collectTextLeaves recursively gathers every JSON string field named "text".
func collectTextLeaves(raw string, out *[]string) {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return
	}
	var walk func(any)
	walk = func(n any) {
		switch t := n.(type) {
		case map[string]any:
			if s, ok := t["text"].(string); ok {
				*out = append(*out, s)
			}
			for _, child := range t {
				walk(child)
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		}
	}
	walk(v)
}

// buildTextContent is the JSON body of an im/v1 create-message request for a
// plain text message; kept separate so the payload is testable without the SDK.
func buildTextContent(text string) string {
	b, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		// text is a map[string]string value; Marshal cannot fail here.
		return fmt.Sprintf(`{"text":%q}`, text)
	}
	return string(b)
}
