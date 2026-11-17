package feishu

import "testing"

func TestAllow(t *testing.T) {
	tests := []struct {
		name      string
		cfg       Config
		chatType  string
		chatID    string
		senderID  string
		mentions  []string
		wantOK    bool
		wantProxy bool
	}{
		{"dm whitelisted", baseCfg(), "p2p", "", "ou_alice", nil, true, false},
		{"dm not whitelisted", baseCfg(), "p2p", "", "ou_stranger", nil, false, false},
		{"group mentions bot", baseCfg(), "group", "chat_other", "ou_stranger", []string{"ou_bot"}, true, false},
		{"group bot mention disabled when BotMentionOpenID empty", Config{AllowedUserIDs: []string{"ou_alice"}, ProxyReplyGroups: map[string]bool{"chat_proxy": true}, AnyMessageGroups: []string{"chat_any"}}, "group", "chat_other", "ou_stranger", []string{"ou_bot"}, false, false},
		{"proxy group mentions whitelisted", baseCfg(), "group", "chat_proxy", "ou_stranger", []string{"ou_bot", "ou_alice"}, true, true},
		{"proxy group no whitelisted mention", baseCfg(), "group", "chat_proxy", "ou_stranger", []string{"ou_other"}, false, false},
		{"proxy group whitelisted sender without mention dropped", baseCfg(), "group", "chat_proxy", "ou_alice", nil, false, false},
		{"any message group", baseCfg(), "group", "chat_any", "ou_stranger", nil, true, false},
		{"unknown group dropped", baseCfg(), "group", "chat_other", "ou_alice", nil, false, false},
		{"dm whitelisted ignores group membership", baseCfg(), "p2p", "chat_any", "ou_bob", nil, true, false},
		{"proxy mention in any group is not proxy", baseCfg(), "group", "chat_any", "ou_stranger", []string{"ou_alice"}, true, false},
		{"unknown chat type dropped", baseCfg(), "topic", "chat_any", "ou_alice", nil, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, proxy := tt.cfg.allow(tt.chatType, tt.chatID, tt.senderID, tt.mentions)
			if ok != tt.wantOK || proxy != tt.wantProxy {
				t.Fatalf("allow(%s,%s,%s,%v) = (%v,%v), want (%v,%v)",
					tt.chatType, tt.chatID, tt.senderID, tt.mentions, ok, proxy, tt.wantOK, tt.wantProxy)
			}
		})
	}
}
