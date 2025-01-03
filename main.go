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
	"strings"
	"sync"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/database"
	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/handlers"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
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
	// Get all queued tasks
	var queues []models.Queue
	err := db.Preload("Task").
		Where("queued = ?", true).
		Find(&queues).Error

	if err != nil {
		slog.Error("Failed to get tasks from queue", "err", err)
		return
	}

	if len(queues) == 0 {
		return
	}

	// Get all available peons
	var availablePeons []models.Peon
	err = db.Where("status = ? AND current_task IS NULL", "IDLE").
		Find(&availablePeons).Error

	if err != nil {
		slog.Error("Failed to get available peons", "err", err)
		return
	}

	if len(availablePeons) == 0 {
		slog.Info("No idle peons found")
		return
	}

	// Create a map of peons by their supported queues
	peonsByQueue := make(map[string][]models.Peon)
	for _, peon := range availablePeons {
		if peon.Queues == nil {
			continue
		}
		queues := strings.Split(*peon.Queues, ",")
		for _, queue := range queues {
			queue = strings.TrimSpace(queue)
			peonsByQueue[queue] = append(peonsByQueue[queue], peon)
		}
	}

	// Process each queued task
	for _, queue := range queues {
		// Find peons that can handle this task's queue
		availablePeonsForQueue := peonsByQueue[queue.Task.Queue]
		if len(availablePeonsForQueue) == 0 {
			continue
		}

		// Select a peon (you could implement different selection strategies here)
		selectedPeon := availablePeonsForQueue[0]

		// Send task to peon
		taskJSON, err := json.Marshal(queue.Task)
		if err != nil {
			slog.Error("Failed to marshal task", "err", err)
			continue
		}

		msgString := fmt.Sprintf(`{"type": "new_task", "data": %s}`, string(taskJSON))
		eventSender.SendEvent(selectedPeon.ID, msgString)

		// Update queue entry
		err = db.Model(&queue).Update("queued", false).Error
		if err != nil {
			slog.Error("Failed to update queue entry", "err", err)
			continue
		}

		// Remove the used peon from the available peons maps
		for queueName, peons := range peonsByQueue {
			newPeons := make([]models.Peon, 0)
			for _, p := range peons {
				if p.ID != selectedPeon.ID {
					newPeons = append(newPeons, p)
				}
			}
			peonsByQueue[queueName] = newPeons
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
			TaskID: task.ID,
			Queued: true,
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
