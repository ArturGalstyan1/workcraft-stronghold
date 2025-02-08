package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/logger"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/sqls"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
	"github.com/Artur-Galstyan/workcraft-stronghold/views"
	"github.com/a-h/templ"
	"gorm.io/gorm"
)

func CreateGetPeonsHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// logger.Log.Info("GET /api/peons")

		queryParams, err := utils.ParsePeonQuery(r.URL.Query().Get("query"))
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid query: %v", err), http.StatusBadRequest)
			return
		}

		response, err := sqls.GetPeons(db, *queryParams)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Log.Error("Error encoding response", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func CreateGetPeonHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peonID := r.PathValue("id")
		if peonID == "" {
			logger.Log.Error("Peon ID is required")
			http.Error(w, "Peon ID is required", http.StatusBadRequest)
			return
		}

		peon, err := sqls.GetPeon(db, peonID)
		if err != nil {
			logger.Log.Error("Error querying peon", "err", err)
			http.Error(w, "Unable to find peon"+fmt.Sprintf("%v", err), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(peon); err != nil {
			logger.Log.Error("Failed to encode peon", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func CreateGetPeonTaskHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peonID := r.PathValue("id")
		if peonID == "" {
			logger.Log.Error("Peon ID is required")
			http.Error(w, "Peon ID is required", http.StatusBadRequest)
			return
		}

		tasks, err := sqls.GetTasksByPeonID(db, peonID)
		if err != nil {
			logger.Log.Error("Error querying tasks", "err", err)
			http.Error(w, "Unable to find tasks", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(tasks); err != nil {
			logger.Log.Error("Failed to encode tasks", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func CreateUpdatePeonHandler(db *gorm.DB, eventSender *events.EventSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peonID := r.PathValue("id")
		if peonID == "" {
			logger.Log.Error("Peon ID is required")
			http.Error(w, "Peon ID is required", http.StatusBadRequest)
			return
		}
		var rawJSON map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&rawJSON); err != nil {
			logger.Log.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		var update models.PeonUpdate
		jsonBytes, _ := json.Marshal(rawJSON)
		if err := json.Unmarshal(jsonBytes, &update); err != nil {
			logger.Log.Error("Failed to parse update data", "err", err)
			http.Error(w, "Invalid update data", http.StatusBadRequest)
			return
		}

		_, update.StatusSet = rawJSON["status"]
		_, update.HeartbeatSet = rawJSON["last_heartbeat"]
		_, update.CurrentTaskSet = rawJSON["current_task"]
		_, update.QueuesSet = rawJSON["queues"]

		updatedPeon, err := sqls.UpdatePeon(db, peonID, update)
		if err != nil {
			logger.Log.Error("Failed to update peon", "err", err)
			http.Error(w, "Failed to update peon", http.StatusInternalServerError)
			return
		}

		peonJSON, err := json.Marshal(updatedPeon)
		if err != nil {
			logger.Log.Error("Failed to serialize updated peon", "err", err)
			http.Error(w, "Failed to serialize updated peon", http.StatusInternalServerError)
			return
		}

		msg := fmt.Sprintf(`{"type": "peon_update", "message": {"peon": %s}}`,
			string(peonJSON))
		eventSender.BroadcastToChieftains(msg)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(updatedPeon); err != nil {
			logger.Log.Error("Failed to encode updated peon", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func CreatePostStatisticsHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var stats models.Stats
		if err := json.NewDecoder(r.Body).Decode(&stats); err != nil {
			logger.Log.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		s, err := sqls.CreateStats(db, stats)
		if err != nil {
			logger.Log.Error("Failed to create statistics", "err", err)
			http.Error(w, "Failed to create statistics", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(s); err != nil {
			logger.Log.Error("Failed to encode statistics", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func CreatePeonViewHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peonID := r.PathValue("id")
		if peonID == "" {
			logger.Log.Error("Peon ID is required")
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
