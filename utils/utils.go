package utils

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"gorm.io/gorm"
)

// Helper function to parse the filter JSON
func ParsePeonFilter(filterJSON string) (*models.PeonFilter, error) {
	if filterJSON == "" {
		return nil, nil
	}

	var filter models.PeonFilter
	err := json.Unmarshal([]byte(filterJSON), &filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter JSON: %w", err)
	}

	// Validate the filter
	if err := validatePeonFilter(&filter); err != nil {
		return nil, err
	}

	return &filter, nil
}

// Validate the filter conditions
func validatePeonFilter(filter *models.PeonFilter) error {
	if filter.Status != nil {
		if _, ok := filter.Status.Value.(string); !ok {
			return fmt.Errorf("status value must be string")
		}
		// Add status validation if you have specific valid statuses for peons
	}

	if filter.LastHeartbeat != nil {
		// Verify that last_heartbeat is a valid timestamp
		switch v := filter.LastHeartbeat.Value.(type) {
		case string:
			_, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return fmt.Errorf("invalid last_heartbeat timestamp: %w", err)
			}
		default:
			return fmt.Errorf("last_heartbeat must be RFC3339 timestamp string")
		}
	}

	return nil
}

// Build SQL query with the filter
func BuildPeonQuery(filter *models.PeonFilter) (string, []interface{}, error) {
	query := "SELECT * FROM peon WHERE 1=1"
	var args []interface{}

	if filter == nil {
		return query, args, nil
	}

	if filter.Status != nil {
		query += " AND status = ?"
		args = append(args, filter.Status.Value)
	}

	if filter.LastHeartbeat != nil {
		var op string
		switch filter.LastHeartbeat.Op {
		case models.FilterOpGreater:
			op = ">"
		case models.FilterOpGreaterEq:
			op = ">="
		case models.FilterOpLess:
			op = "<"
		case models.FilterOpLessEq:
			op = "<="
		case models.FilterOpEquals:
			op = "="
		default:
			return "", nil, fmt.Errorf("unsupported operator for last_heartbeat: %s", filter.LastHeartbeat.Op)
		}
		query += fmt.Sprintf(" AND last_heartbeat %s ?", op)
		args = append(args, filter.LastHeartbeat.Value)
	}

	if filter.CurrentTask != nil {
		if filter.CurrentTask.Value == nil {
			query += " AND current_task IS NULL"
		} else {
			query += " AND current_task = ?"
			args = append(args, filter.CurrentTask.Value)
		}
	}

	if filter.Queues != nil {
		query += " AND queues = ?"
		args = append(args, filter.Queues.Value)
	}

	return query, args, nil
}

// Helper function to parse the filter JSON
func ParseTaskFilter(filterJSON string) (*models.TaskFilter, error) {
	if filterJSON == "" {
		return nil, nil
	}

	var filter models.TaskFilter
	err := json.Unmarshal([]byte(filterJSON), &filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter JSON: %w", err)
	}

	// Validate the filter
	if err := validateTaskFilter(&filter); err != nil {
		return nil, err
	}

	return &filter, nil
}

// Validate the filter conditions
func validateTaskFilter(filter *models.TaskFilter) error {
	if filter.Status != nil {
		if _, ok := filter.Status.Value.(string); !ok {
			return fmt.Errorf("status value must be string")
		}
		status := models.TaskStatus(filter.Status.Value.(string))
		if !isValidTaskStatus(status) {
			return fmt.Errorf("invalid task status: %s", status)
		}
	}

	if filter.CreatedAt != nil {
		// Verify that created_at is a valid timestamp
		switch v := filter.CreatedAt.Value.(type) {
		case string:
			_, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return fmt.Errorf("invalid created_at timestamp: %w", err)
			}
		default:
			return fmt.Errorf("created_at must be RFC3339 timestamp string")
		}
	}

	return nil
}

func isValidTaskStatus(status models.TaskStatus) bool {
	switch status {
	case models.TaskStatusPending, models.TaskStatusRunning, models.TaskStatusSuccess,
		models.TaskStatusFailure, models.TaskStatusInvalid, models.TaskStatusCancelled:
		return true
	}
	return false
}

// Build SQL query with the filter
func BuildTaskQuery(filter *models.TaskFilter) (string, []interface{}, error) {
	query := "SELECT * FROM bountyboard WHERE 1=1"
	var args []interface{}

	if filter == nil {
		return query, args, nil
	}

	if filter.Status != nil {
		query += " AND status = ?"
		args = append(args, filter.Status.Value)
	}

	if filter.CreatedAt != nil {
		var op string
		switch filter.CreatedAt.Op {
		case models.FilterOpGreater:
			op = ">"
		case models.FilterOpGreaterEq:
			op = ">="
		case models.FilterOpLess:
			op = "<"
		case models.FilterOpLessEq:
			op = "<="
		case models.FilterOpEquals:
			op = "="
		default:
			return "", nil, fmt.Errorf("unsupported operator for created_at: %s", filter.CreatedAt.Op)
		}
		query += fmt.Sprintf(" AND created_at %s ?", op)
		args = append(args, filter.CreatedAt.Value)
	}

	if filter.TaskName != nil {
		query += " AND task_name = ?"
		args = append(args, filter.TaskName.Value)
	}

	if filter.Queue != nil {
		query += " AND queue = ?"
		args = append(args, filter.Queue.Value)
	}

	if filter.PeonID != nil {
		query += " AND peon_id = ?"
		args = append(args, filter.PeonID.Value)
	}

	return query, args, nil
}
func ParseTaskQuery(queryJSON string) (*models.TaskQuery, error) {
	// Default query when no JSON is provided
	if queryJSON == "" {
		return &models.TaskQuery{
			QueryParams: models.QueryParams{
				Page:    0,
				PerPage: 30,
				Order: &models.OrderParam{
					Field: "created_at",
					Dir:   "DESC",
				},
			},
		}, nil
	}

	// Parse the provided JSON
	var query models.TaskQuery
	err := json.Unmarshal([]byte(queryJSON), &query)
	if err != nil {
		return nil, fmt.Errorf("invalid query JSON: %w", err)
	}

	// Set defaults and validate pagination
	if query.Page < 0 {
		query.Page = 0
	}
	if query.PerPage <= 0 {
		query.PerPage = 30
	}

	// Set default order if not provided
	if query.Order == nil {
		query.Order = &models.OrderParam{
			Field: "created_at",
			Dir:   "DESC",
		}
	}

	// Validate order direction
	query.Order.Dir = strings.ToUpper(query.Order.Dir)
	if query.Order.Dir != "ASC" && query.Order.Dir != "DESC" {
		return nil, fmt.Errorf("invalid order direction: must be ASC or DESC")
	}

	// Validate order field
	allowedOrderFields := map[string]bool{
		"created_at":  true,
		"updated_at":  true,
		"status":      true,
		"task_name":   true,
		"id":          true,
		"queue":       true,
		"retry_count": true,
		"retry_limit": true,
	}
	if !allowedOrderFields[query.Order.Field] {
		return nil, fmt.Errorf("invalid order field: %s", query.Order.Field)
	}

	// Validate the filter if present
	if query.Filter != nil {
		if err := validateTaskFilter(query.Filter); err != nil {
			return nil, err
		}
	}

	return &query, nil
}

func ParsePeonQuery(queryJSON string) (*models.PeonQuery, error) {
	if queryJSON == "" {
		return &models.PeonQuery{
			QueryParams: models.QueryParams{
				Page:    0,
				PerPage: 30,
				Order: &models.OrderParam{
					Field: "last_heartbeat",
					Dir:   "DESC",
				},
			},
		}, nil
	}

	var query models.PeonQuery
	err := json.Unmarshal([]byte(queryJSON), &query)
	if err != nil {
		return nil, fmt.Errorf("invalid query JSON: %w", err)
	}

	// Set defaults if not provided
	if query.Page < 0 {
		query.Page = 0
	}
	if query.PerPage <= 0 {
		query.PerPage = 30
	}
	if query.Order == nil {
		query.Order = &models.OrderParam{
			Field: "last_heartbeat",
			Dir:   "DESC",
		}
	}

	// Validate order
	query.Order.Dir = strings.ToUpper(query.Order.Dir)
	if query.Order.Dir != "ASC" && query.Order.Dir != "DESC" {
		return nil, fmt.Errorf("invalid order direction: must be ASC or DESC")
	}

	// Validate allowed order fields
	allowedOrderFields := map[string]bool{
		"last_heartbeat": true,
		"status":         true,
		"id":             true,
	}
	if !allowedOrderFields[query.Order.Field] {
		return nil, fmt.Errorf("invalid order field: %s", query.Order.Field)
	}

	// Validate the filter if present
	if query.Filter != nil {
		if err := validatePeonFilter(query.Filter); err != nil {
			return nil, err
		}
	}

	return &query, nil
}

func GenerateUUID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
func ApplyFilterCondition(query *gorm.DB, field string, condition *models.FilterCondition) *gorm.DB {
	if condition == nil || query == nil {
		return query
	}

	switch condition.Op {
	case models.FilterOpEquals:
		return query.Where(field+" = ?", condition.Value)
	case models.FilterOpGreater:
		return query.Where(field+" > ?", condition.Value)
	case models.FilterOpLess:
		return query.Where(field+" < ?", condition.Value)
	case models.FilterOpGreaterEq:
		return query.Where(field+" >= ?", condition.Value)
	case models.FilterOpLessEq:
		return query.Where(field+" <= ?", condition.Value)
	case models.FilterOpIn:
		if condition.Value == nil {
			return query
		}
		return query.Where(field+" IN (?)", condition.Value)
	case models.FilterOpNotIn:
		if condition.Value == nil {
			return query
		}
		return query.Where(field+" NOT IN (?)", condition.Value)
	default:
		slog.Warn("Unsupported filter operator", "operator", condition.Op)
		return query
	}
}

func CleanInconsistencies(db *gorm.DB) error {
	// Start a transaction
	tx := db.Begin()
	if tx.Error != nil {
		return fmt.Errorf("failed to start transaction: %w", tx.Error)
	}
	defer tx.Rollback()

	// Get all tasks that are in RUNNING state with offline peons
	var inconsistentTasks []models.Task
	if err := tx.Model(&models.Task{}).
		Where("status = ? AND peon_id IN (SELECT id FROM peons WHERE status = ?)",
			models.TaskStatusRunning, "OFFLINE").
		Select("id").
		Find(&inconsistentTasks).Error; err != nil {
		return fmt.Errorf("failed to find inconsistent tasks: %w", err)
	}

	// For each inconsistent task
	for _, task := range inconsistentTasks {
		// Update task status and clear peon_id
		if err := tx.Model(&models.Task{}).
			Where("id = ?", task.ID).
			Updates(map[string]interface{}{
				"status":  models.TaskStatusPending,
				"peon_id": nil,
			}).Error; err != nil {
			slog.Error("Failed to update task status", "taskID", task.ID, "err", err)
			continue
		}

		// Create queue entry for the task
		queue := models.Queue{
			TaskID:     task.ID,
			SentToPeon: true,
		}
		if err := tx.Create(&queue).Error; err != nil {
			return fmt.Errorf("failed to insert into queue: %w", err)
		}
	}

	// Clean offline peons (if needed)
	if err := tx.Where("status = ?", "OFFLINE").
		Delete(&models.Peon{}).Error; err != nil {
		return fmt.Errorf("failed to clean peons: %w", err)
	}

	// Commit the transaction
	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
