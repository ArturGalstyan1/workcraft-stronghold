package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/logger"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/sqls"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
	"gorm.io/gorm"
)

func CreateSSEHandler(eventSender *events.EventSender, db *gorm.DB) http.HandlerFunc {
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
			logger.Log.Info("Peon connected", "peon_id", peonID)
			connectionID = peonID

			peon := models.Peon{
				Status: "IDLE",
				Queues: &queues,
			}
			peon.ID = peonID

			_, err := sqls.CreatePeon(db, peon)
			if err != nil {
				logger.Log.Error("Failed to create peon", "err", err)
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
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Keep-Alive", "timeout=600, max=1000")

		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		connectedJSON := fmt.Sprintf("{\"type\": \"connected\", \"connection_id\": \"%s\"}", connectionID)
		if err := eventSender.SendEvent(connectionID, connectedJSON); err != nil {
			logger.Log.Error("Failed to send connected event", "err", err)
			return
		}

		connClosed := r.Context().Done()
		for {
			select {
			case <-connClosed:
				if connectionType == "peon" {
					logger.Log.Info("Peon disconnected", "peon_id", peonID)

					offlineStatus := "OFFLINE"
					result := db.Model(&models.Peon{}).
						Where("id = ?", peonID).
						Update("status", offlineStatus)

					if result.Error != nil {
						logger.Log.Error("Failed to mark peon offline", "err", result.Error)
						return
					}

					if err := utils.CleanInconsistencies(db); err != nil {
						logger.Log.Error("Failed to clean inconsistencies", "err", err)
						return
					}
				}
				return

			}
		}
	}
}
