package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"super-agent/internal/boot"
	"super-agent/internal/config"
	"super-agent/internal/halt"
	"super-agent/internal/herdr"
	"super-agent/internal/inject"
	"super-agent/internal/outbox"
)

// cmdHalt 执行急停：落闸 → 秒级回执 → 换脑（首条注入=急停意图）。
// 解除不在这里：用户的下一句私聊即恢复（ADR 0020）。
func cmdHalt(args []string) error {
	fs := flag.NewFlagSet("halt", flag.ContinueOnError)
	reason := fs.String("reason", "", "急停原因（可选，会写进状态与回执）")
	by := fs.String("by", "cli", "触发者标识")
	cfgPath := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, _, err := loadConfigWithAgents([]string{"--config", *cfgPath})
	if err != nil {
		return err
	}
	ctx := context.Background()
	hc := herdr.New(cfg.Session)
	ob, err := outbox.Open(cfg.OutboxPath())
	if err != nil {
		return err
	}
	rep, err := doHalt(ctx, cfg, hc, ob, *by, *reason, cfg.AdminChatID)
	if err != nil {
		return err
	}
	return jsonPrint(rep)
}

// cmdResume 是离线兜底：清闸门。正常路径是用户下一句私聊（feishu 侧自动清）。
func cmdResume(args []string) error {
	cfg, _, err := loadConfigWithAgents(args)
	if err != nil {
		return err
	}
	st, _, _ := halt.Load(cfg.HaltPath())
	if err := halt.Release(cfg.HaltPath(), halt.Watermark{HaltAt: st.At}); err != nil {
		return err
	}
	fmt.Println("resumed")
	return nil
}

// doHalt 是急停的单一实现，CLI 与飞书指令共用。
func doHalt(ctx context.Context, cfg config.Config, hc *herdr.Client, ob *outbox.Outbox, by, reason, chatID string) (*boot.FreshReport, error) {
	if err := cfg.EnsureDirs(); err != nil {
		return nil, err
	}
	tr := collectTruth(ctx, cfg, hc)

	// 先落闸再换脑：换脑过程中任何自动触发器注入都会被闸门拦下。
	st := halt.State{At: time.Now(), By: by, Reason: reason, Receipt: tr.forMaster(cfg)}
	if cur, ok, _ := halt.Load(cfg.HaltPath()); ok {
		st.OldSession = cur.OldSession
	}
	if err := halt.Set(cfg.HaltPath(), st); err != nil {
		return nil, err
	}

	if chatID != "" {
		if _, err := ob.Append(chatID, haltReceipt(tr.forUser()), nil); err != nil {
			slog.Warn("superctl: halt receipt append failed", "error", err)
		}
	}

	oldSession := currentSession(ctx, hc, cfg.MasterAgent)
	rep, err := freshBrain(ctx, cfg, hc, cfg.MasterAgent, "halt", inject.BuildHaltIntent(tr.forMaster(cfg), oldSession))
	// 即便换脑失败，闸门也留着（fail-closed）：用户已经按了停，不能因为换脑失败就让系统继续跑。
	if err != nil {
		return rep, fmt.Errorf("急停已落闸但换脑失败（系统仍处于停住状态，处理完再 superctl resume）: %w", err)
	}
	st.OldSession, st.NewSession = rep.OldSession, rep.NewSession
	if err := halt.Set(cfg.HaltPath(), st); err != nil {
		return rep, err
	}
	return rep, nil
}

// haltReceipt 是给用户看的秒级回执：只说人话，不出口内部术语（一体人设）。
func haltReceipt(userTruth string) string {
	body := strings.TrimSpace(userTruth)
	if body == "" {
		return "已经停下了，手上没有正在跑的事。你说下一步。"
	}
	return "已经停下了。按停的时候手上是这些：\n" + body + "\n要停哪些，回我一句就行。"
}

// gate 是注入闸门（ADR 0020）。机器注入在急停期间一律静默；白名单私聊放行并解除急停，
// 但落闸之前就排队的旧指令（重试队列）必须丢弃——急停要丢的正是它。
type gate struct {
	cfg config.Config
}

// allowMachine 供 tick/哨兵/事件/群消息使用：急停期间一律不放行。
func (g gate) allowMachine() bool { return !halt.Active(g.cfg.HaltPath()) }

// admit 判定一次飞书注入是否放行。机器注入在急停期间静默；白名单私聊放行并解除急停。
// 落闸之前就排队的旧指令（重试队列携带的那条）一律丢弃——急停要拦的正是它。
// 坏状态文件按已挂闸处理（fail-closed）：用户的这句话可以重置它。
func (g gate) admit(p inject.Payload) bool {
	isUserDM := p.Kind == "dm" && !p.ProxyReply
	if !isUserDM {
		return !halt.Active(g.cfg.HaltPath())
	}
	// 水和位一起判：解除之后 Grace 窗口内，落闸前排队的旧指令仍要丢——
	// 只看「闸门在不在」会让重试队列在解除那一刻畅通无阻。
	if halt.Stale(g.cfg.HaltPath(), p.ReceivedAt, time.Now()) {
		slog.Info("superctl: 丢弃落闸前就排队的旧指令", "chat_id", p.ChatID)
		return false
	}
	st, active, lerr := halt.Load(g.cfg.HaltPath())
	if !active {
		return true
	}
	if lerr == nil && !p.ReceivedAt.IsZero() && p.ReceivedAt.Before(st.At) {
		slog.Info("superctl: 丢弃落闸前就排队的旧指令", "chat_id", p.ChatID)
		return false
	}
	// 坏文件按已挂闸（fail-closed）；用户的这句话可以重置它。
	if err := halt.Release(g.cfg.HaltPath(), halt.Watermark{HaltAt: st.At}); err != nil {
		slog.Error("superctl: 解除急停失败（保持停住）", "error", err)
		return false
	}
	slog.Info("superctl: 用户说话了，急停解除，自动触发器恢复")
	return true
}
