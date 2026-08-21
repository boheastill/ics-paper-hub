package ics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/boheastill/ics-paper-hub/pkg/db"
)

type Config struct {
	Enabled   bool   `json:"enabled"`
	Channel   string `json:"channel"`
	Device    string `json:"device"`
	TargetURL string `json:"target_url,omitempty"`
}

type Client struct {
	cfg Config
}

func NewClient(cfg Config) *Client {
	if cfg.Channel == "" {
		cfg.Channel = "paper-interaction"
	}
	if cfg.Device == "" {
		cfg.Device = "paper-h5"
	}
	return &Client{cfg: cfg}
}

// NotifyHumanResponse constructs the structured ICS message when a human submits via QR scan
func (c *Client) NotifyHumanResponse(task *db.Task, feedback *db.HumanFeedback) error {
	payloadBytes, _ := json.Marshal(feedback.Payload)

	msgText := fmt.Sprintf(
		"🔔 [ICS-Paper-Hub] 收到人类物理纸张反馈\n"+
			"━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
			"📄 文档标题: %s\n"+
			"🆔 任务编号: %s (Short: #%s)\n"+
			"🤖 目标 Agent: %s (模型: %s)\n"+
			"🏷️ 密级/类别: [%s / %s]\n"+
			"⏰ 提交时间: %s\n"+
			"📱 交互数据: %s\n"+
			"💬 补充意见: %s\n"+
			"━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
			"👉 请 Agent 继续下一轮任务执行与推理。",
		task.Title,
		task.ID,
		task.ShortID,
		task.AgentID,
		task.ModelName,
		task.SecurityLevel,
		task.Category,
		feedback.SubmittedAt.Format("2006-01-02 15:04:05"),
		string(payloadBytes),
		feedback.Comment,
	)

	fmt.Printf("[*] [ICS Dispatch] %s\n", msgText)

	if c.cfg.TargetURL != "" {
		postBody, _ := json.Marshal(map[string]string{
			"channel": c.cfg.Channel,
			"device":  c.cfg.Device,
			"text":    msgText,
		})
		req, err := http.NewRequest("POST", c.cfg.TargetURL, bytes.NewReader(postBody))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			client := &http.Client{Timeout: 5 * time.Second}
			client.Do(req)
		}
	}

	return nil
}
