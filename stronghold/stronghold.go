package stronghold

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/handlers"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/sqls"
	"github.com/Artur-Galstyan/workcraft-stronghold/static"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
	"github.com/Artur-Galstyan/workcraft-stronghold/views"
	"github.com/a-h/templ"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Stronghold struct {
	hashedAPIKey string
	db           *gorm.DB
	eventSender  *events.EventSender
}

func NewStronghold(apiKey string, db *gorm.DB, eventSender *events.EventSender) *Stronghold {

	hasher := sha256.New()
	hasher.Write([]byte(apiKey))
	hashedAPIKey := hex.EncodeToString(hasher.Sum(nil))

	return &Stronghold{
		hashedAPIKey: hashedAPIKey,
		db:           db,
		eventSender:  eventSender,
	}

}

func (s *Stronghold) SetupBackgroundTasks() {
	var mutex sync.Mutex

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			mutex.Lock()
			cutoffTime := time.Now().Add(-1 * time.Minute).Format("2006-01-02T15:04:05.999999")

			result := s.db.Model(&models.Peon{}).
				Where("last_heartbeat < ? AND status != 'OFFLINE'", cutoffTime).
				Updates(map[string]interface{}{
					"status":       "OFFLINE",
					"current_task": nil,
				})
			mutex.Unlock()

			if result.Error != nil {
				slog.Error("Failed to clean up dead peons", "err", result.Error)
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			mutex.Lock()
			err := utils.CleanInconsistencies(s.db)
			mutex.Unlock()

			if err != nil {
				slog.Error("Failed to clean up inconsistencies", "err", err)
			}
		}
	}()
}

func (s *Stronghold) Run() {
	go s.SetupBackgroundTasks()
	go s.SendPendingTasksInterval()
	s.StartHTTPServer()
}

func (s *Stronghold) StartHTTPServer() {
	fs := http.FileServer(static.GetFileSystem())
	http.Handle("/static/css/", http.StripPrefix("/static/css/", fs))

	component := views.Index()
	http.Handle("/", templ.Handler(component))
	http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			component := views.Login()
			templ.Handler(component).ServeHTTP(w, r)
		case http.MethodPost:
			handlers.LoginHandler(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	http.HandleFunc("GET /task/{id}", handlers.AuthMiddleware(handlers.CreateTaskViewHandler(s.db), s.hashedAPIKey))
	http.HandleFunc("GET /tasks/", handlers.AuthMiddleware(handlers.TaskView, s.hashedAPIKey))
	http.HandleFunc("GET /peon/{id}/", handlers.AuthMiddleware(handlers.CreatePeonViewHandler(), s.hashedAPIKey))
	http.HandleFunc("GET /peons/", handlers.AuthMiddleware(handlers.CreatePeonsViewHandler(), s.hashedAPIKey))

	http.HandleFunc("GET /api/peons", handlers.AuthMiddleware(handlers.CreateGetPeonsHandler(s.db), s.hashedAPIKey))
	http.HandleFunc("GET /api/peon/{id}", handlers.AuthMiddleware(handlers.CreateGetPeonHandler(s.db), s.hashedAPIKey))
	http.HandleFunc("GET /api/peon/{id}/tasks", handlers.AuthMiddleware(handlers.CreateGetPeonTaskHandler(s.db), s.hashedAPIKey))
	http.HandleFunc("POST /api/peon/{id}/update", handlers.AuthMiddleware(handlers.CreateUpdatePeonHandler(s.db, s.eventSender), s.hashedAPIKey))
	http.HandleFunc("POST /api/peon/{id}/statistics", handlers.AuthMiddleware(handlers.CreatePostStatisticsHandler(s.db), s.hashedAPIKey))

	http.HandleFunc("POST /api/task", handlers.AuthMiddleware(handlers.CreatePostTaskHandler(s.db), s.hashedAPIKey))
	http.HandleFunc("GET /api/tasks", handlers.AuthMiddleware(handlers.CreateGetTasksHandler(s.db), s.hashedAPIKey))
	http.HandleFunc("GET /api/task/{id}", handlers.AuthMiddleware(handlers.CreateGetTaskHandler(s.db), s.hashedAPIKey))
	http.HandleFunc("POST /api/task/{id}/cancel", handlers.AuthMiddleware(handlers.CreateCancelTaskHandler(s.db, s.eventSender), s.hashedAPIKey))
	http.HandleFunc("POST /api/task/{id}/update", handlers.AuthMiddleware(handlers.CreateTaskUpdateHandler(s.db, s.eventSender), s.hashedAPIKey))
	http.HandleFunc("GET /api/test", handlers.AuthMiddleware(createTestHandler(s.eventSender), s.hashedAPIKey))
	http.HandleFunc("GET /api/heartbeat", heartbeatHandler())
	http.HandleFunc("/events", handlers.AuthMiddleware(handlers.CreateSSEHandler(s.eventSender, s.db), s.hashedAPIKey))

	slog.Info("Building Stronghold on port 6112")
	if err := http.ListenAndServe(":6112", nil); err != nil {
		slog.Error("Server failed", "error", err)
	}

}

func heartbeatHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("GET /heartbeat")
		w.Write([]byte("Success!"))
	}
}

func createTestHandler(eventSender *events.EventSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("GET /test")
		eventSender.BroadcastToChieftains("HI")
		w.Write([]byte("Success!"))
	}
}

func (s *Stronghold) SendPendingTasks() {
	tasks, err := sqls.GetNotYetSentOutTasks(s.db)
	if err != nil {
		slog.Error("Failed to get tasks", "err", err)
		return
	}
	if len(tasks) == 0 {
		return
	}

	usedPeons := make(map[string]bool)

	for _, task := range tasks {
		peon, err := sqls.GetAvailablePeon(s.db, task.Queue, usedPeons)
		usedPeons[peon.ID] = true
		if err != nil {
			// slog.Info("Failed to get available peon, skipping. ", "err", err)
			continue
		}

		taskJSON, err := json.Marshal(task)
		if err != nil {
			slog.Error("Failed to marshal task", "err", err)
			return
		}

		msgString := fmt.Sprintf(`{"type": "new_task", "data": %s}`, string(taskJSON))
		s.eventSender.SendEvent(peon.ID, msgString)
	}
}

func (s *Stronghold) PutPendingTasksIntoQueue() {
	var pendingTasks []models.Task
	err := s.db.Where(
		`(
    (status = 'PENDING' AND id NOT IN (SELECT task_id FROM queues WHERE sent_to_peon = true))
    OR
    (
        status = 'FAILURE'
        AND retry_on_failure = true
        AND retry_count < retry_limit
    )
)`,
	).Find(&pendingTasks).Error

	if err != nil {
		slog.Error("Failed to get tasks to process", "err", err)
		return
	}
	if len(pendingTasks) == 0 {
		return
	}

	tx := s.db.Begin()
	if tx.Error != nil {
		slog.Error("Failed to begin transaction", "err", tx.Error)
		return
	}
	defer tx.Rollback()

	for _, task := range pendingTasks {
		queue := models.Queue{
			TaskID:     task.ID,
			SentToPeon: false,
		}
		if err := tx.Clauses(clause.OnConflict{
			UpdateAll: true,
		}).Create(&queue).Error; err != nil {
			slog.Error("Failed to upsert task into queue", "taskID", task.ID, "err", err)
			return
		}

		if task.Status == models.TaskStatusFailure {
			if err := tx.Model(&task).Update("retry_count", task.RetryCount+1).Error; err != nil {
				slog.Error("Failed to increment retry count", "taskID", task.ID, "err", err)
				return
			}
		}
	}

	if err := tx.Commit().Error; err != nil {
		slog.Error("Failed to commit transaction", "err", err)
	}
}

func (s *Stronghold) SendPendingTasksInterval() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.PutPendingTasksIntoQueue()
			s.SendPendingTasks()
		}
	}
}
