package feishu

import (
	"encoding/json"
	"fmt"
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

// extractText turns a Feishu message body into the plain text injected into
// the orchestrator.
//
//   - msg_type "text": the {"text": "..."} field, with @_user_N placeholders
//     rewritten to @Name (or @ID when the name is absent).
//   - "post" rich text: the text runs concatenated (paragraphs newline-separated).
//   - "interactive" cards and anything else with JSON: a degraded plain-text
//     summary collected from "text" fields.
//   - pure media (no text anywhere): "[媒体消息]".
//
// content is the JSON-serialized message content string the event carries.
func extractText(msgType, content string, mentions []mentionInfo) string {
	byKey := make(map[string]mentionInfo, len(mentions))
	for _, m := range mentions {
		byKey[m.Key] = m
	}
	rewrite := func(s string) string {
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
			return rewrite(body.Text)
		}
		return rewrite(strings.Trim(content, `"`))

	case "post":
		var body struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal([]byte(content), &body)
		var lines []string
		if body.Title != "" {
			lines = append(lines, rewrite(body.Title))
		}
		// Post bodies are locale-keyed: {"content": [[...]]} or {"zh_cn": {"content": ...}}.
		var raw map[string]json.RawMessage
		if json.Unmarshal([]byte(content), &raw) != nil {
			return "[媒体消息]"
		}
		blocks, ok := raw["content"]
		if !ok {
			// Locale-keyed shape: {"zh_cn": {"title": ..., "content": ...}}.
			// Pick any locale object and use its title and content.
			for _, v := range raw {
				var loc struct {
					Title   string          `json:"title"`
					Content json.RawMessage `json:"content"`
				}
				if json.Unmarshal(v, &loc) != nil {
					continue
				}
				if loc.Title != "" && len(lines) == 0 {
					lines = append(lines, rewrite(loc.Title))
				}
				if loc.Content != nil {
					blocks = loc.Content
					break
				}
			}
		}
		if blocks != nil {
			_ = collectPostBlocks(blocks, &lines, rewrite)
		}
		out := strings.Join(lines, "\n")
		if strings.TrimSpace(out) == "" {
			return "[媒体消息]"
		}
		return out

	default:
		// Cards and unknown types: degraded plain-text summary from any "text"
		// leaf; pure media degrades to the placeholder.
		var summary []string
		collectTextLeaves(content, &summary)
		if len(summary) == 0 {
			return "[媒体消息]"
		}
		return rewrite(strings.Join(summary, " "))
	}
}

// collectPostBlocks walks the rich-text paragraph array appending one line per
// paragraph with its text runs joined.
func collectPostBlocks(raw json.RawMessage, lines *[]string, rewrite func(string) string) error {
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
			switch node["tag"] {
			case "text":
				if s, ok := node["text"].(string); ok {
					runs = append(runs, s)
				}
			case "a":
				if s, ok := node["text"].(string); ok {
					runs = append(runs, s)
				}
				if s, ok := node["href"].(string); ok {
					runs = append(runs, s)
				}
			case "at":
				// The mention placeholder is rendered by rewrite; fall back to
				// a bare @ when the key is unknown.
				if s, ok := node["user_id"].(string); ok {
					runs = append(runs, s)
				}
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
