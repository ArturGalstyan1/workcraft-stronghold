package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/database"
	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/handlers"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/sqls"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
	"github.com/Artur-Galstyan/workcraft-stronghold/views"
	"github.com/a-h/templ"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

// Constants
const (
	HeartbeatClearInterval = time.Second * 30
)

// Variables
var (
	hashedApiKey string
)

func init() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
	}

	apiKey := os.Getenv("WORKCRAFT_API_KEY")
	if apiKey == "" {
		log.Fatal("WORKCRAFT_API_KEY not set in environment")
	}

	hasher := sha256.New()
	hasher.Write([]byte(apiKey))
	hashedApiKey = hex.EncodeToString(hasher.Sum(nil))
}

func setupCronJobs(db *gorm.DB) {
	c := cron.New()
	var cronMutex sync.Mutex

	c.AddFunc("* * * * *", func() {
		cronMutex.Lock()
		defer cronMutex.Unlock()
		result := db.Model(&models.Peon{}).
			Where("last_heartbeat < datetime('now', '-1 minutes')").
			Updates(map[string]interface{}{
				"status":       "OFFLINE",
				"current_task": nil,
			})
		if result.Error != nil {
			slog.Error("Failed to clean up dead peons", "err", result.Error)
			return
		}

	})

	c.AddFunc("* * * * *", func() {
		cronMutex.Lock()
		defer cronMutex.Unlock()

		err := utils.CleanInconsistencies(db)
		if err != nil {
			slog.Error("Failed to clean up inconsistencies", "err", err)
			return
		}

	})

	c.Start()
}

func createTestHandler(eventSender *events.EventSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("GET /test")
		eventSender.BroadcastToChieftains("HI")
		w.Write([]byte("Success!"))
	}
}

func sendPendingTasks(db *gorm.DB, eventSender *events.EventSender) {
	tasks, err := sqls.GetNotYetSentOutTasks(db)
	if err != nil {
		slog.Error("Failed to get tasks", "err", err)
		return
	}
	if len(tasks) == 0 {
		return
	}

	for _, task := range tasks {
		peon, err := sqls.GetAvailablePeon(db, task.Queue)
		if err != nil {
			slog.Info("Failed to get available peon, skipping. ", "err", err)
			return
		}

		taskJSON, err := json.Marshal(task)
		if err != nil {
			slog.Error("Failed to marshal task", "err", err)
			return
		}

		msgString := fmt.Sprintf(`{"type": "new_task", "data": %s}`, string(taskJSON))
		eventSender.SendEvent(peon.ID, msgString)

		err = sqls.UpdateSentToPeonQueueByTaskID(db, task.ID, true)

		if err != nil {
			slog.Error("Failed to update sent to peon queue", "err", err)
			return
		}
	}

}

func putPendingTasksIntoQueue(db *gorm.DB) {
	// Get all pending tasks not in queue
	var pendingTasks []models.Task
	err := db.Where("status = ? AND id NOT IN (SELECT task_id FROM queues)",
		models.TaskStatusPending).
		Find(&pendingTasks).Error

	if err != nil {
		slog.Error("Failed to get pending tasks", "err", err)
		return
	}

	if len(pendingTasks) == 0 {
		return
	}

	// Start transaction
	tx := db.Begin()
	if tx.Error != nil {
		slog.Error("Failed to begin transaction", "err", tx.Error)
		return
	}
	defer tx.Rollback()

	for _, task := range pendingTasks {
		queue := models.Queue{
			TaskID:     task.ID,
			SentToPeon: true,
		}
		if err := tx.Create(&queue).Error; err != nil {
			slog.Error("Failed to insert task into queue", "taskID", task.ID, "err", err)
			return
		}
	}

	if err := tx.Commit().Error; err != nil {
		slog.Error("Failed to commit transaction", "err", err)
	}
}

func sendPendingTasksInterval(db *gorm.DB, eventSender *events.EventSender) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			putPendingTasksIntoQueue(db)
			sendPendingTasks(db, eventSender)
		}
	}
}

func main() {
	database.InitDB()
	eventSender := events.NewEventSender()

	setupCronJobs(database.DB)

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

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

	http.HandleFunc("GET /task/{id}", handlers.AuthMiddleware(handlers.CreateTaskViewHandler(database.DB), hashedApiKey))
	http.HandleFunc("GET /tasks/", handlers.AuthMiddleware(handlers.TaskView, hashedApiKey))
	http.HandleFunc("GET /peon/{id}/", handlers.AuthMiddleware(handlers.CreatePeonViewHandler(), hashedApiKey))
	http.HandleFunc("GET /peons/", handlers.AuthMiddleware(handlers.CreatePeonsViewHandler(), hashedApiKey))

	http.HandleFunc("GET /api/peons", handlers.AuthMiddleware(handlers.CreateGetPeonsHandler(database.DB), hashedApiKey))
	http.HandleFunc("GET /api/peon/{id}", handlers.AuthMiddleware(handlers.CreateGetPeonHandler(database.DB), hashedApiKey))
	http.HandleFunc("GET /api/peon/{id}/tasks", handlers.AuthMiddleware(handlers.CreateGetPeonTaskHandler(database.DB), hashedApiKey))
	http.HandleFunc("POST /api/peon/{id}/update", handlers.AuthMiddleware(handlers.CreateUpdatePeonHandler(database.DB, eventSender), hashedApiKey))
	http.HandleFunc("POST /api/peon/{id}/statistics", handlers.AuthMiddleware(handlers.CreatePostStatisticsHandler(database.DB), hashedApiKey))

	http.HandleFunc("POST /api/task", handlers.AuthMiddleware(handlers.CreatePostTaskHandler(database.DB), hashedApiKey))
	http.HandleFunc("GET /api/tasks", handlers.AuthMiddleware(handlers.CreateGetTasksHandler(database.DB), hashedApiKey))
	http.HandleFunc("GET /api/task/{id}", handlers.AuthMiddleware(handlers.CreateGetTaskHandler(database.DB), hashedApiKey))
	http.HandleFunc("POST /api/task/{id}/cancel", handlers.AuthMiddleware(handlers.CreateCancelTaskHandler(database.DB, eventSender), hashedApiKey))
	http.HandleFunc("POST /api/task/{id}/update", handlers.AuthMiddleware(handlers.CreateTaskUpdateHandler(database.DB, eventSender), hashedApiKey))
	http.HandleFunc("GET /api/test", handlers.AuthMiddleware(createTestHandler(eventSender), hashedApiKey))
	http.HandleFunc("/events", handlers.AuthMiddleware(handlers.CreateSSEHandler(eventSender, database.DB), hashedApiKey))

	go sendPendingTasksInterval(database.DB, eventSender)

	slog.Info("Building Stronghold on port 6112")
	if err := http.ListenAndServe(":6112", nil); err != nil {
		slog.Error("Server failed", "error", err)
	}

}
