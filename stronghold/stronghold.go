package stronghold

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/sqls"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

type Stronghold struct {
	hashedAPIKey string
	db           *gorm.DB
	eventSender  *events.EventSender
}

func NewStronghold(apiKey string, db *gorm.DB, eventSender *events.EventSender) *Stronghold {

	hasher := sha256.New()
	hasher.Write([]byte(apiKey))
	hashedAPIKey := hex.EncodeToString(hasher.Sum(nil))

	return &Stronghold{
		hashedAPIKey: hashedAPIKey,
		db:           db,
		eventSender:  eventSender,
	}

}

func (s *Stronghold) SetupCRONJobs() {

	c := cron.New()
	var cronMutex sync.Mutex

	c.AddFunc("* * * * *", func() {
		cronMutex.Lock()
		defer cronMutex.Unlock()
		result := s.db.Model(&models.Peon{}).
			Where("last_heartbeat < datetime('now', '-1 minutes')").
			Updates(map[string]interface{}{
				"status":       "OFFLINE",
				"current_task": nil,
			})
		if result.Error != nil {
			slog.Error("Failed to clean up dead peons", "err", result.Error)
			return
		}

	})

	c.AddFunc("* * * * *", func() {
		cronMutex.Lock()
		defer cronMutex.Unlock()

		err := utils.CleanInconsistencies(s.db)
		if err != nil {
			slog.Error("Failed to clean up inconsistencies", "err", err)
			return
		}

	})

	c.Start()
}

func (s *Stronghold) Run() {
	go s.SetupCRONJobs()
	go s.SendPendingTasksInterval()
}

func (s *Stronghold) SendPendingTasks() {
	tasks, err := sqls.GetNotYetSentOutTasks(s.db)
	if err != nil {
		slog.Error("Failed to get tasks", "err", err)
		return
	}
	if len(tasks) == 0 {
		return
	}

	for _, task := range tasks {
		peon, err := sqls.GetAvailablePeon(s.db, task.Queue)
		if err != nil {
			slog.Info("Failed to get available peon, skipping. ", "err", err)
			return
		}

		taskJSON, err := json.Marshal(task)
		if err != nil {
			slog.Error("Failed to marshal task", "err", err)
			return
		}

		msgString := fmt.Sprintf(`{"type": "new_task", "data": %s}`, string(taskJSON))
		s.eventSender.SendEvent(peon.ID, msgString)
	}
}

func (s *Stronghold) PutPendingTasksIntoQueue() {
	var pendingTasks []models.Task
	err := s.db.Where("status = ? AND id NOT IN (SELECT task_id FROM queues)",
		models.TaskStatusPending).
		Find(&pendingTasks).Error

	if err != nil {
		slog.Error("Failed to get pending tasks", "err", err)
		return
	}

	if len(pendingTasks) == 0 {
		return
	}

	tx := s.db.Begin()
	if tx.Error != nil {
		slog.Error("Failed to begin transaction", "err", tx.Error)
		return
	}
	defer tx.Rollback()

	for _, task := range pendingTasks {
		queue := models.Queue{
			TaskID:     task.ID,
			SentToPeon: false,
		}
		if err := tx.Create(&queue).Error; err != nil {
			slog.Error("Failed to insert task into queue", "taskID", task.ID, "err", err)
			return
		}
	}

	if err := tx.Commit().Error; err != nil {
		slog.Error("Failed to commit transaction", "err", err)
	}
}

func (s *Stronghold) SendPendingTasksInterval() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.PutPendingTasksIntoQueue()
			s.SendPendingTasks()
		}
	}
}
