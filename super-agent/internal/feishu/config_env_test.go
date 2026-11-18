package feishu

import (
	"testing"
)

// 密钥卫生（INV-1）：凭证只从 SONGJIANG_FEISHU_* 环境变量来，缺失必须
// fail-fast（空凭证静默流进 SDK 会变成难排查的连接失败）。
func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("SONGJIANG_FEISHU_APP_ID", "cli_env_id")
	t.Setenv("SONGJIANG_FEISHU_APP_SECRET", "env_secret")

	t.Run("env fills empty fields", func(t *testing.T) {
		cfg, err := LoadConfigFromEnv(Config{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.AppID != "cli_env_id" || cfg.AppSecret != "env_secret" {
			t.Fatalf("env fallback wrong: %+v", cfg)
		}
	})

	t.Run("explicit values win over env", func(t *testing.T) {
		cfg, err := LoadConfigFromEnv(Config{AppID: "cli_explicit", AppSecret: "explicit_secret"})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.AppID != "cli_explicit" || cfg.AppSecret != "explicit_secret" {
			t.Fatalf("explicit credentials must not be overridden: %+v", cfg)
		}
	})

	t.Run("missing credentials error", func(t *testing.T) {
		t.Setenv("SONGJIANG_FEISHU_APP_ID", "")
		t.Setenv("SONGJIANG_FEISHU_APP_SECRET", "")
		if _, err := LoadConfigFromEnv(Config{}); err == nil {
			t.Fatal("missing credentials must fail fast")
		}
	})
}

func TestCredentialsFromEnv(t *testing.T) {
	t.Setenv("SONGJIANG_FEISHU_APP_ID", "cli_x")
	t.Setenv("SONGJIANG_FEISHU_APP_SECRET", "s")
	if id, secret := CredentialsFromEnv(); id != "cli_x" || secret != "s" {
		t.Fatalf("CredentialsFromEnv = %q %q", id, secret)
	}
}
