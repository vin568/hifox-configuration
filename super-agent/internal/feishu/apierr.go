package feishu

import "fmt"

// APIError 保留飞书业务错误码，供调用方按码给中文下一步。
// 文案保持 "op: code=… msg=… request_id=…"，与现有日志格式一致，便于按 request_id 报障。
type APIError struct {
	Op        string
	Code      int
	Msg       string
	RequestID string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: code=%d msg=%s request_id=%s", e.Op, e.Code, e.Msg, e.RequestID)
}
