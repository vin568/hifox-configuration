package feishu

import (
	"context"
	"errors"
	"testing"
)

// fakeAPI records Send calls without touching the network.
type fakeAPI struct {
	calls    []string // "chatID|msgType|content" per call
	failNext bool
}

func (f *fakeAPI) send(_ context.Context, chatID, msgType, content string) error {
	if f.failNext {
		f.failNext = false
		return errors.New("boom")
	}
	f.calls = append(f.calls, chatID+"|"+msgType+"|"+content)
	return nil
}

func TestSenderSendBuildsPayload(t *testing.T) {
	api := &fakeAPI{}
	s := &Sender{api: api}
	if err := s.Send(context.Background(), "chat_1", "hi 你好"); err != nil {
		t.Fatal(err)
	}
	want := []string{"chat_1|text|{\"text\":\"hi 你好\"}"}
	if len(api.calls) != 1 || api.calls[0] != want[0] {
		t.Fatalf("Send calls = %v, want %v", api.calls, want)
	}
}

func TestSenderPropagatesError(t *testing.T) {
	s := &Sender{api: &fakeAPI{failNext: true}}
	if err := s.Send(context.Background(), "chat_1", "x"); err == nil {
		t.Fatal("expected error propagation")
	}
}
