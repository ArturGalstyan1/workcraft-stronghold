package handlers

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/sqls"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
)

func CreateSSEHandler(eventSender *events.EventSender, db *sql.DB) http.HandlerFunc {
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

		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		connectedJSON := fmt.Sprintf("{\"type\": \"connected\", \"connection_id\": \"%s\"}", connectionID)
		err := eventSender.SendEvent(connectionID, connectedJSON)
		if err != nil {
			slog.Error("Failed to send connected event", "err", err)
			return
		}

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

					err = sqls.CleanInconsistencies(db)
					if err != nil {
						slog.Error("Failed to clean inconsistencies", "err", err)
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
