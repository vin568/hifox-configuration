package main

import (
	"encoding/json"
	"os"
)

// readJSONFile / writeJSONFile 是运行时小型状态文件的统一读写（告警去重、监督
// 去重）。read 失败按零值处理——状态文件损坏不该让巡检整轮失效；write 走临时
// 文件 + rename，半截文件不会被下一轮当成空状态。
func readJSONFile(path string, v any) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return
	}
	_ = json.Unmarshal(data, v)
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
