package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Artur-Galstyan/workcraft-stronghold/errs"
	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/logger"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/sqls"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
	"github.com/Artur-Galstyan/workcraft-stronghold/views"
	"github.com/a-h/templ"
	"gorm.io/gorm"
)

func CreateTaskViewHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("id")
		if taskID == "" {
			logger.Log.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}

		task, err := sqls.GetTask(db, taskID)
		if err != nil {
			logger.Log.Error("Error querying task", "err", err)
			http.Error(w, "Unable to find task", http.StatusNotFound)
			return
		}

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
		logger.Log.Info("POST /api/task/{id}/update")
		taskID := r.PathValue("id")
		if taskID == "" {
			logger.Log.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}

		var rawJSON map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&rawJSON); err != nil {
			logger.Log.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		logger.Log.Info("Received task update", "data", rawJSON)

		jsonBytes, err := json.Marshal(rawJSON)
		if err != nil {
			logger.Log.Error("Failed to marshal JSON", "err", err)
			http.Error(w, "Invalid request data", http.StatusBadRequest)
			return
		}

		var update models.TaskUpdate
		if err := json.Unmarshal(jsonBytes, &update); err != nil {
			logger.Log.Error("Failed to parse update data", "err", err)
			http.Error(w, "Invalid update data", http.StatusBadRequest)
			return
		}

		// Set the flags based on field presence
		_, update.StatusSet = rawJSON["status"]
		_, update.TaskNameSet = rawJSON["task_name"]
		_, update.PeonIDSet = rawJSON["peon_id"]
		_, update.QueueSet = rawJSON["queue"]
		_, update.PayloadSet = rawJSON["payload"]
		_, update.ResultSet = rawJSON["result"]
		_, update.RetryOnFailureSet = rawJSON["retry_on_failure"]
		_, update.RetryCountSet = rawJSON["retry_count"]
		_, update.RetryLimitSet = rawJSON["retry_limit"]
		_, update.LogsSet = rawJSON["logs"]

		updatedTask, err := sqls.UpdateTask(db, taskID, update)
		if err != nil {
			status, msg := errs.Get(err)
			logger.Log.Error("Failed to update task", "err", err)
			http.Error(w, msg, status)
			return
		}

		taskJSON, err := json.Marshal(updatedTask)
		if err != nil {
			logger.Log.Error("Failed to serialize updated task", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		msg := fmt.Sprintf(`{"type": "task_update", "message": {"task": %s}}`,
			string(taskJSON))
		eventSender.BroadcastToChieftains(msg)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(updatedTask); err != nil {
			logger.Log.Error("Failed to encode response", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}
}

func CreatePostTaskHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Log.Info("Received new task!")

		var task models.Task
		if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
			logger.Log.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		task, err := sqls.CreateTask(db, task)
		if err != nil {
			logger.Log.Error("Failed to create task", "err", err)
			http.Error(w, "Failed to create task", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(task); err != nil {
			logger.Log.Error("Failed to encode task", "err", err)
		}
	}
}

func CreateGetTasksHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		queryString := r.URL.Query().Get("query")
		queryParams, err := utils.ParseTaskQuery(queryString)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid query: %v", err), http.StatusBadRequest)
			return
		}
		logger.Log.Info("queryParams", "queryParams", queryParams)
		response, err := sqls.GetTasks(db, *queryParams)
		if err != nil {
			logger.Log.Error("Failed to fetch tasks", "err", err.Error())
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		responseMap := map[string]interface{}{
			"page":        response.Page,
			"per_page":    response.PerPage,
			"total_items": response.TotalItems,
			"total_pages": response.TotalPages,
			"items":       response.Items,
		}

		logger.Log.Info("total items", "total items", response.TotalItems)

		if err := json.NewEncoder(w).Encode(responseMap); err != nil {
			logger.Log.Error("Error encoding response map: " + err.Error())
			return
		}
	}
}

func CreateGetTaskHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Log.Info("GET /api/task/{id}")

		taskID := r.PathValue("id")
		if taskID == "" {
			logger.Log.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}

		logger.Log.Info("Fetching task", "id", taskID)

		task, err := sqls.GetTask(db, taskID)
		if err != nil {
			logger.Log.Error("Failed to fetch task", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(task); err != nil {
			logger.Log.Error("Failed to encode task", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func CreateCancelTaskHandler(db *gorm.DB, eventSender *events.EventSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Log.Info("POST /api/task/{id}/cancel")

		taskID := r.PathValue("id")
		if taskID == "" {
			logger.Log.Error("Task ID is required")
			http.Error(w, "Task ID is required", http.StatusBadRequest)
			return
		}
		logger.Log.Info("Received cancel for task", "id", taskID)

		task, err := sqls.GetTask(db, taskID)
		if err != nil {
			logger.Log.Error("Failed to fetch task", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		msg := fmt.Sprintf(`{"type": "cancel_task", "data": "%s"}`, taskID)
		if err := eventSender.SendEvent(*task.PeonID, msg); err != nil {
			logger.Log.Error("Failed to send cancel message", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		res := map[string]interface{}{
			"send": "true",
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(res); err != nil {
			logger.Log.Error("Failed to encode response", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}
