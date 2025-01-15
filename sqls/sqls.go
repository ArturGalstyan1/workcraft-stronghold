package sqls

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/errs"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func CreatePeon(db *gorm.DB, p models.Peon) (models.Peon, error) {
	if p.Status == "" {
		p.Status = "IDLE"
	}
	p.LastHeartbeat = time.Now().UTC().Format("2006-01-02T15:04:05.999999")

	// Define which columns to update on conflict
	result := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}}, // primary key
		DoUpdates: clause.AssignmentColumns([]string{
			"status",
			"last_heartbeat",
			"current_task",
			"queues",
			"updated_at",
		}),
	}).Create(&p)

	if result.Error != nil {
		return models.Peon{}, fmt.Errorf("failed to upsert peon: %w", result.Error)
	}

	return p, nil
}

func GetPeon(db *gorm.DB, ID string) (models.Peon, error) {
	var peon models.Peon
	result := db.First(&peon, "id = ?", ID)
	if result.Error != nil {
		return models.Peon{}, fmt.Errorf("failed to find peon: %w", result.Error)
	}
	return peon, nil
}

func UpdatePeon(db *gorm.DB, peonID string, partialPeon models.PeonUpdate) (models.Peon, error) {
	tx := db.Begin()
	if tx.Error != nil {
		return models.Peon{}, fmt.Errorf("failed to start transaction: %w", tx.Error)
	}

	updates := map[string]interface{}{}

	if partialPeon.StatusSet {
		updates["status"] = partialPeon.Status
	}
	if partialPeon.HeartbeatSet {
		updates["last_heartbeat"] = partialPeon.LastHeartbeat
	}
	if partialPeon.CurrentTaskSet {
		updates["current_task"] = partialPeon.CurrentTask
	}
	if partialPeon.QueuesSet {
		updates["queues"] = partialPeon.Queues
	}

	if len(updates) == 0 {
		tx.Rollback()
		return models.Peon{}, fmt.Errorf("no fields to update")
	}

	result := tx.Model(&models.Peon{}).Where("id = ?", peonID).Updates(updates)
	if result.Error != nil {
		tx.Rollback()
		return models.Peon{}, fmt.Errorf("failed to update peon: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		tx.Rollback()
		return models.Peon{}, fmt.Errorf("peon not found")
	}

	var updatedPeon models.Peon
	if err := tx.First(&updatedPeon, "id = ?", peonID).Error; err != nil {
		tx.Rollback()
		return models.Peon{}, fmt.Errorf("failed to fetch updated peon: %w", err)
	}

	if err := tx.Commit().Error; err != nil {
		return models.Peon{}, fmt.Errorf("failed to commit transaction: %w", err)
	}

	if partialPeon.StatusSet && *partialPeon.Status == "OFFLINE" && updatedPeon.CurrentTask != nil {
		pendingStatus := "PENDING"
		_, err := UpdateTask(db, *updatedPeon.CurrentTask, models.TaskUpdate{
			Status:    &pendingStatus,
			StatusSet: true,
			PeonID:    nil,
			PeonIDSet: true,
		})
		if err != nil {
			return models.Peon{}, fmt.Errorf("failed to update current task: %w", err)
		}

		result = db.Model(&models.Peon{}).Where("id = ?", peonID).Update("current_task", nil)
		if result.Error != nil {
			return models.Peon{}, fmt.Errorf("failed to update current task: %w", result.Error)
		}
	} else if partialPeon.StatusSet && *partialPeon.Status == "WORKING" {
		if updatedPeon.CurrentTask == nil {
			return models.Peon{}, fmt.Errorf("current task is required if status is WORKING")
		}

	}

	return updatedPeon, nil
}

func GetPeons(db *gorm.DB, queryParams models.PeonQuery) (models.PaginatedResponse, error) {
	query := db.Model(&models.Peon{})
	if queryParams.Page <= 0 {
		queryParams.Page = 1
	}
	if queryParams.PerPage <= 0 {
		queryParams.PerPage = 10
	}
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
		return models.PaginatedResponse{}, fmt.Errorf("failed to count peons: %w", err)
	}

	if queryParams.Order != nil {
		query = query.Order(fmt.Sprintf("%s %s", queryParams.Order.Field, queryParams.Order.Dir))
	}
	offset := (queryParams.Page - 1) * queryParams.PerPage
	query = query.Limit(queryParams.PerPage).Offset(offset)

	var peons []models.Peon
	if err := query.Find(&peons).Error; err != nil {
		return models.PaginatedResponse{}, fmt.Errorf("failed to find peons: %w", err)
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
	return response, nil
}

func CreateTask(db *gorm.DB, task models.Task) (models.Task, error) {
	if task.TaskName == "" {
		return models.Task{}, fmt.Errorf("task name is required")
	}

	if task.Status == "" {
		task.Status = "PENDING"
	}
	if task.Queue == "" {
		task.Queue = "DEFAULT"
	}
	if task.RetryLimit < 0 {
		return models.Task{}, fmt.Errorf("retry limit cannot be negative")
	}

	result := db.Create(&task)
	if result.Error != nil {
		return models.Task{}, fmt.Errorf("failed to create task: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return models.Task{}, fmt.Errorf("no rows affected when creating task")
	}

	resultQueue := db.Create(&models.Queue{
		TaskID:     task.ID,
		SentToPeon: false,
	})

	if resultQueue.Error != nil {
		return models.Task{}, fmt.Errorf("failed to create task in queue: %w", resultQueue.Error)
	}

	return task, nil
}

func GetTask(db *gorm.DB, taskID string) (models.Task, error) {
	var task models.Task
	result := db.First(&task, "id = ?", taskID)
	if result.Error != nil {
		return models.Task{}, fmt.Errorf("failed to find task: %w", result.Error)
	}
	return task, nil
}

func GetTasks(db *gorm.DB, queryParams models.TaskQuery) (models.PaginatedResponse, error) {
	if queryParams.Page <= 0 {
		queryParams.Page = 1
	}
	if queryParams.PerPage <= 0 {
		queryParams.PerPage = 10
	}
	query := db.Model(&models.Task{})
	if queryParams.Filter != nil {
		if queryParams.Filter.Status != nil {
			query = utils.ApplyFilterCondition(query, "status", queryParams.Filter.Status)
		}
		if queryParams.Filter.CreatedAt != nil {
			query = utils.ApplyFilterCondition(query, "created_at", queryParams.Filter.CreatedAt)
		}
		if queryParams.Filter.TaskName != nil {
			query = utils.ApplyFilterCondition(query, "task_name", queryParams.Filter.TaskName)
		}
		if queryParams.Filter.Queue != nil {
			query = utils.ApplyFilterCondition(query, "queue", queryParams.Filter.Queue)
		}
		if queryParams.Filter.PeonID != nil {
			query = utils.ApplyFilterCondition(query, "peon_id", queryParams.Filter.PeonID)
		}
	}

	var totalItems int64
	if err := query.Count(&totalItems).Error; err != nil {
		slog.Error("Error counting tasks", "err", err)
		return models.PaginatedResponse{}, fmt.Errorf("failed to count tasks: %w", err)
	}

	if queryParams.Order != nil {
		query = query.Order(fmt.Sprintf("%s %s", queryParams.Order.Field, queryParams.Order.Dir))
	}
	offset := (queryParams.Page - 1) * queryParams.PerPage
	query = query.Limit(queryParams.PerPage).Offset(offset)

	var tasks []models.Task
	if err := query.Find(&tasks).Error; err != nil {
		return models.PaginatedResponse{}, fmt.Errorf("failed to find tasks: %w", err)
	}

	if tasks == nil {
		tasks = []models.Task{}
	}

	totalPages := (int(totalItems) + queryParams.PerPage - 1) / queryParams.PerPage

	response := models.PaginatedResponse{
		Page:       queryParams.Page,
		PerPage:    queryParams.PerPage,
		TotalItems: int(totalItems),
		TotalPages: totalPages,
		Items:      tasks,
	}
	return response, nil
}

func UpdateTask(db *gorm.DB, taskID string, partialTask models.TaskUpdate) (models.Task, error) {
	tx := db.Begin()
	if tx.Error != nil {
		return models.Task{}, fmt.Errorf("failed to start transaction: %w", tx.Error)
	}

	updates := map[string]interface{}{}

	if partialTask.TaskNameSet {
		updates["task_name"] = partialTask.TaskName
	}
	if partialTask.PeonIDSet {
		updates["peon_id"] = partialTask.PeonID
	}
	if partialTask.StatusSet {
		updates["status"] = partialTask.Status
	}
	if partialTask.QueueSet {
		updates["queue"] = partialTask.Queue
	}
	if partialTask.RetryLimitSet {
		updates["retry_limit"] = partialTask.RetryLimit
	}
	if partialTask.PayloadSet {
		updates["payload"] = partialTask.Payload
	}
	if partialTask.ResultSet {
		updates["result"] = partialTask.Result
	}
	if partialTask.RetryOnFailureSet {
		updates["retry_on_failure"] = partialTask.RetryOnFailure
	}
	if partialTask.RetryCountSet {
		updates["retry_count"] = partialTask.RetryCount
	}

	if len(updates) == 0 {
		tx.Rollback()
		return models.Task{}, fmt.Errorf("no fields to update")
	}

	if partialTask.StatusSet && *partialTask.Status == "RUNNING" {
		// Check status BEFORE applying any updates
		var task models.Task
		if err := tx.First(&task, "id = ?", taskID).Error; err != nil {
			tx.Rollback()
			return models.Task{}, fmt.Errorf("failed to fetch task: %w", err)
		}
		if task.Status != "ACKNOWLEDGED" {
			tx.Rollback()
			return models.Task{}, errs.TaskWasNotAcknowledgedErr
		}
	}

	result := tx.Model(&models.Task{}).Where("id = ?", taskID).Updates(updates)
	if result.Error != nil {
		tx.Rollback()
		return models.Task{}, fmt.Errorf("failed to update task: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		tx.Rollback()
		return models.Task{}, fmt.Errorf("task not found")
	}

	if partialTask.StatusSet && *partialTask.Status == "PENDING" {
		resultQueue := tx.Model(&models.Queue{}).
			Where("task_id = ?", taskID).
			Update("sent_to_peon", false)
		if resultQueue.Error != nil {
			tx.Rollback()
			return models.Task{}, fmt.Errorf("failed to update queue: %w", resultQueue.Error)
		}

		result = tx.Model(&models.Task{}).Where("id = ?", taskID).Update("peon_id", nil)
		if result.Error != nil {
			tx.Rollback()
			return models.Task{}, fmt.Errorf("failed to update peon_id: %w", result.Error)
		}
	} else if partialTask.StatusSet && *partialTask.Status == "ACKNOWLEDGED" {
		if partialTask.PeonIDSet != true {
			tx.Rollback()
			return models.Task{}, errs.PeonIDRequiredForAckErr
		}
		resultQueue := tx.Model(&models.Queue{}).
			Where("task_id = ?", taskID).
			Update("sent_to_peon", true)
		if resultQueue.Error != nil {
			tx.Rollback()
			return models.Task{}, fmt.Errorf("failed to update queue: %w", resultQueue.Error)
		}
	}

	var updatedTask models.Task
	if err := tx.First(&updatedTask, "id = ?", taskID).Error; err != nil {
		tx.Rollback()
		return models.Task{}, fmt.Errorf("failed to fetch updated task: %w", err)
	}

	if err := tx.Commit().Error; err != nil {
		return models.Task{}, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return updatedTask, nil
}

func GetTaskFromQueueByTaskID(db *gorm.DB, taskID string) (models.Queue, error) {
	var queue models.Queue
	result := db.First(&queue, "task_id = ?", taskID)
	if result.Error != nil {
		return models.Queue{}, fmt.Errorf("failed to find task in queue: %w", result.Error)
	}
	return queue, nil
}

func UpdateSentToPeonQueueByTaskID(db *gorm.DB, taskID string, sentToPeon bool) error {
	result := db.Model(&models.Queue{}).Where("task_id = ?", taskID).Update("sent_to_peon", sentToPeon)
	if result.Error != nil {
		return fmt.Errorf("failed to update sent to peon queue: %w", result.Error)
	}
	return nil
}

func GetTasksByPeonID(db *gorm.DB, peonID string) ([]models.Task, error) {
	var tasks []models.Task
	result := db.Find(&tasks, "peon_id = ?", peonID)
	if result.Error == gorm.ErrRecordNotFound {
		return []models.Task{}, nil
	}
	if result.Error != nil {
		return []models.Task{}, fmt.Errorf("failed to find tasks by peon ID: %w", result.Error)
	}
	return tasks, nil
}

func GetAvailablePeon(db *gorm.DB, queue string, usedPeons map[string]bool) (models.Peon, error) {
	var peon models.Peon

	query := db.Where("status = ? AND queues LIKE ?", "IDLE", `%'`+queue+`'%`)
	for peonID := range usedPeons {
		if peonID != "" {
			query = query.Where("id != ?", peonID)
		}
	}
	result := query.First(&peon)

	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return models.Peon{}, fmt.Errorf("no available peon")
		}
		return models.Peon{}, fmt.Errorf("failed to find available peon: %w", result.Error)
	}
	return peon, nil
}

func GetAvailablePeons(db *gorm.DB, queue string) ([]models.Peon, error) {
	var peons []models.Peon
	result := db.Find(&peons, `status = ? AND queues LIKE ?`,
		"IDLE",
		`%'`+queue+`'%`)

	if result.Error != nil {
		return []models.Peon{}, fmt.Errorf("failed to find available peons: %w", result.Error)
	}
	return peons, nil
}

func CreateStats(db *gorm.DB, stats models.Stats) (models.Stats, error) {
	result := db.Create(&stats)
	if result.Error != nil {
		return models.Stats{}, fmt.Errorf("failed to create stats: %w", result.Error)
	}
	return stats, nil
}

// TODO: Write tests for this function
func GetNotYetSentOutTasks(db *gorm.DB) ([]models.Task, error) {
	var tasks []models.Task
	result := db.Where("status = ? AND id NOT IN (SELECT task_id FROM queues WHERE sent_to_peon = true)",
		models.TaskStatusPending).
		Find(&tasks)
	if result.Error != nil {
		return []models.Task{}, fmt.Errorf("failed to get pending tasks: %w", result.Error)
	}
	return tasks, nil
}

func UpdateQueueByTaskID(db *gorm.DB, taskID string, sentToPeon bool) (models.Queue, error) {
	var queue models.Queue
	result := db.First(&queue, "task_id = ?", taskID)
	if result.Error != nil {
		return models.Queue{}, fmt.Errorf("failed to find queue by task ID: %w", result.Error)
	}

	result = db.Model(&models.Queue{}).Where("task_id = ?", taskID).Update("sent_to_peon", sentToPeon)
	if result.Error != nil {
		return models.Queue{}, fmt.Errorf("failed to update queue by task ID: %w", result.Error)
	}

	return queue, nil
}
