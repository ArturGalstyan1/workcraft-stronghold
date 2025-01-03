package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/sqls"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
	"github.com/Artur-Galstyan/workcraft-stronghold/views"
	"github.com/a-h/templ"
	"gorm.io/gorm"
)

func CreateTaskViewHandler(db *sql.DB) http.HandlerFunc {
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
			&task.TaskName, &task.PeonID, &task.Queue, &payloadJSON,
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
		component := views.Task(task)
		templ.Handler(component).ServeHTTP(w, r)
	}
}

func TaskView(w http.ResponseWriter, r *http.Request) {
	component := views.Tasks()
	templ.Handler(component).ServeHTTP(w, r)
}

func CreateTaskUpdateHandler(db *sql.DB, eventSender *events.EventSender) http.HandlerFunc {
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
			&updatedTask.UpdatedAt, &updatedTask.TaskName, &updatedTask.PeonID,
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
					msg := fmt.Sprintf(`{"type": "task_update", "message": {"task": %s}}`,
						string(taskJSON))
					eventSender.BroadcastToChieftains(msg)
				}
			}
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func CreatePostTaskHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Received new task!")

		var task models.Task
		if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
			slog.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// Start a transaction
		tx := db.Begin()
		if tx.Error != nil {
			slog.Error("Failed to begin transaction", "err", tx.Error)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Validate and set defaults
		if task.TaskName == "" {
			tx.Rollback()
			http.Error(w, "task name is required", http.StatusBadRequest)
			return
		}
		if task.Status == "" {
			task.Status = models.TaskStatusPending
		}
		if task.Queue == "" {
			task.Queue = "DEFAULT"
		}
		if task.RetryLimit < 0 {
			tx.Rollback()
			http.Error(w, "retry limit cannot be negative", http.StatusBadRequest)
			return
		}

		// Create the task
		if err := tx.Create(&task).Error; err != nil {
			tx.Rollback()
			slog.Error("Failed to create task", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Create the queue entry
		queue := models.Queue{
			TaskID: task.ID,
			Queued: true,
		}
		if err := tx.Create(&queue).Error; err != nil {
			tx.Rollback()
			slog.Error("Failed to create queue", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Commit the transaction
		if err := tx.Commit().Error; err != nil {
			tx.Rollback()
			slog.Error("Failed to commit transaction", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(task); err != nil {
			slog.Error("Failed to encode task", "err", err)
		}
	}
}

func CreateGetTasksHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("GET /api/tasks")
		queryParams, err := utils.ParseTaskQuery(r.URL.Query().Get("query"))
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid query: %v", err), http.StatusBadRequest)
			return
		}

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
				&task.TaskName, &task.PeonID, &task.Queue, &payloadJSON,
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

func CreateGetTaskHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("GET /api/task/{id}")
		taskID := r.PathValue("id")

		if taskID == "" {
			slog.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}

		task, err := sqls.GetTaskByID(db, taskID)
		if err == sql.ErrNoRows {
			slog.Error("Task not found")
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}

		if err != nil {
			slog.Error("Failed to retrieve task", "err", err)
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

func CreateCancelTaskHandler(db *sql.DB, eventSender *events.EventSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("POST /api/task/{id}/cancel")
		taskID := r.PathValue("id")

		if taskID == "" {
			slog.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}

		slog.Info("Received cancel for task", "id", taskID)
		task, err := sqls.GetTaskByID(db, taskID)
		if err == sql.ErrNoRows {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}
		if err != nil {
			slog.Error("Failed to fetch task", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if task.Status != "RUNNING" {
			http.Error(w, "Task is not running", http.StatusBadRequest)
			return
		}

		msg := fmt.Sprintf(`{"type": "cancel_task", "data": "%s"}`,
			taskID)
		err = eventSender.SendEvent(*task.PeonID, msg)

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
