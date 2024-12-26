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
	slog.Info("Registering", "ID", ID)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connections[ID] = w
	s.controllers[ID] = rc
}

func (s *EventSender) Unregister(ID string) {
	slog.Info("Unregistering", "ID", ID)
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

func markPeonOffline(db *sql.DB, peonID string) error {
	_, err := db.Exec(utils.MarkPeonAsOffline(), peonID)
	return err
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
					fmt.Sprintf(`{"type": "task_update", "message": {"task": %s}}`,
						string(taskJSON))
					// TODO: Notify Chieftain
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

func createTestHandler(eventSender *EventSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("GET /test")
		eventSender.BroadcastToChieftains("HI")
		w.Write([]byte("Success!"))
	}
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
			fmt.Sprintf(`{"type": "peon_update", "message": {"peon": %s}}`,
				string(peonJSON))
			// TODO: Notify Chieftain
			if err != nil {
				slog.Error("Failed to send peon update to chieftain", "err", err)
			}
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func createCancelTaskHandler(db *sql.DB) http.HandlerFunc {
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
		err := db.QueryRow("SELECT peon_id, status FROM bountyboard WHERE id = ?", taskID).Scan(&peonID, &status)
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

		// data := map[string]interface{}{
		// 	"type":    "cancel_task",
		// 	"message": taskID,
		// }

		// bytes, err := json.Marshal(data)

		if err != nil {
			slog.Error("Failed to marshal cancel message", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// TODO: Send cancel SSE
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

		// TODO: Add task

	}
}

func insertPeonIntoDb(db *sql.DB, peonID string, peonQueues string) error {
	var queues interface{} = nil
	if peonQueues != "NULL" {
		queues = peonQueues
	}

	_, err := db.Exec(utils.InsertPeon(), peonID, queues)
	return err
}

func updatePeonHeartbeat(db *sql.DB, peonId string) {
	query := `UPDATE peon SET last_heartbeat = datetime('now') WHERE id = ?`
	_, err := db.Exec(query, peonId)
	if err != nil {
		slog.Error("Error updating peon with", "id", peonId)
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
			connectionID = peonID
			err := insertPeonIntoDb(db, peonID, queues)
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
					err := markPeonOffline(db, peonID)
					if err != nil {
						slog.Error("Failed to mark peon offline", "err", err)
						return
					}
				}
				return
			case <-ticker.C:
				// msg := fmt.Sprintf("{\"random_number\": %s}", "Hello!")
				// err := eventSender.SendEvent(connectionID, msg)
				// if err != nil {
				// 	slog.Error("Failed to send event", "err", err)
				// 	return
				// }
				slog.Debug("Ticker!")
			}
		}
	}
}

func main() {
	db, err := sql.Open("sqlite3", "workcraft.db")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	setupDatabase(db)
	setupCronJobs(db)

	eventSender := NewEventSender()

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	component := view.Index()
	http.Handle("/", templ.Handler(component))
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
	http.HandleFunc("POST /api/task/{id}/cancel", authMiddleware(createCancelTaskHandler(db)))
	http.HandleFunc("POST /api/task/{id}/update", authMiddleware(createTaskUpdateHandler(db)))
	http.HandleFunc("POST /api/task/{id}/acknowledgement", authMiddleware(createPostTaskAcknowledgementHandler(db)))

	http.HandleFunc("GET /api/test", authMiddleware(createTestHandler(eventSender)))

	http.HandleFunc("/events", createSSEHandler(eventSender, db))

	slog.Info("Building Stronghold on port 6112")
	http.ListenAndServe(":6112", nil)
}
