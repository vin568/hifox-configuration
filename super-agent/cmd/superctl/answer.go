package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"super-agent/internal/hifox"
)

// answerClient 按平台解析回答通道的客户端工厂：平台 = 平台 CLI 应用名。平台 →
// 客户端的映射只有 platformClientFor 一处；这里只管回答动作的措辞与测试接缝。
var answerClient = func(platformName string) (func(org string) *hifox.Client, error) {
	clientFor, ok := platformClientFor(platformName)
	if !ok {
		return nil, fmt.Errorf("平台 %q 还没有回答实现", platformName)
	}
	return clientFor, nil
}

// cmdAnswer 把用户的回答投进平台任务的对话（不是任务评论）。
//
// 这条通道服务一个固定场景：承接人（平台任务执行 agent）在任务对话里提问并把
// 会话置为 waiting_human_reply，用户在飞书回话后，主控把他的原话送进那条对话。
// 平台把消息记成用户本人的一轮（role=user），承接人因此被唤醒；任务评论不产生
// 回合，写在那里等于没答。命令面故意没有评论选项（ADR 0031）。
func cmdAnswer(args []string) error {
	fs := flag.NewFlagSet("answer", flag.ContinueOnError)
	platformName := fs.String("platform", "", "平台：平台 CLI 应用名（目前只支持 hifox）")
	task := fs.String("task", "", "平台任务号（如 HF-16187）")
	text := fs.String("text", "", "用户的回答原文（原样投递，不改写）")
	textFile := fs.String("text-file", "", "回答正文文件（多行；与 --text 二选一）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	*platformName = strings.TrimSpace(*platformName)
	if *platformName == "" {
		return errors.New("answer 需要 --platform <平台名>（目前只有 hifox）")
	}
	if strings.TrimSpace(*task) == "" {
		return errors.New("answer 需要 --task <任务号>")
	}
	body, err := answerBody(*text, *textFile)
	if err != nil {
		return err
	}
	clientFor, err := answerClient(*platformName)
	if err != nil {
		return err
	}
	out, err := answerTask(context.Background(), clientFor, *task, body)
	if err != nil {
		return err
	}
	out["platform"] = *platformName
	return jsonPrint(out)
}

// answerBody 要求恰好一个正文来源：--text 与 --text-file 都给/都不给都是调用错误。
// 末尾换行会 Trim 掉——用户的消息不带「文件结尾」语义。
func answerBody(text, file string) (string, error) {
	text, file = strings.TrimSpace(text), strings.TrimSpace(file)
	if (text == "") == (file == "") {
		return "", errors.New("answer 需要恰好一个 --text 或 --text-file")
	}
	if file == "" {
		return text, nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("读回答文件: %w", err)
	}
	body := strings.TrimSpace(string(data))
	if body == "" {
		return "", errors.New("回答文件是空的")
	}
	return body, nil
}

// answerTask 先取任务真值（会话 id 与组织都从平台读，调用方不解析平台 ref），
// 再把回答写进那条对话。会话按 Task.AnswerSessionID 选：会话汇总里最该推进的那条
// 优先，否则任务主会话——多会话任务里投错会话，承接人不会被唤醒，补的话也进不了
// 它当前的上下文。
// 返回投递结果与投递前的 agent 状态，供主控写台账、判断要不要补一句
// 「排在当前回合后面」。
func answerTask(ctx context.Context, clientFor func(org string) *hifox.Client, taskID, text string) (map[string]any, error) {
	task, err := clientFor("").TaskView(ctx, taskID)
	if err != nil {
		return nil, err
	}
	session := task.AnswerSessionID()
	if session == "" {
		return nil, fmt.Errorf("任务 %s 没有可投的对话会话（会话状态无 top，chatSessionId 也为空）：先确认承接人已在任务对话里跑过；不要改用任务评论", taskID)
	}
	msg, err := clientFor(task.OrganizationID).PostUserMessage(ctx, session, text)
	if err != nil {
		return nil, err
	}
	id := task.Identifier
	if id == "" {
		id = taskID
	}
	return map[string]any{
		"task_id":              id,
		"session_id":           session,
		"message_id":           msg.MessageID,
		"run_id":               msg.RunID,
		"queued":               msg.Queued,
		"agent_working_status": task.AgentWorkingStatus,
	}, nil
}
