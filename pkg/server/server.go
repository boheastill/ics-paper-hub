package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/boheastill/ics-paper-hub/pkg/db"
	"github.com/boheastill/ics-paper-hub/pkg/ics"
	"github.com/boheastill/ics-paper-hub/pkg/pdfengine"
)

type Config struct {
	Port         int
	DataDir      string
	HostIP       string
	PrintctlPath string
	AutoPrint    bool
	PrinterIP    string
	ICSConfig    ics.Config
}

type Server struct {
	cfg    Config
	store  *db.Store
	icsCli *ics.Client
}

func NewServer(cfg Config) (*Server, error) {
	if cfg.Port == 0 {
		cfg.Port = 3000
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if cfg.HostIP == "" {
		cfg.HostIP = getOutboundIP()
	}
	if cfg.PrintctlPath == "" {
		cfg.PrintctlPath = "printctl"
	}

	store, err := db.NewStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}

	return &Server{
		cfg:    cfg,
		store:  store,
		icsCli: ics.NewClient(cfg.ICSConfig),
	}, nil
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// CORS & Static
	mux.HandleFunc("/api/", s.corsMiddleware(s.handleAPI))
	mux.HandleFunc("/r/", s.handleMobileScan)
	mux.HandleFunc("/dashboard", s.handleDashboard)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/dashboard", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})

	addr := fmt.Sprintf("0.0.0.0:%d", s.cfg.Port)
	fmt.Printf("[*] ics-paper-hub active on http://%s (LAN Host IP: %s)\n", addr, s.cfg.HostIP)
	fmt.Printf("    📱 Mobile QR Landing Base: http://%s:%d/r/<TASK_ID>\n", s.cfg.HostIP, s.cfg.Port)
	fmt.Printf("    🖥️ Desktop Dashboard    : http://%s:%d/dashboard\n", s.cfg.HostIP, s.cfg.Port)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			return
		}
		next(w, r)
	}
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	case path == "/api/tasks/create" && r.Method == http.MethodPost:
		s.handleCreateTask(w, r)
	case path == "/api/tasks" && r.Method == http.MethodGet:
		s.handleListTasks(w, r)
	case strings.HasPrefix(path, "/api/tasks/") && strings.HasSuffix(path, "/pdf"):
		s.handleGetTaskPDF(w, r)
	case strings.HasPrefix(path, "/api/tasks/"):
		s.handleGetTask(w, r)
	case strings.HasPrefix(path, "/api/feedback/") && r.Method == http.MethodPost:
		s.handleSubmitFeedback(w, r)
	default:
		http.NotFound(w, r)
	}
}

type CreateTaskRequest struct {
	Title         string          `json:"title"`
	Content       string          `json:"content"`
	SecurityLevel string          `json:"security_level"` // "公开", "内部", "秘密", "机密", "绝密"
	AgentID       string          `json:"agent_id"`       // "CodeArchitect", "DeepSeek-R1"
	ModelName     string          `json:"model_name"`     // "deepseek-r1"
	Category      string          `json:"category"`       // "技术", "业务", "思考", "私人", "决策", "告警"
	Widgets       []db.FormWidget `json:"widgets"`
	AutoPrint     *bool           `json:"auto_print,omitempty"`
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Title == "" {
		req.Title = "AI 物理交互任务"
	}
	if req.SecurityLevel == "" {
		req.SecurityLevel = "内部"
	}
	if req.AgentID == "" {
		req.AgentID = "AI-Agent"
	}
	if req.ModelName == "" {
		req.ModelName = "deepseek-r1"
	}
	if req.Category == "" {
		req.Category = "技术"
	}

	// Generate IDs
	shortHex := randomHex(2)
	now := time.Now()
	taskID := fmt.Sprintf("task_%s_%s", now.Format("20060102_150405"), shortHex)
	shortID := strings.ToUpper(shortHex)

	qrURL := fmt.Sprintf("http://%s:%d/r/%s", s.cfg.HostIP, s.cfg.Port, taskID)

	task := &db.Task{
		ID:            taskID,
		ShortID:       shortID,
		Title:         req.Title,
		Content:       req.Content,
		SecurityLevel: req.SecurityLevel,
		AgentID:       req.AgentID,
		ModelName:     req.ModelName,
		Category:      req.Category,
		Status:        "PRINTED",
		CreatedAt:     now,
		QRURL:         qrURL,
		Widgets:       req.Widgets,
		PageCount:     1,
	}

	// Generate and Save PDF
	pdfBytes, err := pdfengine.GenerateTaskPDF(task)
	if err == nil {
		pdfDir := filepath.Join(s.cfg.DataDir, "pdfs")
		os.MkdirAll(pdfDir, 0755)
		pdfPath := filepath.Join(pdfDir, fmt.Sprintf("%s.pdf", taskID))
		os.WriteFile(pdfPath, pdfBytes, 0644)
		task.PDFPath = pdfPath

		// Physical Dispatch if auto print is enabled
		shouldPrint := s.cfg.AutoPrint
		if req.AutoPrint != nil {
			shouldPrint = *req.AutoPrint
		}

		if shouldPrint {
			go func() {
				s.dispatchPhysicalPrint(pdfPath)
			}()
			pNow := time.Now()
			task.PrintedAt = &pNow
		}
	}

	s.store.CreateTask(task)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "success",
		"task_id":  task.ID,
		"short_id": task.ShortID,
		"qr_url":   task.QRURL,
		"pdf_url":  fmt.Sprintf("/api/tasks/%s/pdf", task.ID),
	})
}

func (s *Server) dispatchPhysicalPrint(pdfPath string) {
	// Call printctl binary to physically print
	args := []string{"print", pdfPath}
	if s.cfg.PrinterIP != "" {
		args = append(args, "--ip", s.cfg.PrinterIP)
	}
	cmd := exec.Command(s.cfg.PrintctlPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("[-] Physical print dispatch error: %v (Output: %s)\n", err, string(out))
	} else {
		fmt.Printf("[+] Physical print dispatched: %s\n", pdfPath)
	}
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks := s.store.ListTasks()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	t, err := s.store.GetTask(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(t)
}

func (s *Server) handleGetTaskPDF(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	id = strings.TrimSuffix(id, "/pdf")
	t, err := s.store.GetTask(id)
	if err != nil || t.PDFPath == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, t.PDFPath)
}

func (s *Server) handleSubmitFeedback(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/feedback/")
	task, err := s.store.GetTask(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	comment, _ := payload["_comment"].(string)
	delete(payload, "_comment")

	feedback := &db.HumanFeedback{
		SubmittedAt: time.Now(),
		Payload:     payload,
		Comment:     comment,
		Device:      r.UserAgent(),
	}

	s.store.SubmitFeedback(id, feedback)

	// Notify ICS stream
	go s.icsCli.NotifyHumanResponse(task, feedback)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Feedback recorded and notified to agent",
	})
}

// Mobile QR Landing Page (Ultra-fast responsive dark mode H5)
func (s *Server) handleMobileScan(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimPrefix(r.URL.Path, "/r/")
	task, err := s.store.GetTask(taskID)
	if err != nil {
		http.Error(w, "Task not found or expired", http.StatusNotFound)
		return
	}

	tmpl := template.Must(template.New("mobile").Parse(mobileHTMLTemplate))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, task)
}

// Desktop Management Dashboard
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	tasks := s.store.ListTasks()
	tmpl := template.Must(template.New("dashboard").Parse(dashboardHTMLTemplate))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, map[string]interface{}{
		"Tasks":  tasks,
		"HostIP": s.cfg.HostIP,
		"Port":   s.cfg.Port,
	})
}

func getOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

func randomHex(n int) string {
	bytes := make([]byte, n)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

const mobileHTMLTemplate = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no">
  <title>{{.Title}} - AI 物理交互</title>
  <style>
    :root { --bg: #0f172a; --card: #1e293b; --text: #f8fafc; --muted: #94a3b8; --accent: #38bdf8; --border: #334155; --success: #10b981; }
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
    body { background: var(--bg); color: var(--text); padding: 16px; min-height: 100vh; }
    .header { padding-bottom: 12px; border-bottom: 1px solid var(--border); margin-bottom: 16px; }
    .badges { display: flex; gap: 8px; margin-bottom: 8px; }
    .badge { font-size: 11px; padding: 3px 8px; border-radius: 4px; font-weight: 600; text-transform: uppercase; }
    .badge-sec { background: rgba(245, 158, 11, 0.2); color: #fbbf24; border: 1px solid #f59e0b; }
    .badge-cat { background: rgba(56, 189, 248, 0.2); color: #38bdf8; }
    .title { font-size: 20px; font-weight: 700; line-height: 1.3; }
    .meta { font-size: 12px; color: var(--muted); margin-top: 4px; }
    .card { background: var(--card); border: 1px solid var(--border); border-radius: 12px; padding: 16px; margin-bottom: 16px; }
    .content-box { font-size: 14px; line-height: 1.6; color: #cbd5e1; white-space: pre-wrap; word-break: break-word; }
    .widget-group { margin-top: 16px; }
    .widget-label { font-size: 14px; font-weight: 600; margin-bottom: 8px; color: #e2e8f0; }
    .option-btn { display: flex; align-items: center; gap: 10px; background: #0f172a; border: 1px solid var(--border); border-radius: 8px; padding: 12px; margin-bottom: 8px; cursor: pointer; transition: all 0.2s; }
    .option-btn.selected { border-color: var(--accent); background: rgba(56, 189, 248, 0.1); }
    textarea { width: 100%; background: #0f172a; border: 1px solid var(--border); border-radius: 8px; color: var(--text); padding: 12px; font-size: 14px; min-height: 80px; resize: vertical; margin-top: 6px; }
    .btn-submit { width: 100%; background: var(--accent); color: #0f172a; border: none; border-radius: 10px; padding: 14px; font-size: 16px; font-weight: 700; margin-top: 20px; cursor: pointer; }
    .btn-submit:disabled { opacity: 0.5; }
    .success-card { text-align: center; padding: 32px 16px; display: none; }
    .success-icon { font-size: 48px; margin-bottom: 12px; }
  </style>
</head>
<body>
  <div id="form-container">
    <div class="header">
      <div class="badges">
        <span class="badge badge-sec">{{.SecurityLevel}}</span>
        <span class="badge badge-cat">{{.Category}}</span>
        <span class="badge" style="background:#334155;">#{{.ShortID}}</span>
      </div>
      <div class="title">{{.Title}}</div>
      <div class="meta">发起: {{.AgentID}} ({{.ModelName}}) · {{.CreatedAt.Format "2006-01-02 15:04"}}</div>
    </div>

    <div class="card">
      <div class="content-box">{{.Content}}</div>
    </div>

    <form id="feedback-form" class="card">
      <div style="font-size:16px; font-weight:700; margin-bottom:12px;">📱 你的决策与批注</div>
      {{range .Widgets}}
        <div class="widget-group" data-widget-id="{{.ID}}">
          <div class="widget-label">{{.Label}}</div>
          {{if eq .Type "SINGLE_CHOICE"}}
            {{range .Options}}
              <div class="option-btn" onclick="selectRadio(this, '{{$.ID}}')">
                <input type="radio" name="widget_{{$.ID}}" value="{{.}}" style="accent-color:var(--accent);">
                <span>{{.}}</span>
              </div>
            {{end}}
          {{else}}
            <textarea name="widget_{{.ID}}" placeholder="{{.Placeholder}}"></textarea>
          {{end}}
        </div>
      {{end}}

      <div class="widget-group">
        <div class="widget-label">💬 补充意见 / 语音随笔</div>
        <textarea name="_comment" placeholder="输入任何想对 AI 说的话，点击提交后 AI 将立即收到..."></textarea>
      </div>

      <button type="button" class="btn-submit" onclick="submitFeedback()">🚀 提交给 AI Agent</button>
    </form>
  </div>

  <div id="success-box" class="card success-card">
    <div class="success-icon">✅</div>
    <div style="font-size:20px; font-weight:700; margin-bottom:8px;">已成功送达 Agent！</div>
    <div style="font-size:14px; color:var(--muted); line-height:1.5;">
      您的决策已落库并同步推送到 ICS 消息总线。<br>AI Agent 正在开启下一轮推理，您可以离开手机继续工作。
    </div>
  </div>

  <script>
    function selectRadio(el, id) {
      const parent = el.closest('.widget-group');
      parent.querySelectorAll('.option-btn').forEach(b => b.classList.remove('selected'));
      el.classList.add('selected');
      el.querySelector('input[type="radio"]').checked = true;
    }

    async function submitFeedback() {
      const form = document.getElementById('feedback-form');
      const formData = new FormData(form);
      const payload = {};
      for (let [k, v] of formData.entries()) {
        payload[k] = v;
      }
      
      const btn = document.querySelector('.btn-submit');
      btn.disabled = true;
      btn.innerText = '正在同步至 Agent...';

      try {
        const res = await fetch('/api/feedback/{{.ID}}', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify(payload)
        });
        if (res.ok) {
          document.getElementById('form-container').style.display = 'none';
          document.getElementById('success-box').style.display = 'block';
        } else {
          alert('提交失败，请重试');
          btn.disabled = false;
        }
      } catch (e) {
        alert('网络错误: ' + e);
        btn.disabled = false;
      }
    }
  </script>
</body>
</html>`

const dashboardHTMLTemplate = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <title>ICS-Paper-Hub 物理交互全景控制台</title>
  <style>
    :root { --bg: #0b0f19; --card: #151c2e; --text: #f8fafc; --muted: #94a3b8; --accent: #38bdf8; --border: #222f49; }
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
    body { background: var(--bg); color: var(--text); padding: 24px; }
    .header-bar { display: flex; justify-content: space-between; align-items: center; padding-bottom: 20px; border-bottom: 1px solid var(--border); margin-bottom: 24px; }
    .header-title { font-size: 22px; font-weight: 700; display: flex; align-items: center; gap: 10px; }
    .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(360px, 1fr)); gap: 20px; }
    .task-card { background: var(--card); border: 1px solid var(--border); border-radius: 12px; padding: 20px; position: relative; }
    .task-badges { display: flex; gap: 6px; margin-bottom: 10px; }
    .badge { font-size: 11px; padding: 2px 8px; border-radius: 4px; font-weight: 600; }
    .badge-status-PRINTED { background: #3b82f6; color: white; }
    .badge-status-RESPONDED { background: #10b981; color: white; }
    .task-title { font-size: 16px; font-weight: 700; margin-bottom: 6px; }
    .task-content { font-size: 13px; color: var(--muted); margin-bottom: 16px; max-height: 80px; overflow: hidden; text-overflow: ellipsis; }
    .feedback-box { background: rgba(16, 185, 129, 0.1); border: 1px solid #10b981; border-radius: 8px; padding: 10px; font-size: 12px; margin-top: 12px; }
    .btn { background: var(--accent); color: #0f172a; padding: 6px 12px; border-radius: 6px; text-decoration: none; font-size: 12px; font-weight: 600; }
  </style>
</head>
<body>
  <div class="header-bar">
    <div class="header-title">🖨️ ICS-Paper-Hub 物理交互全景控制台</div>
    <div style="font-size:13px; color:var(--muted);">LAN Host: {{.HostIP}}:{{.Port}}</div>
  </div>

  <div class="grid">
    {{range .Tasks}}
      <div class="task-card">
        <div class="task-badges">
          <span class="badge badge-status-{{.Status}}">{{.Status}}</span>
          <span class="badge" style="background:#334155;">{{.SecurityLevel}}</span>
          <span class="badge" style="background:#1e293b; color:#38bdf8;">{{.Category}}</span>
          <span class="badge" style="background:#334155;">#{{.ShortID}}</span>
        </div>
        <div class="task-title">{{.Title}}</div>
        <div style="font-size:12px; color:var(--muted); margin-bottom:10px;">{{.AgentID}} ({{.ModelName}}) · {{.CreatedAt.Format "2006-01-02 15:04"}}</div>
        <div class="task-content">{{.Content}}</div>

        {{if .HumanFeedback}}
          <div class="feedback-box">
            <div style="font-weight:700; color:#34d399; margin-bottom:4px;">✅ 人类已扫码回传:</div>
            <div>{{.HumanFeedback.Comment}}</div>
          </div>
        {{end}}

        <div style="margin-top:16px; display:flex; justify-content:space-between; align-items:center;">
          <a href="/r/{{.ID}}" target="_blank" class="btn" style="background:#334155; color:white;">📱 打开手机端页</a>
          <a href="/api/tasks/{{.ID}}/pdf" target="_blank" class="btn">📄 查看 PDF</a>
        </div>
      </div>
    {{end}}
  </div>
</body>
</html>`
