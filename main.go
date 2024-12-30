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
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
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

		tx, err := db.Begin()
		if err != nil {
			slog.Error("Failed to start transaction", "err", err)
			return
		}

		defer tx.Rollback()
		rows, err := tx.Query(sqls.CleanBountyboard())
		if err == sql.ErrNoRows {
			return
		}
		if err != nil {
			slog.Error("Failed to find inconsistent tasks", "err", err)
		}
		defer rows.Close()
		i := 0
		for rows.Next() {
			var taskID string
			err := rows.Scan(&taskID)
			if err != nil {
				slog.Error("Failed to extract ID into string", "err", err)
			}
			updateQuery := `UPDATE bountyboard SET status = 'PENDING', peon_id = NULL WHERE id = ?`
			_, err = tx.Exec(updateQuery, taskID)
			if err != nil {
				slog.Error("Failed to update taskID back to PENDING after worker went offline: ", "err", err)
			}
			i++
		}
		err = tx.Commit()
		if err != nil {
			slog.Error("Failed to commit transaction", "err", err)
			return
		}

		if i > 0 {
			slog.Info("Reset tasks: ", "count", i)
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

	status := "RUNNING"
	_, err = sqls.UpdateTask(db, task.ID, models.TaskUpdate{Status: &status, PeonId: &idlePeon.ID})
	if err != nil {
		slog.Error("Failed to update task status to RUNNING", "err", err)
		return
	}

	status = "WORKING"
	_, err = sqls.UpdatePeon(db, idlePeon.ID, models.PeonUpdate{Status: &status, CurrentTask: &task.ID})
	if err != nil {
		slog.Error("Failed to update peon status to WORKING", "err", err)
		return
	}

	slog.Info("Sent task to peon", "task_id", task.ID, "peon_id", idlePeon.ID)

	err = sqls.DeleteTaskFromQueue(db, task.ID)
	if err != nil {
		slog.Error("Failed to delete task from queue", "err", err)
		return
	}

}

func sendPendingTasksInterval(db *sql.DB, eventSender *events.EventSender) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
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
	http.HandleFunc("POST /api/peon/{id}/update", handlers.AuthMiddleware(handlers.CreateUpdatePeonHandler(db), hashedApiKey))
	http.HandleFunc("POST /api/peon/{id}/statistics", handlers.AuthMiddleware(handlers.CreatePostStatisticsHandler(db), hashedApiKey))

	http.HandleFunc("POST /api/task", handlers.AuthMiddleware(handlers.CreatePostTaskHandler(db), hashedApiKey))
	http.HandleFunc("GET /api/tasks", handlers.AuthMiddleware(handlers.CreateGetTasksHandler(db), hashedApiKey))
	http.HandleFunc("GET /api/task/{id}", handlers.AuthMiddleware(handlers.CreateGetTaskHandler(db), hashedApiKey))
	http.HandleFunc("POST /api/task/{id}/cancel", handlers.AuthMiddleware(handlers.CreateCancelTaskHandler(db), hashedApiKey))
	http.HandleFunc("POST /api/task/{id}/update", handlers.AuthMiddleware(handlers.CreateTaskUpdateHandler(db), hashedApiKey))
	http.HandleFunc("POST /api/task/{id}/acknowledgement", handlers.AuthMiddleware(handlers.CreatePostTaskAcknowledgementHandler(db), hashedApiKey))
	http.HandleFunc("GET /api/test", handlers.AuthMiddleware(createTestHandler(eventSender), hashedApiKey))
	http.HandleFunc("/events", handlers.CreateSSEHandler(eventSender, db))

	go sendPendingTasksInterval(db, eventSender)

	slog.Info("Building Stronghold on port 6112")
	if err := http.ListenAndServe(":6112", nil); err != nil {
		slog.Error("Server failed", "error", err)
	}

}
