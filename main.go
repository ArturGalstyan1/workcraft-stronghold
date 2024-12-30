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
	"strings"
	"sync"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/handlers"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/sqls"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
	"github.com/Artur-Galstyan/workcraft-stronghold/views"
	"github.com/a-h/templ"
	"github.com/golang-jwt/jwt/v5"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"github.com/robfig/cron/v3"
)

// Constants
const (
	TokenExpiration        = time.Hour * 24 // 24 hours
	HeartbeatClearInterval = time.Second * 30
)

// Variables
var (
	hashedApiKey string
	db           *sql.DB
)

type EventSender struct {
	connections map[string]http.ResponseWriter
	controllers map[string]http.ResponseController
	mu          sync.RWMutex
}

func NewEventSender() *EventSender {
	return &EventSender{
		connections: make(map[string]http.ResponseWriter),
		controllers: make(map[string]http.ResponseController),
	}
}

func (s *EventSender) Register(ID string, w http.ResponseWriter, rc http.ResponseController) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connections[ID] = w
	s.controllers[ID] = rc
}

func (s *EventSender) Unregister(ID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.connections, ID)
	delete(s.controllers, ID)
}

func (s *EventSender) SendEvent(ID string, msg string) error {
	s.mu.RLock()
	w, exists := s.connections[ID]
	rc, _ := s.controllers[ID]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no connection found for ID: %s", ID)
	}

	_, err := fmt.Fprintf(w, "data: %s\n\n", msg)
	if err != nil {
		return err
	}
	return rc.Flush()
}

func (s *EventSender) BroadcastToChieftains(msg string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for id := range s.connections {
		if strings.HasPrefix(id, "chieftain-") {
			w := s.connections[id]
			rc := s.controllers[id]
			_, err := fmt.Fprintf(w, "data: %s\n\n", msg)
			if err != nil {
				slog.Error("Failed to write to writer", "err", err)
				continue
			}
			rc.Flush()
		}
	}
}

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

func loginHandler(w http.ResponseWriter, r *http.Request) {
	var creds models.Credentials
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if creds.Username != os.Getenv("WORKCRAFT_CHIEFTAIN_USER") ||
		creds.Password != os.Getenv("WORKCRAFT_CHIEFTAIN_PASS") {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// Create the JWT claims
	now := time.Now()
	claims := models.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(TokenExpiration)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
		APIKey: os.Getenv("WORKCRAFT_API_KEY"),
	}

	// Create and sign the token using the API key as the secret
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(os.Getenv("WORKCRAFT_API_KEY")))
	if err != nil {
		slog.Error("Failed to sign token", "err", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "workcraft_auth",
		Value:    signedToken,
		HttpOnly: true,  // Cannot be accessed by JavaScript
		Secure:   false, // false for development, true for production
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
		MaxAge:   int(TokenExpiration.Seconds()), // Match JWT expiration
	})
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

func createTestHandler(eventSender *EventSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("GET /test")
		eventSender.BroadcastToChieftains("HI")
		w.Write([]byte("Success!"))
	}
}

func createSSEHandler(eventSender *EventSender, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		connectionType := r.URL.Query().Get("type")
		if connectionType == "" {
			http.Error(w, "No type provided", http.StatusBadRequest)
			return
		}
		if connectionType != "peon" && connectionType != "chieftain" {
			http.Error(w, "Invalid type provided", http.StatusBadRequest)
			return
		}
		peonID := r.URL.Query().Get("peon_id")
		if peonID == "" && connectionType == "peon" {
			http.Error(w, "No peon_id provided", http.StatusBadRequest)
			return
		}
		queues := r.URL.Query().Get("queues")
		if queues == "" && connectionType == "peon" {
			http.Error(w, "No queues provided", http.StatusBadRequest)
			return
		}

		rc := http.NewResponseController(w)
		var connectionID string
		if connectionType == "peon" {
			slog.Info("Peon connected", "peon_id", peonID)
			connectionID = peonID
			err := sqls.InsertPeonIntoDb(db, peonID, queues)
			if err != nil {
				slog.Error("Failed to insert peon into db", "err", err)
				http.Error(w, "Failed to insert peon into db", http.StatusInternalServerError)
				return
			}
		} else {
			connectionID = fmt.Sprintf("chieftain-%s", utils.GenerateUUID())
		}

		eventSender.Register(connectionID, w, *rc)
		defer eventSender.Unregister(connectionID)

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		connClosed := r.Context().Done()
		for {
			select {
			case <-connClosed:
				if connectionType == "peon" {
					slog.Info("Peon disconnected", "peon_id", peonID)
					status := "OFFLINE"
					_, err := sqls.UpdatePeon(db, peonID, models.PeonUpdate{
						Status: &status,
					})
					if err != nil {
						slog.Error("Failed to mark peon offline", "err", err)
						return
					}
				}
				return
			case <-ticker.C:
				slog.Debug("Ticker!")
			}
		}
	}
}

func sendPendingTasks(db *sql.DB, eventSender *EventSender) {
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

func sendPendingTasksInterval(db *sql.DB, eventSender *EventSender) {
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

	eventSender := NewEventSender()

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
			loginHandler(w, r)
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
	http.HandleFunc("/events", createSSEHandler(eventSender, db))

	go sendPendingTasksInterval(db, eventSender)

	slog.Info("Building Stronghold on port 6112")
	if err := http.ListenAndServe(":6112", nil); err != nil {
		slog.Error("Server failed", "error", err)
	}

}
