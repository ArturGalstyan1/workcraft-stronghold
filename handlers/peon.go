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
)

func CreateGetPeonsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("GET /api/peons")
		queryParams, err := utils.ParsePeonQuery(r.URL.Query().Get("query"))
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid query: %v", err), http.StatusBadRequest)
			return
		}

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

func CreateGetPeonHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peonID := r.PathValue("id")

		if peonID == "" {
			slog.Error("Peon ID is required")
			http.Error(w, "Peon ID is required", http.StatusBadRequest)
			return
		}

		var peon models.Peon

		peon, err := sqls.GetPeonByID(db, peonID)
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

func CreateGetPeonTaskHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		peonID := r.PathValue("id")

		if peonID == "" {
			slog.Error("Peon ID is required")
			http.Error(w, "Peon ID is required", http.StatusBadRequest)
			return
		}

		tasks, err := sqls.GetTasksByPeonID(db, peonID)
		if err != nil {
			slog.Error("Error querying tasks", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
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

func CreateUpdatePeonHandler(db *sql.DB, eventSender *events.EventSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peonID := r.PathValue("id")
		var update models.PeonUpdate
		err := json.NewDecoder(r.Body).Decode(&update)
		if err != nil {
			slog.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		updatedPeon, err := sqls.UpdatePeon(db, peonID, update)
		if err != nil {
			slog.Error("Failed to update peon", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		peonJSON, err := json.Marshal(updatedPeon)
		if err != nil {
			slog.Error("Failed to serialize updated peon", "err", err)
			http.Error(w, "Failed to serialize updated peon", http.StatusInternalServerError)
			return
		}

		msg := fmt.Sprintf(`{"type": "peon_update", "message": {"peon": %s}}`,
			string(peonJSON))
		eventSender.BroadcastToChieftains(msg)

		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(updatedPeon)
		if err != nil {
			slog.Error("Failed to encode updated peon", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}

func CreatePostStatisticsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// slog.Info("Received statistics update")
		var stats models.Stats
		err := json.NewDecoder(r.Body).Decode(&stats)
		if err != nil {
			slog.Error("Failed to decode request body", "err", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		err = sqls.CreateStats(db, stats)
		if err != nil {
			slog.Error("Failed to insert stats", "err", err)
			http.Error(w, "Failed to insert stats", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("Statistics added"))
	}
}

func PeonView(w http.ResponseWriter, r *http.Request) {
	peonID := r.PathValue("id")

	if peonID == "" {
		slog.Error("Peon ID is required")
		http.Error(w, "Peon ID is required", http.StatusBadRequest)
		return
	}

	component := views.Peon(peonID)
	templ.Handler(component).ServeHTTP(w, r)
}

func PeonsView(w http.ResponseWriter, r *http.Request) {
	component := views.Peons()
	templ.Handler(component).ServeHTTP(w, r)
}
