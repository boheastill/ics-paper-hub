package db

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Task struct {
	ID            string         `json:"id"`             // e.g. "task_20260821_a8f9"
	ShortID       string         `json:"short_id"`       // e.g. "A8F9"
	Title         string         `json:"title"`          // e.g. "2026年架构重构提案"
	Content       string         `json:"content"`        // Markdown / Text content
	SecurityLevel string         `json:"security_level"` // "公开", "内部", "秘密", "机密", "绝密"
	AgentID       string         `json:"agent_id"`       // "CodeArchitect", "DeepSeek-R1"
	ModelName     string         `json:"model_name"`     // "deepseek-r1", "gemini-3.7-flash"
	Category      string         `json:"category"`       // "技术", "业务", "思考", "私人", "决策", "告警"
	Status        string         `json:"status"`         // "PRINTED", "READ", "RESPONDED", "ARCHIVED"
	CreatedAt     time.Time      `json:"created_at"`
	PrintedAt     *time.Time     `json:"printed_at,omitempty"`
	RespondedAt   *time.Time     `json:"responded_at,omitempty"`
	QRURL         string         `json:"qr_url"`
	Widgets       []FormWidget   `json:"widgets"`
	HumanFeedback *HumanFeedback `json:"human_feedback,omitempty"`
	PageCount     int            `json:"page_count"`
	PDFPath       string         `json:"pdf_path,omitempty"`
}

type FormWidget struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"` // "CONFIRM", "SINGLE_CHOICE", "MULTI_CHOICE", "TEXT_FEEDBACK", "VOICE_NOTE"
	Label       string   `json:"label"`
	Options     []string `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Required    bool     `json:"required"`
}

type HumanFeedback struct {
	SubmittedAt  time.Time              `json:"submitted_at"`
	Payload      map[string]interface{} `json:"payload"`
	Comment      string                 `json:"comment"`
	Device       string                 `json:"device"`
	ICSMessageID string                 `json:"ics_message_id,omitempty"`
}

type Store struct {
	mu       sync.RWMutex
	filePath string
	tasks    map[string]*Task
}

func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	dbFile := filepath.Join(dataDir, "paper_tasks.json")
	s := &Store{
		filePath: dbFile,
		tasks:    make(map[string]*Task),
	}
	if err := s.load(); err != nil {
		// If file doesn't exist, start empty
		s.save()
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	var list []*Task
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	s.tasks = make(map[string]*Task)
	for _, t := range list {
		s.tasks[t.ID] = t
	}
	return nil
}

func (s *Store) save() error {
	var list []*Task
	for _, t := range s.tasks {
		list = append(list, t)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0644)
}

func (s *Store) CreateTask(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[t.ID] = t
	return s.save()
}

func (s *Store) GetTask(id string) (*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	return t, nil
}

func (s *Store) ListTasks() []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var list []*Task
	for _, t := range s.tasks {
		list = append(list, t)
	}
	// Sort by CreatedAt desc
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[i].CreatedAt.Before(list[j].CreatedAt) {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	return list
}

func (s *Store) SubmitFeedback(id string, feedback *HumanFeedback) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	now := time.Now()
	t.Status = "RESPONDED"
	t.RespondedAt = &now
	t.HumanFeedback = feedback
	return s.save()
}
