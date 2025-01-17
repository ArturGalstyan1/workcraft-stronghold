package main

import (
	"crypto/sha256"
	"crypto/subtle"
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

	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
	"github.com/Artur-Galstyan/workcraft-stronghold/view"
	"github.com/a-h/templ"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"github.com/robfig/cron/v3"
)

// Variables
var (
	hashedApiKey  string
	registry      *ConnectionRegistry
	taskProcessor *TaskProcessor
	db            *sql.DB
	chieftain     *ChieftainConnection
)

const (
	TokenExpiration        = time.Hour * 24 // 24 hours
	HeartbeatClearInterval = time.Second * 30
)

type ChieftainConnection struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

type ConnectionRegistry struct {
	connections map[string]*websocket.Conn
	mu          sync.RWMutex
	db          *sql.DB
}

type TaskProcessor struct {
	db        *sql.DB
	registry  *ConnectionRegistry
	taskQueue chan models.Task
	mu        sync.RWMutex
	running   bool
}

func NewChieftainConnection() *ChieftainConnection {
	return &ChieftainConnection{}
}

func (c *ChieftainConnection) SetConnection(conn *websocket.Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn = conn
}

func (c *ChieftainConnection) SendMessage(message string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("no chieftain connection")
	}
	return c.conn.WriteMessage(websocket.TextMessage, []byte(message))
}

func notifyChieftain(chieftain *ChieftainConnection, message string) error {
	if chieftain == nil {
		return fmt.Errorf("chieftain connection not initialized")
	}
	return chieftain.SendMessage(message)
}

func NewConnectionRegistry(db *sql.DB) *ConnectionRegistry {
	return &ConnectionRegistry{
		connections: make(map[string]*websocket.Conn), db: db,
	}
}

func NewTaskProcessor(db *sql.DB, registry *ConnectionRegistry) *TaskProcessor {
	return &TaskProcessor{
		db:        db,
		registry:  registry,
		taskQueue: make(chan models.Task, 100),
		running:   false,
	}
}

func (tp *TaskProcessor) Start() {
	tp.mu.Lock()
	if tp.running {
		tp.mu.Unlock()
		return
	}
	tp.running = true
	tp.mu.Unlock()

	// Start the task scanner
	go tp.scanForPendingTasks()

	// Start the task processor
	go tp.processQueue()

}

func (tp *TaskProcessor) Stop() {
	tp.mu.Lock()
	tp.running = false
	tp.mu.Unlock()
}

func (tp *TaskProcessor) processQueue() {
	for task := range tp.taskQueue {
		// sleep for a second
		// time.Sleep(1 * time.Second)
		var count int
		row := tp.db.QueryRow("SELECT COUNT(*) FROM peon WHERE status != 'OFFLINE'")
		err := row.Scan(&count)
		if err != nil {
			slog.Error("Failed to get peon count", "err", err)
			continue
		}
		if count == 0 {
			slog.Info("No peons available")
			continue
		}

		message := models.WebSocketMessage{
			Type: "new_task",
			Message: &map[string]interface{}{
				"id":               task.ID,
				"task_name":        task.TaskName,
				"payload":          task.Payload,
				"retry_on_failure": task.RetryOnFailure,
				"retry_limit":      task.RetryLimit,
				"retry_count":      task.RetryCount,
			},
		}

		messageBytes, err := json.Marshal(message)
		if err != nil {
			slog.Error("Failed to marshal task notification", "err", err)
			continue
		}

		var peonId string
		err = tp.db.QueryRow(utils.GetIdlePeons(), task.Queue).Scan(&peonId)

		if err == sql.ErrNoRows {
			err = tp.db.QueryRow(utils.GetAnyOnlinePeon(), task.Queue).Scan(&peonId)

			if err != nil {
				slog.Error("Failed to find any available peon", "err", err)
				continue
			}
		} else if err != nil {
			slog.Error("Error querying for idle peon", "err", err)
			continue
		}

		slog.Info("Sending task to peon", "taskID", task.ID, "peonId", peonId)
		err = tp.registry.SendMessage(peonId, messageBytes)
		if err != nil {
			slog.Error("Failed to send task to peon", "err", err)
			continue
		}

	}
}

func (tp *TaskProcessor) scanForPendingTasks() {
	ticker := time.NewTicker(1000 * time.Millisecond)
	defer ticker.Stop()

	for {
		tp.mu.RLock()
		if !tp.running {
			tp.mu.RUnlock()
			return
		}
		tp.mu.RUnlock()

		select {
		case <-ticker.C:
			rows, err := tp.db.Query(utils.GetPendingTasks())
			if err != nil {
				slog.Error("Error querying pending tasks", "err", err)
				continue
			}

			for rows.Next() {
				var task models.Task
				var payloadJSON string
				err := rows.Scan(&task.ID, &task.TaskName, &task.Queue, &payloadJSON, &task.RetryCount, &task.RetryLimit, &task.RetryOnFailure)
				if err != nil {
					slog.Error("Error scanning task", "err", err)
					continue
				}

				// Parse the payload
				err = json.Unmarshal([]byte(payloadJSON), &task.Payload)
				if err != nil {
					slog.Error("Error parsing payload", "err", err)
					continue
				}

				slog.Info("Adding task to queue", "taskID", task.ID, "taskName", task.TaskName, "retryCount", task.RetryCount, "retryLimit", task.RetryLimit)
				tp.AddTask(task)
			}
			rows.Close()
		}
	}
}

func (tp *TaskProcessor) AddTask(task models.Task) {
	tp.taskQueue <- task
}

func (cr *ConnectionRegistry) Add(peonID string, conn *websocket.Conn) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	cr.connections[peonID] = conn
}

func (cr *ConnectionRegistry) Remove(peonID string) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	delete(cr.connections, peonID)
}

func (cr *ConnectionRegistry) GetConnection(peonID string) (*websocket.Conn, bool) {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	conn, exists := cr.connections[peonID]
	return conn, exists
}

func (cr *ConnectionRegistry) SendMessage(peonID string, message []byte) error {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	conn, exists := cr.connections[peonID]
	if !exists {
		markPeonOffline(db, peonID)
		return fmt.Errorf("no connection found for peon: %s", peonID)
	}

	return conn.WriteMessage(websocket.TextMessage, message)
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

func markPeonOffline(db *sql.DB, peonID string) {
	_, err := db.Exec(utils.MarkPeonAsOffline(), peonID)
	if err != nil {
		slog.Error("Failed to mark peon offline", "err", err)
	}
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

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Check for API key first (service-to-service)
		token := r.Header.Get("WORKCRAFT_API_KEY")
		if token != "" {
			// Use constant-time comparison for API key
			if subtle.ConstantTimeCompare([]byte(token), []byte(hashedApiKey)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// 2. Check for JWT cookie (browser-based)
		cookie, err := r.Cookie("workcraft_auth")
		if err != nil {
			// For API requests, return 401
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			// For page requests, redirect to login
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Validate JWT
		token = cookie.Value
		claims := &models.Claims{}
		jwtToken, err := jwt.ParseWithClaims(token, claims, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return []byte(os.Getenv("WORKCRAFT_API_KEY")), nil
		})

		if err != nil || !jwtToken.Valid {
			// For API requests, return 401
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			// For page requests, redirect to login
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// JWT is valid, set API key from claims and continue
		r.Header.Set("WORKCRAFT_API_KEY", claims.APIKey)
		next.ServeHTTP(w, r)
	}
}

func setupCronJobs(db *sql.DB) {
	c := cron.New()
	var cronMutex sync.Mutex

	c.AddFunc("* * * * *", func() {
		cronMutex.Lock()
		defer cronMutex.Unlock()
		_, err := db.Exec(utils.CleanPeons())
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
		rows, err := tx.Query(utils.CleanBountyboard())
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

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		token := r.Header.Get("WORKCRAFT_API_KEY")
		if token == "" {
			slog.Error("No WORKCRAFT_API_KEY provided")
			return false
		}

		// Use constant-time comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(token), []byte(hashedApiKey)) != 1 {
			slog.Error("Invalid WORKCRAFT_API_KEY provided")
			return false
		}

		return true
	},
}

var chieftainUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		jwtToken := r.Header.Get("Sec-Websocket-Protocol")
		slog.Info("JWT Token: ", "jet", jwtToken)
		// If not in query params, try cookie as before
		if jwtToken == "" {
			if cookie, err := r.Cookie("workcraft_auth"); err == nil {
				jwtToken = cookie.Value
			}
		}

		if jwtToken == "" {
			slog.Error("No JWT provided")
			return false
		}

		claims := &models.Claims{}
		t, err := jwt.ParseWithClaims(jwtToken, claims, func(token *jwt.Token) (interface{}, error) {
			return []byte(os.Getenv("WORKCRAFT_API_KEY")), nil
		})
		if err != nil {
			slog.Error("Invalid JWT", "err", err)
			return false
		}

		if !t.Valid {
			slog.Error("Invalid JWT")
			return false
		}
		return true
	},
}

func taskView(w http.ResponseWriter, r *http.Request) {
	component := view.Tasks()
	templ.Handler(component).ServeHTTP(w, r)
}
func peonsView(w http.ResponseWriter, r *http.Request) {
	component := view.Peons()
	templ.Handler(component).ServeHTTP(w, r)
}

func peonView(w http.ResponseWriter, r *http.Request) {
	peonID := r.PathValue("id")

	if peonID == "" {
		slog.Error("Peon ID is required")
		http.Error(w, "Peon ID is required", http.StatusBadRequest)
		return
	}

	component := view.Peon(peonID)
	templ.Handler(component).ServeHTTP(w, r)
}

func createGetPeonTaskHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		peonID := r.PathValue("id")

		if peonID == "" {
			slog.Error("Peon ID is required")
			http.Error(w, "Peon ID is required", http.StatusBadRequest)
			return
		}

		rows, err := db.Query("SELECT * FROM bountyboard WHERE peon_id = ?", peonID)
		if err != nil {
			slog.Error("Error querying tasks", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		defer rows.Close()

		var tasks []models.Task

		for rows.Next() {
			var task models.Task
			var payloadJSON string

			err := rows.Scan(&task.ID, &task.Status, &task.CreatedAt, &task.UpdatedAt, &task.TaskName, &task.PeonId, &task.Queue, &payloadJSON, &task.Result, &task.RetryOnFailure, &task.RetryCount, &task.RetryLimit)

			if err != nil {
				slog.Error("Error scanning task", "err", err)
				continue
			}

			err = json.Unmarshal([]byte(payloadJSON), &task.Payload)
			if err != nil {
				slog.Error("Error parsing payload", "err", err)
				continue
			}

			tasks = append(tasks, task)
		}

		if len(tasks) == 0 {
			tasks = []models.Task{}
		}

		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(tasks)
		if err != nil {
			slog.Error("Failed to encode tasks", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}
}

func createGetPeonHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peonID := r.PathValue("id")

		if peonID == "" {
			slog.Error("Peon ID is required")
			http.Error(w, "Peon ID is required", http.StatusBadRequest)
			return
		}

		var peon models.Peon

		err := db.QueryRow("SELECT * FROM peon WHERE id = ?", peonID).Scan(&peon.ID, &peon.Status, &peon.LastHeartbeat, &peon.CurrentTask, &peon.Queues)
		if err == sql.ErrNoRows {
			http.Error(w, "Peon not found", http.StatusNotFound)
			return
		}

		if err != nil {
			slog.Error("Failed to fetch peon", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(peon)
		if err != nil {
			slog.Error("Failed to encode peon", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func createTaskViewHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("id")

		if taskID == "" {
			slog.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}

		var task models.Task
		var payloadJSON string
		err := db.QueryRow(`
            SELECT id, status, created_at, updated_at, task_name,
                   peon_id, queue, payload, result, retry_on_failure,
                   retry_count, retry_limit
            FROM bountyboard WHERE id = ?`, taskID).Scan(
			&task.ID, &task.Status, &task.CreatedAt, &task.UpdatedAt,
			&task.TaskName, &task.PeonId, &task.Queue, &payloadJSON,
			&task.Result, &task.RetryOnFailure, &task.RetryCount, &task.RetryLimit)
		if err == sql.ErrNoRows {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}
		if err != nil {
			slog.Error("Failed to fetch task", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		err = json.Unmarshal([]byte(payloadJSON), &task.Payload)
		if err != nil {
			slog.Error("Error parsing payload", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Render the template
		component := view.Task(task)
		templ.Handler(component).ServeHTTP(w, r)
	}
}

func createTaskUpdateHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("id")
		if taskID == "" {
			slog.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}
		slog.Info("Received update for task", "id", taskID)

		var update struct {
			Status      *models.TaskStatus `json:"status,omitempty"`
			PeonId      *string            `json:"peon_id,omitempty"`
			RetryCount  *int               `json:"retry_count,omitempty"`
			Result      interface{}        `json:"result,omitempty"`
			Queue       *string            `json:"queue,omitempty"`
			RetryLimit  *int               `json:"retry_limit,omitempty"`
			RetryOnFail *bool              `json:"retry_on_failure,omitempty"`
		}

		err := json.NewDecoder(r.Body).Decode(&update)
		if err != nil {
			slog.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// Build dynamic query
		query := "UPDATE bountyboard SET"
		var args []interface{}
		var setClauses []string

		if update.Status != nil {
			setClauses = append(setClauses, "status = ?")
			args = append(args, *update.Status)
		}
		if update.PeonId != nil {
			setClauses = append(setClauses, "peon_id = ?")
			args = append(args, update.PeonId)
		}
		if update.RetryCount != nil {
			setClauses = append(setClauses, "retry_count = ?")
			args = append(args, *update.RetryCount)
		}
		if update.Result != nil {
			setClauses = append(setClauses, "result = ?")
			args = append(args, update.Result)
		}
		if update.Queue != nil {
			setClauses = append(setClauses, "queue = ?")
			args = append(args, *update.Queue)
		}
		if update.RetryLimit != nil {
			setClauses = append(setClauses, "retry_limit = ?")
			args = append(args, *update.RetryLimit)
		}
		if update.RetryOnFail != nil {
			setClauses = append(setClauses, "retry_on_failure = ?")
			args = append(args, *update.RetryOnFail)
		}

		if len(setClauses) == 0 {
			http.Error(w, "No fields to update", http.StatusBadRequest)
			return
		}

		query += " " + strings.Join(setClauses, ", ")
		query += " WHERE id = ?"
		args = append(args, taskID)

		_, err = db.Exec(query, args...)
		if err != nil {
			slog.Error("Failed to update task", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Fetch updated task for websocket notification
		var updatedTask models.Task
		var payloadJSON string
		err = db.QueryRow(`
            SELECT id, status, created_at, updated_at, task_name,
                   peon_id, queue, payload, result, retry_on_failure,
                   retry_count, retry_limit
            FROM bountyboard WHERE id = ?`, taskID).Scan(
			&updatedTask.ID, &updatedTask.Status, &updatedTask.CreatedAt,
			&updatedTask.UpdatedAt, &updatedTask.TaskName, &updatedTask.PeonId,
			&updatedTask.Queue, &payloadJSON, &updatedTask.Result,
			&updatedTask.RetryOnFailure, &updatedTask.RetryCount, &updatedTask.RetryLimit)

		if err != nil {
			slog.Error("Failed to fetch updated task", "err", err)
		} else {
			err = json.Unmarshal([]byte(payloadJSON), &updatedTask.Payload)
			if err != nil {
				slog.Error("Error parsing payload", "err", err)
			} else {
				taskJSON, err := json.Marshal(updatedTask)
				if err != nil {
					slog.Error("Failed to serialize updated task", "err", err)
				} else {
					message := fmt.Sprintf(`{"type": "task_update", "message": {"task": %s}}`,
						string(taskJSON))
					err := notifyChieftain(chieftain, message)
					if err != nil {
						slog.Error("Failed to send task update to chieftain", "err", err)
					}
				}
			}
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func createPostTaskAcknowledgementHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("id")
		if taskID == "" {
			slog.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}
		slog.Info("Received acknowledge for ", "id", taskID)

		var t models.TaskAcknowledgement
		err := json.NewDecoder(r.Body).Decode(&t)
		if err != nil {
			slog.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		_, err = db.Exec("UPDATE bountyboard SET status = 'RUNNING', peon_id = ? WHERE id = ?", t.PeonID, taskID)
		if err != nil {
			slog.Error("Failed to update task status", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)

	}
}

func createPostStatisticsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// slog.Info("Received statistics update")

		var stats struct {
			Type   string      `json:"type"`
			Value  interface{} `json:"value"`
			PeonID *string     `json:"peon_id"`
			TaskID *string     `json:"task_id"`
		}

		err := json.NewDecoder(r.Body).Decode(&stats)
		if err != nil {
			slog.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		valueJSON, err := json.Marshal(stats.Value)
		if err != nil {
			slog.Error("Failed to serialize value", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		query := `INSERT INTO stats (type, value, peon_id, task_id) VALUES (?, ?, ?, ?)`
		_, err = db.Exec(query, stats.Type, valueJSON, stats.PeonID, stats.TaskID)
		if err != nil {
			slog.Error("Failed to insert statistics into database", "err", err)
		}

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("Statistics added"))
	}
}

func testHandler(w http.ResponseWriter, r *http.Request) {
	slog.Info("GET /test")
	w.Write([]byte("Success!"))
}

func createGetPeonsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("GET /api/peons")
		queryParams, err := utils.ParsePeonQuery(r.URL.Query().Get("query"))
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid query: %v", err), http.StatusBadRequest)
			return
		}

		// First, get total count
		countQuery, countArgs, err := utils.BuildPeonQuery(queryParams.Filter)
		countQuery = strings.Replace(countQuery, "SELECT *", "SELECT COUNT(*)", 1)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid filter: %v", err), http.StatusBadRequest)
			return
		}

		var totalItems int
		err = db.QueryRow(countQuery, countArgs...).Scan(&totalItems)
		if err != nil {
			slog.Error("Error counting peons", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Then get paginated items
		sqlQuery, args, err := utils.BuildPeonQuery(queryParams.Filter)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid filter: %v", err), http.StatusBadRequest)
			return
		}

		sqlQuery += fmt.Sprintf(" ORDER BY %s %s", queryParams.Order.Field, queryParams.Order.Dir)
		sqlQuery += " LIMIT ? OFFSET ?"
		args = append(args, queryParams.PerPage, queryParams.Page*queryParams.PerPage)

		rows, err := db.Query(sqlQuery, args...)
		if err != nil {
			slog.Error("Error querying peons", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var peons []models.Peon
		for rows.Next() {
			var peon models.Peon
			err := rows.Scan(&peon.ID, &peon.Status, &peon.LastHeartbeat,
				&peon.CurrentTask, &peon.Queues)
			if err != nil {
				slog.Error("Error scanning peon", "err", err)
				continue
			}
			peons = append(peons, peon)
		}

		if peons == nil {
			peons = []models.Peon{}
		}

		// Calculate total pages
		totalPages := (totalItems + queryParams.PerPage - 1) / queryParams.PerPage

		response := models.PaginatedResponse{
			Page:       queryParams.Page,
			PerPage:    queryParams.PerPage,
			TotalItems: totalItems,
			TotalPages: totalPages,
			Items:      peons,
		}

		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(response)
		if err != nil {
			slog.Error("Error encoding response", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func createUpdatePeonHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// slog.Info("POST /api/peon/{id}/update")
		var update struct {
			Status        *string `json:"status,omitempty"`
			LastHeartbeat *string `json:"last_heartbeat,omitempty"`
			CurrentTask   *string `json:"current_task,omitempty"`
			Queues        *string `json:"queues,omitempty"`
		}

		err := json.NewDecoder(r.Body).Decode(&update)
		if err != nil {
			slog.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		query := "UPDATE peon SET"
		var args []interface{}
		var setClauses []string

		if update.Status != nil {
			setClauses = append(setClauses, "status = ?")
			args = append(args, *update.Status)
		}
		if update.LastHeartbeat != nil {
			setClauses = append(setClauses, "last_heartbeat = ?")
			args = append(args, *update.LastHeartbeat)
		}

		setClauses = append(setClauses, "current_task = ?")
		if update.CurrentTask != nil {
			args = append(args, *update.CurrentTask)
		} else {
			args = append(args, nil)
		}
		if update.Queues != nil {
			setClauses = append(setClauses, "queues = ?")
			args = append(args, *update.Queues)
		}

		if len(setClauses) == 0 {
			http.Error(w, "No fields to update", http.StatusBadRequest)
			return
		}

		query += " " + strings.Join(setClauses, ", ")
		query += " WHERE id = ?"

		peonID := r.PathValue("id")
		args = append(args, peonID)

		_, err = db.Exec(query, args...)
		if err != nil {
			slog.Error("Failed to update peon", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Construct updated peon directly from the update data
		var (
			currentTask *string
			queues      *string
		)
		if update.CurrentTask != nil {
			val := *update.CurrentTask
			currentTask = &val
		}
		if update.Queues != nil {
			val := *update.Queues
			queues = &val
		}

		updatedPeon := models.Peon{
			ID: peonID,
		}

		// Set only the fields that were updated
		if update.Status != nil {
			updatedPeon.Status = *update.Status
		}
		if update.LastHeartbeat != nil {
			updatedPeon.LastHeartbeat = *update.LastHeartbeat
		}
		updatedPeon.CurrentTask = currentTask // Always set this, may be nil
		if update.Queues != nil {
			updatedPeon.Queues = queues
		}

		peonJSON, err := json.Marshal(updatedPeon)
		if err != nil {
			slog.Error("Failed to serialize updated peon", "err", err)
		} else {
			message := fmt.Sprintf(`{"type": "peon_update", "message": {"peon": %s}}`,
				string(peonJSON))
			err := notifyChieftain(chieftain, message)
			if err != nil {
				slog.Error("Failed to send peon update to chieftain", "err", err)
			}
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func createCancelTaskHandler(cr *ConnectionRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("POST /api/task/{id}/cancel")
		taskID := r.PathValue("id")

		if taskID == "" {
			slog.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}

		slog.Info("Received cancel for task", "id", taskID)
		var peonID string
		var status string
		err := cr.db.QueryRow("SELECT peon_id, status FROM bountyboard WHERE id = ?", taskID).Scan(&peonID, &status)
		if err == sql.ErrNoRows {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}

		if err != nil {
			slog.Error("Failed to fetch task", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if status != "RUNNING" {
			http.Error(w, "Task is not running", http.StatusBadRequest)
			return
		}

		data := map[string]interface{}{
			"type":    "cancel_task",
			"message": taskID,
		}

		bytes, err := json.Marshal(data)
		if err != nil {
			slog.Error("Failed to marshal cancel message", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		err = cr.SendMessage(peonID, bytes)
		if err != nil {
			slog.Error("Failed to send cancel message", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		res := map[string]interface{}{
			"send": "true",
		}

		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(res)
	}
}

func createGetTasksHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("GET /api/tasks")
		queryParams, err := utils.ParseTaskQuery(r.URL.Query().Get("query"))
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid query: %v", err), http.StatusBadRequest)
			return
		}

		// First, get total count
		countQuery, countArgs, err := utils.BuildTaskQuery(queryParams.Filter)
		countQuery = strings.Replace(countQuery, "SELECT *", "SELECT COUNT(*)", 1)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid filter: %v", err), http.StatusBadRequest)
			return
		}

		var totalItems int
		err = db.QueryRow(countQuery, countArgs...).Scan(&totalItems)
		if err != nil {
			slog.Error("Error counting tasks", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Then get paginated items
		sqlQuery, args, err := utils.BuildTaskQuery(queryParams.Filter)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid filter: %v", err), http.StatusBadRequest)
			return
		}

		sqlQuery += fmt.Sprintf(" ORDER BY %s %s", queryParams.Order.Field, queryParams.Order.Dir)
		sqlQuery += " LIMIT ? OFFSET ?"
		args = append(args, queryParams.PerPage, queryParams.Page*queryParams.PerPage)

		rows, err := db.Query(sqlQuery, args...)
		if err != nil {
			slog.Error("Error querying tasks", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var tasks []models.Task
		for rows.Next() {
			var task models.Task
			var payloadJSON string
			err := rows.Scan(&task.ID, &task.Status, &task.CreatedAt, &task.UpdatedAt,
				&task.TaskName, &task.PeonId, &task.Queue, &payloadJSON,
				&task.Result, &task.RetryOnFailure, &task.RetryCount, &task.RetryLimit)
			if err != nil {
				slog.Error("Error scanning task", "err", err)
				continue
			}
			err = json.Unmarshal([]byte(payloadJSON), &task.Payload)
			if err != nil {
				slog.Error("Error parsing payload", "err", err)
				continue
			}
			tasks = append(tasks, task)
		}

		if tasks == nil {
			tasks = []models.Task{}
		}

		totalPages := (totalItems + queryParams.PerPage - 1) / queryParams.PerPage

		response := models.PaginatedResponse{
			Page:       queryParams.Page,
			PerPage:    queryParams.PerPage,
			TotalItems: totalItems,
			TotalPages: totalPages,
			Items:      tasks,
		}

		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(response)
		if err != nil {
			slog.Error("Error encoding response", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func createGetTaskHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("GET /api/task/{id}")
		taskID := r.PathValue("id")

		if taskID == "" {
			slog.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}

		var task models.Task
		var payloadJSON string

		err := db.QueryRow("SELECT * FROM bountyboard WHERE id = ?", taskID).Scan(&task.ID, &task.Status, &task.CreatedAt, &task.UpdatedAt, &task.TaskName, &task.PeonId, &task.Queue, &payloadJSON, &task.Result, &task.RetryOnFailure, &task.RetryCount, &task.RetryLimit)

		if err == sql.ErrNoRows {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}

		if err != nil {
			slog.Error("Failed to fetch task", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		err = json.Unmarshal([]byte(payloadJSON), &task.Payload)
		if err != nil {
			slog.Error("Error parsing payload", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(task)
		if err != nil {
			slog.Error("Failed to encode task", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func createPostTaskHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Received new task!")
		task := models.Task{}
		err := json.NewDecoder(r.Body).Decode(&task)
		if err != nil {
			slog.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// Validate the task
		if task.TaskName == "" {
			slog.Error("Task name is required")
			http.Error(w, "Task name is required", http.StatusBadRequest)
			return
		}
		task.Status = models.TaskStatusPending

		slog.Info("Creating task with ID and name", "id", task.ID, "name", task.TaskName)
		payloadJSON, err := json.Marshal(task.Payload)
		if err != nil {
			slog.Error("Failed to serialize payload", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		_, err = db.Exec(utils.InsertIntoBountyboard(), task.ID, task.Status, task.TaskName, task.Queue, payloadJSON, task.RetryOnFailure, task.RetryLimit)
		if err != nil {
			slog.Error("Failed to insert task into database", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)

		err = json.NewEncoder(w).Encode(task)
		if err != nil {
			slog.Error("Failed to encode task", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}

		taskProcessor.AddTask(task)

	}
}

func insertPeonIntoDb(db *sql.DB, peonId string, peonQueues string) {
	var queues interface{} = nil
	if peonQueues != "NULL" {
		queues = peonQueues
	}

	_, err := db.Exec(utils.InsertPeon(), peonId, queues)
	if err != nil {
		slog.Error("Error inserting peon into db", "err", err)
	}
}

func updatePeonHeartbeat(db *sql.DB, peonId string) {
	query := `UPDATE peon SET last_heartbeat = datetime('now') WHERE id = ?`
	_, err := db.Exec(query, peonId)
	if err != nil {
		slog.Error("Error updating peon with", "id", peonId)
	}
}

func chieftainWsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := chieftainUpgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Info("upgrade failed: ", "err", err)
		return
	}
	chieftain.SetConnection(conn)
	defer func() {
		slog.Info("Chieftain disconnected")
		conn.Close()
	}()

	slog.Info("Chieftain connected")
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Error("websocket error:", "err", err)
			}
			break
		}
	}
}

func createWsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Info("upgrade failed: ", "err", err)
			return
		}
		peonId := r.URL.Query().Get("peon")
		peonQueues := r.URL.Query().Get("queues")
		slog.Info("Peon with id connected", "id", peonId, "queues", peonQueues)

		insertPeonIntoDb(db, peonId, peonQueues)
		registry.Add(peonId, conn)

		defer func() {
			slog.Info("Peon disconnected", "id", peonId)
			registry.Remove(peonId)

			_, err := db.Exec(utils.MarkPeonAsOffline(), peonId)
			if err != nil {
				slog.Error("Failed to update peon status to offline", "err", err)
			}

			conn.Close()
		}()
		// Just echo back any message received
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					slog.Error("websocket error:", "err", err)
				}
				break
			}

			var wsMsg models.WebSocketMessage
			if err := json.Unmarshal(msg, &wsMsg); err != nil {
				slog.Error("failed to parse message", "err", err, "msg", string(msg))
				continue
			}

			switch wsMsg.Type {
			case "heartbeat":
				updatePeonHeartbeat(db, peonId)
			case "task_done":
				slog.Info("Received task done from peon", "peon", peonId)
				if wsMsg.Message != nil {
					if taskID, ok := (*wsMsg.Message)["id"].(string); ok {
						if result, ok := (*wsMsg.Message)["result"]; ok {
							status := (*wsMsg.Message)["status"].(string)
							_, err := db.Exec("UPDATE bountyboard SET status = ?, result = ? WHERE id = ?", status, result, taskID)
							if err != nil {
								slog.Error("Failed to update task status", "err", err)
							}
						}
					}
				}
			case "ack":
				slog.Info("Got acknowledgement from ", "peon", peonId)
				if wsMsg.Message != nil {
					if taskID, ok := (*wsMsg.Message)["id"].(string); ok {
						// Update task status to RUNNING
						_, err = db.Exec("UPDATE bountyboard SET status = 'RUNNING', peon_id = ? WHERE id = ?", peonId, taskID)
						if err != nil {
							slog.Error("Failed to update task status", "err", err)
							continue
						}

					}
				}
			default:
				slog.Warn("Unknown message type arrived", "msg", wsMsg.Type)
			}

		}
	}
}

func setupDatabase(db *sql.DB) {
	queries := []string{
		utils.CreatePeonTable(),
		utils.CreatePeonTrigger(),
		utils.CreateBountyboardTable(),
		utils.CreateBountyboardTrigger(),
		utils.CreateStatsTable(),
	}

	for _, q := range queries {
		_, err := db.Exec(q)
		if err != nil {
			slog.Error("Error executing query", "query", q, "err", err)
		}
	}

	_, err := db.Exec("UPDATE peon SET status = 'OFFLINE'")
	if err != nil {
		slog.Error("Error setting all peons to offline", "err", err)
	}

}

func main() {
	var err error
	db, err = sql.Open("sqlite3", "workcraft.db")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	setupDatabase(db)
	setupCronJobs(db)

	registry = NewConnectionRegistry(db)
	taskProcessor = NewTaskProcessor(db, registry)
	chieftain = NewChieftainConnection()

	taskProcessor.Start()
	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	component := view.Index()

	http.Handle("/", templ.Handler(component))
	http.HandleFunc("/ws", createWsHandler(db))
	http.HandleFunc("/ws/chieftain", chieftainWsHandler)
	http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			component := view.Login()
			templ.Handler(component).ServeHTTP(w, r)
		case http.MethodPost:
			loginHandler(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	http.HandleFunc("GET /task/{id}", authMiddleware(createTaskViewHandler(db)))
	http.HandleFunc("GET /peon/{id}/", authMiddleware(peonView))
	http.HandleFunc("GET /peons/", authMiddleware(peonsView))
	http.HandleFunc("GET /tasks/", authMiddleware(taskView))

	http.HandleFunc("GET /api/peons", authMiddleware(createGetPeonsHandler(db)))
	http.HandleFunc("GET /api/peon/{id}", authMiddleware(createGetPeonHandler(db)))
	http.HandleFunc("GET /api/peon/{id}/tasks", authMiddleware(createGetPeonTaskHandler(db)))
	http.HandleFunc("POST /api/peon/{id}/update", authMiddleware(createUpdatePeonHandler(db)))
	http.HandleFunc("POST /api/peon/{id}/statistics", authMiddleware(createPostStatisticsHandler(db)))

	http.HandleFunc("POST /api/task", authMiddleware(createPostTaskHandler(db)))
	http.HandleFunc("GET /api/tasks", authMiddleware(createGetTasksHandler(db)))
	http.HandleFunc("GET /api/task/{id}", authMiddleware(createGetTaskHandler(db)))
	http.HandleFunc("POST /api/task/{id}/cancel", authMiddleware(createCancelTaskHandler(registry)))
	http.HandleFunc("POST /api/task/{id}/update", authMiddleware(createTaskUpdateHandler(db)))
	http.HandleFunc("POST /api/task/{id}/acknowledgement", authMiddleware(createPostTaskAcknowledgementHandler(db)))

	http.HandleFunc("GET /api/test", authMiddleware(testHandler))

	slog.Info("Building Stronghold on port 6112")
	http.ListenAndServe(":6112", nil)
}
