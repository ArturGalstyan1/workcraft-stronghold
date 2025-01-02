package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/handlers"
	"github.com/Artur-Galstyan/workcraft-stronghold/sqls"
	"github.com/Artur-Galstyan/workcraft-stronghold/views"
	"github.com/a-h/templ"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"github.com/robfig/cron/v3"
)

// Constants
const (
	HeartbeatClearInterval = time.Second * 30
)

// Variables
var (
	hashedApiKey string
	db           *sql.DB
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

func setupCronJobs(db *sql.DB) {
	c := cron.New()
	var cronMutex sync.Mutex

	c.AddFunc("* * * * *", func() {
		cronMutex.Lock()
		defer cronMutex.Unlock()
		_, err := db.Exec(sqls.CleanPeons())
		if err != nil {
			slog.Error("Failed to clean up dead peons", "err", err)
			return
		}

	})

	c.AddFunc("* * * * *", func() {
		cronMutex.Lock()
		defer cronMutex.Unlock()

		err := sqls.CleanInconsistencies(db)
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

func sendPendingTasks(db *sql.DB, eventSender *events.EventSender) {
	task, err := sqls.GetTaskFromQueue(db)
	if err == sql.ErrNoRows {
		return
	}
	if err != nil {
		slog.Error("Failed to get task from queue", "err", err)
		return
	}
	idlePeon, err := sqls.GetPeonForTask(db, task.Queue)
	if err == sql.ErrNoRows {
		slog.Info("No idle peons found")
		return
	}
	if err != nil {
		slog.Error("Failed to get peon for task", "err", err)
		return
	}

	taskJSON, err := json.Marshal(task)

	if err != nil {
		slog.Error("Failed to marshal task", "err", err)
		return
	}
	msgString := fmt.Sprintf("{\"type\": \"%s\", \"data\": %s}", "new_task", string(taskJSON))
	eventSender.SendEvent(idlePeon.ID, msgString)
	err = sqls.UpdateQueue(db, task.ID)
	if err != nil {
		slog.Error("Failed to update task from queue", "err", err)
		return
	}

}

func putPendingTasksIntoQueue(db *sql.DB) {
	err := sqls.PutPendingTasksIntoQueue(db)
	if err != nil {
		slog.Error("Failed to put pending tasks into queue", "err", err)
	}
}

func sendPendingTasksInterval(db *sql.DB, eventSender *events.EventSender) {
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
	db, err := sql.Open("sqlite3", "workcraft.db")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	err = sqls.SetupDatabase(db)
	if err != nil {
		panic(err)
	}
	setupCronJobs(db)

	eventSender := events.NewEventSender()

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

	http.HandleFunc("GET /task/{id}", handlers.AuthMiddleware(handlers.CreateTaskViewHandler(db), hashedApiKey))
	http.HandleFunc("GET /tasks/", handlers.AuthMiddleware(handlers.TaskView, hashedApiKey))
	http.HandleFunc("GET /peon/{id}/", handlers.AuthMiddleware(handlers.PeonView, hashedApiKey))
	http.HandleFunc("GET /peons/", handlers.AuthMiddleware(handlers.PeonsView, hashedApiKey))

	http.HandleFunc("GET /api/peons", handlers.AuthMiddleware(handlers.CreateGetPeonsHandler(db), hashedApiKey))
	http.HandleFunc("GET /api/peon/{id}", handlers.AuthMiddleware(handlers.CreateGetPeonHandler(db), hashedApiKey))
	http.HandleFunc("GET /api/peon/{id}/tasks", handlers.AuthMiddleware(handlers.CreateGetPeonTaskHandler(db), hashedApiKey))
	http.HandleFunc("POST /api/peon/{id}/update", handlers.AuthMiddleware(handlers.CreateUpdatePeonHandler(db, eventSender), hashedApiKey))
	http.HandleFunc("POST /api/peon/{id}/statistics", handlers.AuthMiddleware(handlers.CreatePostStatisticsHandler(db), hashedApiKey))

	http.HandleFunc("POST /api/task", handlers.AuthMiddleware(handlers.CreatePostTaskHandler(db), hashedApiKey))
	http.HandleFunc("GET /api/tasks", handlers.AuthMiddleware(handlers.CreateGetTasksHandler(db), hashedApiKey))
	http.HandleFunc("GET /api/task/{id}", handlers.AuthMiddleware(handlers.CreateGetTaskHandler(db), hashedApiKey))
	http.HandleFunc("POST /api/task/{id}/cancel", handlers.AuthMiddleware(handlers.CreateCancelTaskHandler(db, eventSender), hashedApiKey))
	http.HandleFunc("POST /api/task/{id}/update", handlers.AuthMiddleware(handlers.CreateTaskUpdateHandler(db, eventSender), hashedApiKey))
	http.HandleFunc("GET /api/test", handlers.AuthMiddleware(createTestHandler(eventSender), hashedApiKey))
	http.HandleFunc("/events", handlers.AuthMiddleware(handlers.CreateSSEHandler(eventSender, db), hashedApiKey))

	go sendPendingTasksInterval(db, eventSender)

	slog.Info("Building Stronghold on port 6112")
	if err := http.ListenAndServe(":6112", nil); err != nil {
		slog.Error("Server failed", "error", err)
	}

}
