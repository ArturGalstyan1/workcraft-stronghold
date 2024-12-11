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
)

const (
	TokenExpiration        = time.Hour * 24 // 24 hours
	HeartbeatClearInterval = time.Second * 30
)

type ConnectionRegistry struct {
	connections map[string]*websocket.Conn
	mu          sync.RWMutex
	db          *sql.DB
}

type TaskProcessor struct {
	db        *sql.DB
	registry  *ConnectionRegistry
	taskQueue chan Task
	mu        sync.RWMutex
	running   bool
}

type WebSocketMessage struct {
	Type    string                  `json:"type"`
	Message *map[string]interface{} `json:"message,omitempty"`
}

type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type Claims struct {
	jwt.RegisteredClaims
	APIKey string `json:"api_key"`
}

type TaskAcknowledgement struct {
	PeonID string `json:"peon_id"`
}

type TaskStatus string

const (
	TaskStatusPending TaskStatus = "PENDING"
	TaskStatusRunning TaskStatus = "RUNNING"
	TaskStatusSuccess TaskStatus = "SUCCESS"
	TaskStatusFailure TaskStatus = "FAILURE"
	TaskStatusInvalid TaskStatus = "INVALID"
)

// TaskPayload represents the arguments and parameters for task execution
type TaskPayload struct {
	TaskArgs             []interface{}          `json:"task_args"`
	TaskKwargs           map[string]interface{} `json:"task_kwargs"`
	PrerunHandlerArgs    []interface{}          `json:"prerun_handler_args"`
	PrerunHandlerKwargs  map[string]interface{} `json:"prerun_handler_kwargs"`
	PostrunHandlerArgs   []interface{}          `json:"postrun_handler_args"`
	PostrunHandlerKwargs map[string]interface{} `json:"postrun_handler_kwargs"`
}

type Task struct {
	ID             string      `json:"id"`
	TaskName       string      `json:"task_name"`
	Status         TaskStatus  `json:"status"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
	PeonId         *string     `json:"peon_id"`
	Queue          string      `json:"queue"`
	Payload        TaskPayload `json:"payload"`
	Result         interface{} `json:"result"`
	RetryOnFailure bool        `json:"retry_on_failure"`
	RetryCount     int         `json:"retry_count"`
	RetryLimit     int         `json:"retry_limit"`
}

type Peon struct {
	Id            string  `json:"id"`
	Status        string  `json:"status"`
	LastHeartbeat string  `json:"last_heartbeat"`
	CurrentTask   *string `json:"current_task"`
	Queues        *string `json:"queues"`
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
		taskQueue: make(chan Task, 100),
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

		message := WebSocketMessage{
			Type: "new_task",
			Message: &map[string]interface{}{
				"id":        task.ID,
				"task_name": task.TaskName,
				"payload":   task.Payload,
			},
		}

		messageBytes, err := json.Marshal(message)
		if err != nil {
			slog.Error("Failed to marshal task notification", "err", err)
			continue
		}

		var peonId string
		err = tp.db.QueryRow(`
			SELECT id FROM peon
			WHERE status = 'IDLE'
			ORDER BY last_heartbeat ASC
			LIMIT 1
		`).Scan(&peonId)

		if err == sql.ErrNoRows {
			slog.Info("Failed to find idle peon", "err", err)
		}
		if err != nil {
			slog.Info("Sending to peon with latest heartbeat")

			err = tp.db.QueryRow(`
				SELECT id FROM peon
				WHERE status != 'OFFLINE'
				ORDER BY last_heartbeat ASC
				LIMIT 1
			`).Scan(&peonId)
			if err != nil {
				slog.Error("Failed to find any peon", "err", err)
				continue
			}
		}

		slog.Info("Sending task to peon", "taskID", task.ID, "peonId", peonId)
		// Send task to peon
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
			rows, err := tp.db.Query(`
                            SELECT id, task_name, queue, payload
                            FROM bountyboard
                            WHERE status = 'PENDING'
                            ORDER BY created_at ASC
                            LIMIT 10
                        `)
			if err != nil {
				slog.Error("Error querying pending tasks", "err", err)
				continue
			}

			for rows.Next() {
				var task Task
				var payloadJSON string
				err := rows.Scan(&task.ID, &task.TaskName, &task.Queue, &payloadJSON)
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

				// Add to processing queue
				tp.taskQueue <- task
			}
			rows.Close()
		}
	}
}

func (tp *TaskProcessor) AddTask(task Task) {
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
	_, err := db.Exec("UPDATE peon SET status = 'OFFLINE' WHERE id = ?", peonID)
	if err != nil {
		slog.Error("Failed to mark peon offline", "err", err)
	}
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	var creds Credentials
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
	claims := Claims{
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

	json.NewEncoder(w).Encode(map[string]string{
		"token": signedToken,
	})
}

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenString := strings.TrimPrefix(authHeader, "Bearer ")

			// Parse and validate the token using the API key as secret
			token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
				}
				return []byte(os.Getenv("WORKCRAFT_API_KEY")), nil
			})

			if err != nil {
				slog.Error("Invalid JWT", "err", err)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			if claims, ok := token.Claims.(*Claims); ok && token.Valid {
				// Add the API key to the request for downstream handlers
				r.Header.Set("WORKCRAFT_API_KEY", claims.APIKey)
				next.ServeHTTP(w, r)
				return
			}
		}

		// Fall back to API key check for service-to-service calls
		token := r.Header.Get("WORKCRAFT_API_KEY")
		if token == "" {
			slog.Error("No authentication provided")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		if subtle.ConstantTimeCompare([]byte(token), []byte(hashedApiKey)) != 1 {
			slog.Error("Invalid API key provided")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	}
}

func setupCronJobs(db *sql.DB) {
	c := cron.New()

	c.AddFunc("* * * * *", func() {
		query := `UPDATE peon SET status = 'OFFLINE' WHERE last_heartbeat < datetime('now', '-1 minutes')`

		_, err := db.Exec(query)
		if err != nil {
			slog.Error("Failed to clean up dead peons", "err", err)
			return
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

func createPostTaskUpdateHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("id")
		if taskID == "" {
			slog.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}
		slog.Info("Received update for ", "id", taskID)

		var task Task
		err := json.NewDecoder(r.Body).Decode(&task)
		if err != nil {
			slog.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		_, err = db.Exec("UPDATE bountyboard SET status = ?, peon_id = ?, retry_count = ? WHERE id = ?", task.Status, task.PeonId, task.RetryCount, taskID)
		if err != nil {
			slog.Error("Failed to update task status", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
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

		var t TaskAcknowledgement
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
		slog.Info("Received statistics update")

		var stats struct {
			Type  string      `json:"type"`
			Value interface{} `json:"value"`
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

		query := `INSERT INTO stats (type, value) VALUES (?, ?)`
		_, err = db.Exec(query, stats.Type, valueJSON)
		if err != nil {
			slog.Error("Failed to insert statistics into database", "err", err)
		}

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("Statistics added"))
	}
}

func createGetAllPeonsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query("SELECT * FROM peon")
		if err != nil {
			slog.Error("Error querying peons", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var peons []Peon
		for rows.Next() {
			var p Peon
			err := rows.Scan(&p.Id, &p.Status, &p.LastHeartbeat, &p.CurrentTask, &p.Queues)
			if err != nil {
				slog.Error("Error scanning peon", "err", err)
				continue
			}
			peons = append(peons, p)
		}

		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(peons)
		if err != nil {
			slog.Error("Error encoding peons", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func createUpdatePeonHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var peon Peon
		err := json.NewDecoder(r.Body).Decode(&peon)
		if err != nil {
			slog.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		query := `UPDATE peon SET status = ?, last_heartbeat = ?, current_task = ?, queues = ? WHERE id = ?`
		_, err = db.Exec(query, peon.Status, peon.LastHeartbeat, peon.CurrentTask, peon.Queues, peon.Id)
		if err != nil {
			slog.Error("Failed to update peon", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)

	}
}

func createPostTaskHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task := Task{}
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
		task.Status = TaskStatusPending

		slog.Info("Creating task with ID and name", "id", task.ID, "name", task.TaskName)
		payloadJSON, err := json.Marshal(task.Payload)
		if err != nil {
			slog.Error("Failed to serialize payload", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		query := `INSERT INTO bountyboard (id, status, task_name, queue, payload, retry_on_failure, retry_limit) VALUES (?, ?, ?, ?, ?, ?, ?)`
		_, err = db.Exec(query, task.ID, task.Status, task.TaskName, task.Queue, payloadJSON, task.RetryOnFailure, task.RetryLimit)
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

func insertPeonIntoDb(db *sql.DB, peonId string) {
	query := `INSERT INTO peon (id) VALUES (?) ON CONFLICT (id) DO UPDATE SET status = 'IDLE'`
	_, err := db.Exec(query, peonId)
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

func createWsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Info("upgrade failed: ", "err", err)
			return
		}
		peonId := r.URL.Query().Get("peon")
		slog.Info("Peon with id connected", "id", peonId)

		insertPeonIntoDb(db, peonId)
		registry.Add(peonId, conn)

		defer func() {
			slog.Info("Peon disconnected", "id", peonId)
			registry.Remove(peonId)

			_, err := db.Exec("UPDATE peon SET status = 'OFFLINE' WHERE id = ?", peonId)
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

			var wsMsg WebSocketMessage
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
		`CREATE TABLE IF NOT EXISTS peon (
			id VARCHAR(36) PRIMARY KEY,
			status TEXT DEFAULT 'IDLE' CHECK (status IN ('IDLE', 'PREPARING', 'WORKING', 'OFFLINE')),
			last_heartbeat TIMESTAMP DEFAULT (datetime ('now')),
			current_task CHAR(36),
			queues TEXT
		);`,
		`CREATE TRIGGER IF NOT EXISTS peon_last_heartbeat_update AFTER
		UPDATE ON peon WHEN NEW.status != OLD.status BEGIN
		UPDATE peon
		SET last_heartbeat = datetime ('now')
		WHERE id = NEW.id;
		END;`,
		`CREATE TABLE IF NOT EXISTS bountyboard (
		    id CHAR(36) PRIMARY KEY,
		    status TEXT NOT NULL DEFAULT 'PENDING' CHECK (
		        status IN (
		            'PENDING',
		            'RUNNING',
		            'SUCCESS',
		            'FAILURE',
		            'INVALID'
		        )
		    ),
		    created_at TIMESTAMP DEFAULT (datetime ('now')),
		    updated_at TIMESTAMP DEFAULT (datetime ('now')),
		    task_name VARCHAR(255) NOT NULL,
		    peon_id VARCHAR(36),
		    queue VARCHAR(255) DEFAULT 'DEFAULT',
		    payload TEXT,
		    result TEXT,
		    retry_on_failure BOOLEAN DEFAULT FALSE,
		    retry_count INTEGER DEFAULT 0,
		    retry_limit INTEGER NOT NULL DEFAULT 0,
		    CONSTRAINT chk_retry_limit CHECK (retry_limit >= 0),
		    CONSTRAINT chk_retry_consistency CHECK (
		        (retry_on_failure = 0)
		        OR (
		            retry_on_failure = 1
		            AND retry_limit > 1
		        )
		    ),
		    FOREIGN KEY (peon_id) REFERENCES peon (id)
		)`,
		`CREATE TRIGGER IF NOT EXISTS bountyboard_updated_at AFTER
		UPDATE ON bountyboard BEGIN
		UPDATE bountyboard
		SET
		    updated_at = datetime ('now')
		WHERE
		    id = NEW.id;
		END`,
		`
		CREATE TABLE IF NOT EXISTS stats (
			id INTEGER PRIMARY KEY,
			type TEXT NOT NULL,
			value TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT (datetime ('now'))
		);
		`,
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
	defer db.Close() // Now this will close when the program ends

	setupDatabase(db)
	setupCronJobs(db)

	registry = NewConnectionRegistry(db)
	taskProcessor = NewTaskProcessor(db, registry)
	taskProcessor.Start()
	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	component := view.Index()

	http.Handle("/", templ.Handler(component))
	http.HandleFunc("/ws", createWsHandler(db))
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
	http.HandleFunc("GET /api/peons", authMiddleware(createGetAllPeonsHandler(db)))
	http.HandleFunc("POST /api/tasks", authMiddleware(createPostTaskHandler(db)))
	http.HandleFunc("POST /api/tasks/{id}/update", authMiddleware(createPostTaskUpdateHandler(db)))
	http.HandleFunc("POST /api/tasks/{id}/acknowledgement", authMiddleware(createPostTaskAcknowledgementHandler(db)))
	http.HandleFunc("POST /api/peons/{id}/statistics", authMiddleware(createPostStatisticsHandler(db)))
	slog.Info("Building Stronghold on port 6112")
	http.ListenAndServe(":6112", nil)
}
