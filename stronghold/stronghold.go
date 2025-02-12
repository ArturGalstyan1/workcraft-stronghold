package stronghold

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/handlers"
	"github.com/Artur-Galstyan/workcraft-stronghold/logger"
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
	apiKey      string
	db          *gorm.DB
	eventSender *events.EventSender
	config      models.WorkcraftConfig
}

func NewStronghold(apiKey string, db *gorm.DB, eventSender *events.EventSender, config models.WorkcraftConfig) *Stronghold {

	return &Stronghold{
		apiKey:      apiKey,
		db:          db,
		eventSender: eventSender,
		config:      config,
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
				logger.Log.Error("Failed to clean up dead peons", "err", result.Error)
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
				logger.Log.Error("Failed to clean up inconsistencies", "err", err.Error())
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

	http.HandleFunc("GET /task/{id}", handlers.AuthMiddleware(handlers.CreateTaskViewHandler(s.db), s.apiKey))
	http.HandleFunc("GET /tasks/", handlers.AuthMiddleware(handlers.TaskView, s.apiKey))
	http.HandleFunc("GET /peon/{id}/", handlers.AuthMiddleware(handlers.CreatePeonViewHandler(), s.apiKey))
	http.HandleFunc("GET /peons/", handlers.AuthMiddleware(handlers.CreatePeonsViewHandler(), s.apiKey))
	http.HandleFunc("GET /db/", handlers.AuthMiddleware(handlers.CreateDBDataViewHandler(), s.apiKey))

	http.HandleFunc("GET /api/peons", handlers.AuthMiddleware(handlers.CreateGetPeonsHandler(s.db), s.apiKey))
	http.HandleFunc("GET /api/peon/{id}", handlers.AuthMiddleware(handlers.CreateGetPeonHandler(s.db), s.apiKey))
	http.HandleFunc("GET /api/peon/{id}/tasks", handlers.AuthMiddleware(handlers.CreateGetPeonTaskHandler(s.db), s.apiKey))
	http.HandleFunc("POST /api/peon/{id}/update", handlers.AuthMiddleware(handlers.CreateUpdatePeonHandler(s.db, s.eventSender), s.apiKey))
	http.HandleFunc("POST /api/peon/{id}/statistics", handlers.AuthMiddleware(handlers.CreatePostStatisticsHandler(s.db), s.apiKey))

	http.HandleFunc("POST /api/task", handlers.AuthMiddleware(handlers.CreatePostTaskHandler(s.db), s.apiKey))
	http.HandleFunc("GET /api/tasks", handlers.AuthMiddleware(handlers.CreateGetTasksHandler(s.db), s.apiKey))
	http.HandleFunc("GET /api/task/{id}", handlers.AuthMiddleware(handlers.CreateGetTaskHandler(s.db), s.apiKey))
	http.HandleFunc("POST /api/task/{id}/cancel", handlers.AuthMiddleware(handlers.CreateCancelTaskHandler(s.db, s.eventSender), s.apiKey))
	http.HandleFunc("POST /api/task/{id}/update", handlers.AuthMiddleware(handlers.CreateTaskUpdateHandler(s.db, s.eventSender), s.apiKey))
	http.HandleFunc("GET /api/test", handlers.AuthMiddleware(createTestHandler(s.eventSender), s.apiKey))
	http.HandleFunc("GET /api/db/dump/sqlite", handlers.AuthMiddleware(handlers.DumpDatabaseHandler, s.apiKey))
	http.HandleFunc("GET /api/db/dump/csv", handlers.AuthMiddleware(handlers.CreateDumpDatabaseAsCSVHandler(s.db), s.apiKey))
	http.HandleFunc("POST /api/db/import/csv", handlers.AuthMiddleware(handlers.CreateImportCSVToDBHandler(s.db), s.apiKey))
	http.HandleFunc("GET /api/heartbeat", heartbeatHandler())
	http.HandleFunc("/events", handlers.AuthMiddleware(handlers.CreateSSEHandler(s.eventSender, s.db), s.apiKey))

	logger.Log.Info("Building Stronghold on port 6112")
	if err := http.ListenAndServe(":6112", nil); err != nil {
		logger.Log.Error("Server failed", "error", err)
	}

}

func heartbeatHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Log.Info("GET /heartbeat")
		w.Write([]byte("Success!"))
	}
}

func createTestHandler(eventSender *events.EventSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Log.Info("GET /test")
		eventSender.BroadcastToChieftains("HI")
		w.Write([]byte("Success!"))
	}
}

func (s *Stronghold) SendPendingTasks() {
	tasks, err := sqls.GetNotYetSentOutTasks(s.db)
	if err != nil {
		logger.Log.Error("Failed to get tasks", "err", err)
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
			// logger.Log.Info("Failed to get available peon, skipping. ", "err", err)
			continue
		}

		taskJSON, err := json.Marshal(task)
		if err != nil {
			logger.Log.Error("Failed to marshal task", "err", err)
			return
		}

		msgString := fmt.Sprintf(`{"type": "new_task", "data": %s}`, string(taskJSON))
		s.eventSender.SendEvent(peon.ID, msgString)
	}
}

func (s *Stronghold) SendHeartbeatToPeons() {
	msgJSON := fmt.Sprintf("{\"type\": \"heartbeat\", \"msg\": \"%s\"}", "We need more burrows!")
	s.eventSender.BroadcastToPeons(msgJSON)
}

func (s *Stronghold) PutPendingTasksIntoQueue() {
	type TaskInfo struct {
		ID         string
		Status     string
		RetryCount int
	}
	var tasksToProcess []TaskInfo

	err := s.db.Raw(`
        SELECT id, status, retry_count
        FROM tasks
        WHERE (
            (status = 'PENDING' AND id NOT IN (SELECT task_id FROM queues))
            OR
            (
                status = 'FAILURE'
                AND retry_on_failure = true
                AND retry_count < retry_limit
                AND id NOT IN (SELECT task_id FROM queues)
            )
        )
    `).Scan(&tasksToProcess).Error

	if err != nil {
		logger.Log.Error("Failed to get tasks to process", "err", err)
		return
	}

	if len(tasksToProcess) == 0 {
		return
	}

	tx := s.db.Begin()
	if tx.Error != nil {
		logger.Log.Error("Failed to begin transaction", "err", tx.Error)
		return
	}
	defer tx.Rollback()

	for _, taskInfo := range tasksToProcess {
		queue := models.Queue{
			TaskID:     taskInfo.ID,
			SentToPeon: false,
		}
		if err := tx.Clauses(clause.OnConflict{
			UpdateAll: true,
		}).Create(&queue).Error; err != nil {
			logger.Log.Error("Failed to upsert task into queue", "taskID", taskInfo.ID, "err", err)
			return
		}

		if taskInfo.Status == string(models.TaskStatusFailure) {
			if err := tx.Exec(`
                UPDATE tasks
                SET retry_count = ?
                WHERE id = ?`,
				taskInfo.RetryCount+1, taskInfo.ID,
			).Error; err != nil {
				logger.Log.Error("Failed to increment retry count", "taskID", taskInfo.ID, "err", err)
				return
			}
		}
	}

	if err := tx.Commit().Error; err != nil {
		logger.Log.Error("Failed to commit transaction", "err", err)
		return
	}
}

func (s *Stronghold) SendPendingTasksInterval() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.PutPendingTasksIntoQueue()
			s.SendPendingTasks()
			s.SendHeartbeatToPeons()
		}
	}
}

func (s *Stronghold) CheckDeadWorkers() {
	var deadPeons []models.Peon

	waitingTimeDuration := s.config.TimeBeforeDeadPeon

	err := s.db.Raw(`
        SELECT *
        FROM peons
        WHERE last_heartbeat < ? AND status != 'OFFLINE'
    `, time.Now().Add(-waitingTimeDuration)).Scan(&deadPeons).Error

	if err != nil {
		logger.Log.Error("Failed to get dead peons", "err", err)
		return
	}

	if len(deadPeons) == 0 {
		return
	}

	for _, peon := range deadPeons {
		logger.Log.Info("Peon is dead", "peonID", peon.ID)
		peon.CurrentTask = nil
		peon.Status = "OFFLINE"
		if err := s.db.Save(&peon).Error; err != nil {
			logger.Log.Error("Failed to update peon status to OFFLINE", "peonID", peon.ID, "err", err)
		}
	}

}

func (s *Stronghold) CheckDeadWorkerInterval() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.CheckDeadWorkers()
		}
	}
}
