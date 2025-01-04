package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
	"github.com/Artur-Galstyan/workcraft-stronghold/views"
	"github.com/a-h/templ"
	"gorm.io/gorm"
)

func CreateGetPeonsHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("GET /api/peons")

		queryParams, err := utils.ParsePeonQuery(r.URL.Query().Get("query"))
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid query: %v", err), http.StatusBadRequest)
			return
		}

		query := db.Model(&models.Peon{})

		if queryParams.Filter != nil {
			if queryParams.Filter.Status != nil {
				query = utils.ApplyFilterCondition(query, "status", queryParams.Filter.Status)
			}
			if queryParams.Filter.LastHeartbeat != nil {
				query = utils.ApplyFilterCondition(query, "last_heartbeat", queryParams.Filter.LastHeartbeat)
			}
			if queryParams.Filter.CurrentTask != nil {
				if queryParams.Filter.CurrentTask.Op == models.FilterOpIn ||
					queryParams.Filter.CurrentTask.Op == models.FilterOpNotIn {
					query.Where("current_task "+string(queryParams.Filter.CurrentTask.Op)+" (?)",
						queryParams.Filter.CurrentTask.Value)
				} else {
					query = utils.ApplyFilterCondition(query, "current_task", queryParams.Filter.CurrentTask)
				}
			}
			if queryParams.Filter.Queues != nil {
				query = utils.ApplyFilterCondition(query, "queues", queryParams.Filter.Queues)
			}
		}

		var totalItems int64
		if err := query.Count(&totalItems).Error; err != nil {
			slog.Error("Error counting peons", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if queryParams.Order != nil {
			query = query.Order(fmt.Sprintf("%s %s", queryParams.Order.Field, queryParams.Order.Dir))
		}
		offset := queryParams.Page * queryParams.PerPage
		query = query.Limit(queryParams.PerPage).Offset(offset)

		var peons []models.Peon
		if err := query.Find(&peons).Error; err != nil {
			slog.Error("Error querying peons", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if peons == nil {
			peons = []models.Peon{}
		}

		totalPages := (int(totalItems) + queryParams.PerPage - 1) / queryParams.PerPage

		response := models.PaginatedResponse{
			Page:       queryParams.Page,
			PerPage:    queryParams.PerPage,
			TotalItems: int(totalItems),
			TotalPages: totalPages,
			Items:      peons,
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			slog.Error("Error encoding response", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func CreateGetPeonHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peonID := r.PathValue("id")
		if peonID == "" {
			slog.Error("Peon ID is required")
			http.Error(w, "Peon ID is required", http.StatusBadRequest)
			return
		}

		var peon models.Peon
		result := db.First(&peon, "id = ?", peonID)

		if result.Error == gorm.ErrRecordNotFound {
			http.Error(w, "Peon not found", http.StatusNotFound)
			return
		}
		if result.Error != nil {
			slog.Error("Failed to fetch peon", "err", result.Error)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(peon); err != nil {
			slog.Error("Failed to encode peon", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func CreateGetPeonTaskHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peonID := r.PathValue("id")
		if peonID == "" {
			slog.Error("Peon ID is required")
			http.Error(w, "Peon ID is required", http.StatusBadRequest)
			return
		}

		var tasks []models.Task
		result := db.Where("peon_id = ?", peonID).Find(&tasks)
		if result.Error != nil {
			slog.Error("Error querying tasks", "err", result.Error)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if tasks == nil {
			tasks = []models.Task{}
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(tasks); err != nil {
			slog.Error("Failed to encode tasks", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func CreateUpdatePeonHandler(db *gorm.DB, eventSender *events.EventSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peonID := r.PathValue("id")
		if peonID == "" {
			slog.Error("Peon ID is required")
			http.Error(w, "Peon ID is required", http.StatusBadRequest)
			return
		}

		var update struct {
			Status        *string `json:"status,omitempty"`
			LastHeartbeat *string `json:"last_heartbeat,omitempty"`
			CurrentTask   *string `json:"current_task,omitempty"`
			Queues        *string `json:"queues,omitempty"`
		}

		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			slog.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// Start transaction
		tx := db.Begin()
		if tx.Error != nil {
			slog.Error("Failed to begin transaction", "err", tx.Error)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Build updates map
		updates := map[string]interface{}{}
		if update.Status != nil {
			updates["status"] = *update.Status
		}
		if update.LastHeartbeat != nil {
			updates["last_heartbeat"] = *update.LastHeartbeat
		}
		if update.CurrentTask != nil {
			updates["current_task"] = *update.CurrentTask
		}
		if update.Queues != nil {
			updates["queues"] = *update.Queues
		}

		if len(updates) == 0 {
			tx.Rollback()
			http.Error(w, "No fields to update", http.StatusBadRequest)
			return
		}

		// Perform the update
		result := tx.Model(&models.Peon{}).Where("id = ?", peonID).Updates(updates)
		if result.Error != nil {
			tx.Rollback()
			slog.Error("Failed to update peon", "err", result.Error)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if result.RowsAffected == 0 {
			tx.Rollback()
			http.Error(w, "Peon not found", http.StatusNotFound)
			return
		}

		// Fetch updated peon for response and websocket notification
		var updatedPeon models.Peon
		if err := tx.First(&updatedPeon, "id = ?", peonID).Error; err != nil {
			tx.Rollback()
			slog.Error("Failed to fetch updated peon", "err", err)
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

		// Broadcast update
		peonJSON, err := json.Marshal(updatedPeon)
		if err != nil {
			slog.Error("Failed to serialize updated peon", "err", err)
			http.Error(w, "Failed to serialize updated peon", http.StatusInternalServerError)
			return
		}

		msg := fmt.Sprintf(`{"type": "peon_update", "message": {"peon": %s}}`,
			string(peonJSON))
		eventSender.BroadcastToChieftains(msg)

		// Send response
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(updatedPeon); err != nil {
			slog.Error("Failed to encode updated peon", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func CreatePostStatisticsHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var stats models.Stats
		if err := json.NewDecoder(r.Body).Decode(&stats); err != nil {
			slog.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if err := db.Create(&stats).Error; err != nil {
			slog.Error("Failed to insert stats", "err", err)
			http.Error(w, "Failed to insert stats", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("Statistics added"))
	}
}

func CreatePeonViewHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peonID := r.PathValue("id")
		if peonID == "" {
			slog.Error("Peon ID is required")
			http.Error(w, "Peon ID is required", http.StatusBadRequest)
			return
		}
		component := views.Peon(peonID)
		templ.Handler(component).ServeHTTP(w, r)
	}
}

func CreatePeonsViewHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		component := views.Peons()
		templ.Handler(component).ServeHTTP(w, r)
	}
}
