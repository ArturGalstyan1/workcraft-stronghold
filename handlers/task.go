package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
	"github.com/Artur-Galstyan/workcraft-stronghold/views"
	"github.com/a-h/templ"
	"gorm.io/gorm"
)

func CreateTaskViewHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("id")
		if taskID == "" {
			slog.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}

		var task models.Task
		result := db.First(&task, "id = ?", taskID)

		if result.Error == gorm.ErrRecordNotFound {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}
		if result.Error != nil {
			slog.Error("Failed to fetch task", "err", result.Error)
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

func CreateTaskUpdateHandler(db *gorm.DB, eventSender *events.EventSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("id")
		if taskID == "" {
			slog.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}
		slog.Info("Received update for task", "id", taskID)

		var update struct {
			Status         *models.TaskStatus  `json:"status,omitempty"`
			PeonID         *string             `json:"peon_id,omitempty"`
			RetryCount     *int                `json:"retry_count,omitempty"`
			Result         interface{}         `json:"result,omitempty"`
			Queue          *string             `json:"queue,omitempty"`
			RetryLimit     *int                `json:"retry_limit,omitempty"`
			RetryOnFailure *bool               `json:"retry_on_failure,omitempty"`
			Payload        *models.TaskPayload `json:"payload,omitempty"`
		}

		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			slog.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		tx := db.Begin()
		if tx.Error != nil {
			slog.Error("Failed to begin transaction", "err", tx.Error)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		updates := map[string]interface{}{}
		if update.Status != nil {
			updates["status"] = *update.Status
		}
		if update.PeonID != nil {
			updates["peon_id"] = *update.PeonID
		}
		if update.RetryCount != nil {
			updates["retry_count"] = *update.RetryCount
		}
		if update.Queue != nil {
			updates["queue"] = *update.Queue
		}
		if update.RetryLimit != nil {
			updates["retry_limit"] = *update.RetryLimit
		}
		if update.RetryOnFailure != nil {
			updates["retry_on_failure"] = *update.RetryOnFailure
		}
		if update.Result != nil {
			resultJSON, err := json.Marshal(update.Result)
			if err != nil {
				tx.Rollback()
				slog.Error("Failed to marshal result", "err", err)
				http.Error(w, "Invalid result format", http.StatusBadRequest)
				return
			}
			updates["result"] = string(resultJSON)
		}
		if update.Payload != nil {
			var currentTask models.Task
			if err := tx.First(&currentTask, "id = ?", taskID).Error; err != nil {
				tx.Rollback()
				slog.Error("Failed to fetch current task", "err", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}

			// If current payload fields are nil, initialize them
			if currentTask.Payload.TaskArgs == nil {
				currentTask.Payload.TaskArgs = make([]interface{}, 0)
			}
			if currentTask.Payload.TaskKwargs == nil {
				currentTask.Payload.TaskKwargs = make(map[string]interface{})
			}
			if currentTask.Payload.PrerunHandlerArgs == nil {
				currentTask.Payload.PrerunHandlerArgs = make([]interface{}, 0)
			}
			if currentTask.Payload.PrerunHandlerKwargs == nil {
				currentTask.Payload.PrerunHandlerKwargs = make(map[string]interface{})
			}
			if currentTask.Payload.PostrunHandlerArgs == nil {
				currentTask.Payload.PostrunHandlerArgs = make([]interface{}, 0)
			}
			if currentTask.Payload.PostrunHandlerKwargs == nil {
				currentTask.Payload.PostrunHandlerKwargs = make(map[string]interface{})
			}

			mergedPayload := currentTask.Payload
			if update.Payload.TaskArgs != nil {
				mergedPayload.TaskArgs = update.Payload.TaskArgs
			}
			if update.Payload.TaskKwargs != nil {
				mergedPayload.TaskKwargs = update.Payload.TaskKwargs
			}
			if update.Payload.PrerunHandlerArgs != nil {
				mergedPayload.PrerunHandlerArgs = update.Payload.PrerunHandlerArgs
			}
			if update.Payload.PrerunHandlerKwargs != nil {
				mergedPayload.PrerunHandlerKwargs = update.Payload.PrerunHandlerKwargs
			}
			if update.Payload.PostrunHandlerArgs != nil {
				mergedPayload.PostrunHandlerArgs = update.Payload.PostrunHandlerArgs
			}
			if update.Payload.PostrunHandlerKwargs != nil {
				mergedPayload.PostrunHandlerKwargs = update.Payload.PostrunHandlerKwargs
			}

			payloadJSON, err := json.Marshal(mergedPayload)
			if err != nil {
				tx.Rollback()
				slog.Error("Failed to marshal payload", "err", err)
				http.Error(w, "Invalid payload format", http.StatusBadRequest)
				return
			}
			updates["payload"] = string(payloadJSON)
		}

		if len(updates) == 0 {
			tx.Rollback()
			http.Error(w, "No fields to update", http.StatusBadRequest)
			return
		}

		result := tx.Model(&models.Task{}).Where("id = ?", taskID).Updates(updates)
		if result.Error != nil {
			tx.Rollback()
			slog.Error("Failed to update task", "err", result.Error)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if result.RowsAffected == 0 {
			tx.Rollback()
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}

		var updatedTask models.Task
		if err := tx.First(&updatedTask, "id = ?", taskID).Error; err != nil {
			tx.Rollback()
			slog.Error("Failed to fetch updated task", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if err := tx.Commit().Error; err != nil {
			tx.Rollback()
			slog.Error("Failed to commit transaction", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		taskJSON, err := json.Marshal(updatedTask)
		if err != nil {
			slog.Error("Failed to serialize updated task", "err", err)
		} else {
			msg := fmt.Sprintf(`{"type": "task_update", "message": {"task": %s}}`,
				string(taskJSON))
			eventSender.BroadcastToChieftains(msg)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(updatedTask); err != nil {
			slog.Error("Failed to encode response", "err", err)
		}
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

		tx := db.Begin()
		if tx.Error != nil {
			slog.Error("Failed to begin transaction", "err", tx.Error)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

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

		if err := tx.Create(&task).Error; err != nil {
			tx.Rollback()
			slog.Error("Failed to create task", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

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

func CreateGetTasksHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("GET /api/tasks")
		queryString := r.URL.Query().Get("query")

		queryParams, err := utils.ParseTaskQuery(queryString)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid query: %v", err), http.StatusBadRequest)
			return
		}

		// Start with base query
		query := db.Model(&models.Task{})

		// Add filters
		if queryParams.Filter != nil {
			if queryParams.Filter.Status != nil && queryParams.Filter.Status.Value != nil {
				query = query.Where("status = ?", queryParams.Filter.Status.Value)
			}
			if queryParams.Filter.TaskName != nil && queryParams.Filter.TaskName.Value != nil {
				query = query.Where("task_name = ?", queryParams.Filter.TaskName.Value)
			}
			if queryParams.Filter.Queue != nil && queryParams.Filter.Queue.Value != nil {
				query = query.Where("queue = ?", queryParams.Filter.Queue.Value)
			}
			if queryParams.Filter.PeonID != nil && queryParams.Filter.PeonID.Value != nil {
				query = query.Where("peon_id = ?", queryParams.Filter.PeonID.Value)
			}
			if queryParams.Filter.CreatedAt != nil && queryParams.Filter.CreatedAt.Value != nil {
				query = query.Where("created_at = ?", queryParams.Filter.CreatedAt.Value)
			}
		}

		// Add ordering
		if queryParams.Order != nil && queryParams.Order.Field != "" {
			orderStr := fmt.Sprintf("%s %s", queryParams.Order.Field, strings.ToUpper(queryParams.Order.Dir))
			query = query.Order(orderStr)
		}

		// Count total items before pagination
		var totalItems int64
		if err := query.Count(&totalItems).Error; err != nil {
			slog.Error("Error counting tasks", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Add pagination
		offset := (queryParams.Page - 1) * queryParams.PerPage // Fix: Page should start from 1
		query = query.Limit(queryParams.PerPage).Offset(offset)

		// Execute query
		var tasks []models.Task
		if err := query.Find(&tasks).Error; err != nil {
			slog.Error("Error querying tasks", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Ensure tasks is never nil
		if tasks == nil {
			tasks = []models.Task{}
		}

		// Calculate total pages
		totalPages := (int(totalItems) + queryParams.PerPage - 1) / queryParams.PerPage

		// Prepare and send response
		response := models.PaginatedResponse{
			Page:       queryParams.Page,
			PerPage:    queryParams.PerPage,
			TotalItems: int(totalItems),
			TotalPages: totalPages,
			Items:      tasks,
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			slog.Error("Error encoding response", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func CreateGetTaskHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("GET /api/task/{id}")

		taskID := r.PathValue("id")
		if taskID == "" {
			slog.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}

		slog.Info("Fetching task", "id", taskID)

		var task models.Task
		result := db.First(&task, "id = ?", taskID)

		if result.Error == gorm.ErrRecordNotFound {
			slog.Error("Task not found")
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}
		if result.Error != nil {
			slog.Error("Failed to retrieve task", "err", result.Error)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(task); err != nil {
			slog.Error("Failed to encode task", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func CreateCancelTaskHandler(db *gorm.DB, eventSender *events.EventSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("POST /api/task/{id}/cancel")

		taskID := r.PathValue("id")
		if taskID == "" {
			slog.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}
		slog.Info("Received cancel for task", "id", taskID)

		var task models.Task
		result := db.First(&task, "id = ?", taskID)

		if result.Error == gorm.ErrRecordNotFound {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}
		if result.Error != nil {
			slog.Error("Failed to fetch task", "err", result.Error)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if task.Status != models.TaskStatusRunning {
			http.Error(w, "Task is not running", http.StatusBadRequest)
			return
		}

		if task.PeonID == nil {
			slog.Error("Task has no assigned peon")
			http.Error(w, "Task has no assigned peon", http.StatusBadRequest)
			return
		}

		msg := fmt.Sprintf(`{"type": "cancel_task", "data": "%s"}`, taskID)
		if err := eventSender.SendEvent(*task.PeonID, msg); err != nil {
			slog.Error("Failed to send cancel message", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		res := map[string]interface{}{
			"send": "true",
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(res); err != nil {
			slog.Error("Failed to encode response", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}
