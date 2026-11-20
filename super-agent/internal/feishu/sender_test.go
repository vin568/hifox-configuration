package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeAPI records calls without touching the network and implements the three
// operations the Sender seam needs.
type fakeAPI struct {
	calls     []string // "chatID|msgType|content" per send
	uploads   []string // paths uploaded
	recalls   []string // message ids recalled
	sendErr   error
	failAt    int // 1-based upload index that fails; 0 = none
	recallErr error
	block     bool // uploadImage blocks until ctx is done
}

func (f *fakeAPI) send(_ context.Context, chatID, msgType, content string) (string, error) {
	if f.sendErr != nil {
		return "", f.sendErr
	}
	f.calls = append(f.calls, chatID+"|"+msgType+"|"+content)
	return "om_sent", nil
}

func (f *fakeAPI) uploadImage(ctx context.Context, path string) (string, error) {
	if f.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	f.uploads = append(f.uploads, path)
	if f.failAt > 0 && len(f.uploads) == f.failAt {
		return "", errors.New("upload boom: " + path)
	}
	return "img_" + strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/"), nil
}

func (f *fakeAPI) recall(_ context.Context, messageID string) error {
	f.recalls = append(f.recalls, messageID)
	return f.recallErr
}

func TestDeliverTextUnchanged(t *testing.T) {
	api := &fakeAPI{}
	s := &Sender{api: api}
	id, err := s.Deliver(context.Background(), "chat_1", "hi 你好", nil)
	if err != nil || id != "om_sent" {
		t.Fatalf("Deliver = %q, %v", id, err)
	}
	want := `chat_1|text|{"text":"hi 你好"}`
	if len(api.calls) != 1 || api.calls[0] != want {
		t.Fatalf("calls = %v, want %s", api.calls, want)
	}
}

func TestDeliverImageOnly(t *testing.T) {
	api := &fakeAPI{}
	s := &Sender{api: api}
	if _, err := s.Deliver(context.Background(), "chat_1", "", []string{"/tmp/a.png"}); err != nil {
		t.Fatal(err)
	}
	if len(api.calls) != 1 || !strings.HasPrefix(api.calls[0], "chat_1|image|") {
		t.Fatalf("calls = %v", api.calls)
	}
	var body struct {
		ImageKey string `json:"image_key"`
	}
	content := strings.SplitN(api.calls[0], "|", 3)[2]
	if err := json.Unmarshal([]byte(content), &body); err != nil || body.ImageKey != "img_tmp/a.png" {
		t.Fatalf("image content = %s, err=%v", content, err)
	}
}

func TestDeliverPost(t *testing.T) {
	api := &fakeAPI{}
	s := &Sender{api: api}
	if _, err := s.Deliver(context.Background(), "chat_1", "结论", []string{"/a.png", "/b.jpg"}); err != nil {
		t.Fatal(err)
	}
	if len(api.calls) != 1 || !strings.HasPrefix(api.calls[0], "chat_1|post|") {
		t.Fatalf("calls = %v", api.calls)
	}
	var body struct {
		ZhCn struct {
			Title   string `json:"title"`
			Content [][]struct {
				Tag      string `json:"tag"`
				Text     string `json:"text"`
				ImageKey string `json:"image_key"`
			} `json:"content"`
		} `json:"zh_cn"`
	}
	content := strings.SplitN(api.calls[0], "|", 3)[2]
	if err := json.Unmarshal([]byte(content), &body); err != nil {
		t.Fatalf("post content %s: %v", content, err)
	}
	if body.ZhCn.Title != "" || len(body.ZhCn.Content) != 3 {
		t.Fatalf("post shape = %+v", body.ZhCn)
	}
	if body.ZhCn.Content[0][0].Tag != "text" || body.ZhCn.Content[0][0].Text != "结论" {
		t.Fatalf("first paragraph = %+v", body.ZhCn.Content[0])
	}
	if body.ZhCn.Content[1][0].Tag != "img" || body.ZhCn.Content[2][0].ImageKey == "" {
		t.Fatalf("image paragraphs = %+v / %+v", body.ZhCn.Content[1], body.ZhCn.Content[2])
	}
}

// 全有或全无：任一上传失败，一条都不发。
func TestDeliverUploadFailureSendsNothing(t *testing.T) {
	api := &fakeAPI{failAt: 2}
	s := &Sender{api: api}
	_, err := s.Deliver(context.Background(), "chat_1", "正文", []string{"/a.png", "/b.png"})
	if err == nil || !strings.Contains(err.Error(), "/b.png") {
		t.Fatalf("err = %v, want upload failure with path", err)
	}
	if len(api.calls) != 0 {
		t.Fatalf("must not send after failed upload: %v", api.calls)
	}
}

func TestSenderDeliverTimeout(t *testing.T) {
	s := &Sender{api: &fakeAPI{block: true}, timeout: 20 * time.Millisecond}
	_, err := s.Deliver(context.Background(), "chat_1", "", []string{"/a.png"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func TestSenderRecall(t *testing.T) {
	api := &fakeAPI{}
	s := &Sender{api: api}
	if err := s.Recall(context.Background(), "om_x"); err != nil {
		t.Fatal(err)
	}
	if len(api.recalls) != 1 || api.recalls[0] != "om_x" {
		t.Fatalf("recalls = %v", api.recalls)
	}

	api = &fakeAPI{recallErr: &APIError{Op: "feishu recall", Code: 230009, Msg: "expired", RequestID: "r1"}}
	s = &Sender{api: api}
	err := s.Recall(context.Background(), "om_x")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 230009 {
		t.Fatalf("err = %v, want APIError 230009", err)
	}
}

// post 里图片顺序必须与入参一致（互换 key 不报错会让用户看到错图）。
func TestDeliverPostPreservesImageOrder(t *testing.T) {
	api := &fakeAPI{}
	s := &Sender{api: api}
	if _, err := s.Deliver(context.Background(), "chat_1", "结论", []string{"/a.png", "/b.jpg", "/c.gif"}); err != nil {
		t.Fatal(err)
	}
	content := strings.SplitN(api.calls[0], "|", 3)[2]
	var body struct {
		ZhCn struct {
			Content [][]struct {
				ImageKey string `json:"image_key"`
			} `json:"content"`
		} `json:"zh_cn"`
	}
	if err := json.Unmarshal([]byte(content), &body); err != nil {
		t.Fatal(err)
	}
	want := []string{"img_a.png", "img_b.jpg", "img_c.gif"}
	for i, key := range want {
		if body.ZhCn.Content[i+1][0].ImageKey != key {
			t.Fatalf("paragraph %d key = %q, want %q", i, body.ZhCn.Content[i+1][0].ImageKey, key)
		}
	}
}

// 发送失败必须原样传给调用方（Drain 据此计次重试）。
func TestDeliverSendErrorPropagates(t *testing.T) {
	s := &Sender{api: &fakeAPI{sendErr: errors.New("boom")}}
	if _, err := s.Deliver(context.Background(), "chat_1", "x", nil); err == nil {
		t.Fatal("expected send error")
	}
}
