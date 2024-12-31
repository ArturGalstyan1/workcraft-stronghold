package sqls

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"

	"github.com/Artur-Galstyan/workcraft-stronghold/models"
)

func GetIdlePeons() string {
	return `
	SELECT id FROM peon
	WHERE status = 'IDLE'
	AND (
	queues LIKE '[%''' || ? || '''%]'
	OR queues IS NULL
	)
	ORDER BY last_heartbeat ASC
	LIMIT 1
	`
}

func GetAnyOnlinePeon() string {
	return `
	SELECT id FROM peon
	WHERE status != 'OFFLINE'
	AND (
	queues LIKE '[%''' || ? || '''%]'
	OR queues IS NULL
	)
	ORDER BY last_heartbeat ASC
	LIMIT 1
	`
}

func GetPendingTasks() string {
	return `
	SELECT id, task_name, queue, payload, retry_count, retry_limit, retry_on_failure
	FROM bountyboard
	WHERE status = 'PENDING'
	   OR (status = 'FAILURE'
	       AND retry_on_failure = TRUE
	       AND retry_count < retry_limit
	       AND updated_at < datetime('now', '-1 minutes'))
	ORDER BY created_at ASC
	LIMIT 10`
}

func MarkPeonAsOffline() string {
	return "UPDATE peon SET status = 'OFFLINE' WHERE id = ?"
}

func CleanPeons() string {
	return "UPDATE peon SET status = 'OFFLINE', current_task = NULL WHERE last_heartbeat < datetime('now', '-1 minutes')"
}

func CleanBountyboard() string {
	return `
    UPDATE bountyboard
    SET status = 'PENDING',
        peon_id = NULL
    WHERE status = 'RUNNING' OR status = 'ACKNOWLEDGED'
    AND peon_id IS NOT NULL
    AND (
        NOT EXISTS (
            SELECT 1 FROM peon
            WHERE peon.id = bountyboard.peon_id
            AND peon.status != 'OFFLINE'
        )
        OR
        EXISTS (
            SELECT 1 FROM peon
            WHERE peon.id = bountyboard.peon_id
            AND peon.status != 'OFFLINE'
            AND peon.current_task != bountyboard.id
        )
    )`
}

func GetCreatePeonTableSQL() string {
	return `CREATE TABLE IF NOT EXISTS peon (
		id VARCHAR(36) PRIMARY KEY,
		status TEXT DEFAULT 'IDLE' CHECK (status IN ('IDLE', 'PREPARING', 'WORKING', 'OFFLINE')),
		last_heartbeat TIMESTAMP DEFAULT (datetime ('now')),
		current_task CHAR(36),
		queues TEXT
	);`
}

func GetCreatePeonTriggerSQL() string {
	return `CREATE TRIGGER IF NOT EXISTS peon_last_heartbeat_update AFTER
	UPDATE ON peon WHEN NEW.status != OLD.status BEGIN
	UPDATE peon
	SET last_heartbeat = datetime ('now')
	WHERE id = NEW.id;
	END;`
}

func GetCreateBountyboardTableSQL() string {
	return `CREATE TABLE IF NOT EXISTS bountyboard (
	    id CHAR(36) PRIMARY KEY,
	    status TEXT NOT NULL DEFAULT 'PENDING' CHECK (
	        status IN (
	            'PENDING',
	            'RUNNING',
	            'SUCCESS',
	            'FAILURE',
	            'INVALID',
                'CANCELLED',
                'ACKNOWLEDGED'
	        )
	    ),
	    created_at TIMESTAMP DEFAULT (datetime ('now')),
	    updated_at TIMESTAMP DEFAULT (datetime ('now')),
	    task_name VARCHAR(255) NOT NULL,
	    peon_id VARCHAR(36),
	    queue VARCHAR(255) DEFAULT 'DEFAULT',
	    payload TEXT,
	    result TEXT,
	    retry_on_failure BOOLEAN DEFAULT FALSE,
	    retry_count INTEGER DEFAULT 0,
	    retry_limit INTEGER NOT NULL DEFAULT 0,
	    CONSTRAINT chk_retry_limit CHECK (retry_limit >= 0),
	    CONSTRAINT chk_retry_consistency CHECK (
	        (retry_on_failure = 0)
	        OR (
	            retry_on_failure = 1
	            AND retry_limit > 1
	        )
	    ),
	    FOREIGN KEY (peon_id) REFERENCES peon (id)
	)`
}

func GetCreateBountyboardTriggerSQL() string {
	return `CREATE TRIGGER IF NOT EXISTS bountyboard_updated_at AFTER
		UPDATE ON bountyboard BEGIN
		UPDATE bountyboard
		SET
		    updated_at = datetime ('now')
		WHERE
		    id = NEW.id;
		END`
}

func GetCreateStatsTableSQL() string {
	return `
	CREATE TABLE IF NOT EXISTS stats (
		id INTEGER PRIMARY KEY,
		type TEXT NOT NULL,
		value TEXT NOT NULL,
		task_id CHAR(36),
		peon_id CHAR(36),
		created_at TIMESTAMP DEFAULT (datetime ('now')),
		FOREIGN KEY (task_id) REFERENCES bountyboard (id),
		FOREIGN KEY (peon_id) REFERENCES peon (id)
	);
	`
}

func GetCreateQueueTableSQL() string {
	return `
	CREATE TABLE IF NOT EXISTS queue (
		id INTEGER PRIMARY KEY,
		created_at TIMESTAMP DEFAULT (datetime ('now')),
		task_id CHAR(36) NOT NULL,
		queued BOOLEAN DEFAULT FALSE,
		FOREIGN KEY (task_id) REFERENCES bountyboard (id)
	);
	`
}

func InsertIntoBountyboard() string {
	return `
		INSERT INTO bountyboard (id, status, task_name, queue, payload, retry_on_failure, retry_limit) VALUES (?, ?, ?, ?, ?, ?, ?)
	`
}

func UpdatePeonHeartbeat(db *sql.DB, peonId string) error {
	query := `UPDATE peon SET last_heartbeat = datetime('now') WHERE id = ?`
	_, err := db.Exec(query, peonId)
	return err
}

func SetupDatabase(db *sql.DB) error {
	queries := []string{
		GetCreatePeonTableSQL(),
		GetCreatePeonTriggerSQL(),
		GetCreateBountyboardTableSQL(),
		GetCreateBountyboardTriggerSQL(),
		GetCreateStatsTableSQL(),
		GetCreateQueueTableSQL(),
	}

	for _, q := range queries {
		_, err := db.Exec(q)
		if err != nil {
			return err
		}
	}

	_, err := db.Exec("UPDATE peon SET status = 'OFFLINE'")
	return err
}

func GetTasksByPeonID(db *sql.DB, peonID string) ([]models.Task, error) {
	rows, err := db.Query("SELECT * FROM bountyboard WHERE peon_id = ?", peonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []models.Task

	for rows.Next() {
		var task models.Task
		var payloadJSON string

		err := rows.Scan(&task.ID, &task.Status, &task.CreatedAt, &task.UpdatedAt, &task.TaskName, &task.PeonId, &task.Queue, &payloadJSON, &task.Result, &task.RetryOnFailure, &task.RetryCount, &task.RetryLimit)

		if err != nil {
			return nil, err
		}

		err = json.Unmarshal([]byte(payloadJSON), &task.Payload)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}

	if len(tasks) == 0 {
		tasks = []models.Task{}
	}

	return tasks, nil
}

func GetPeonByID(db *sql.DB, peonID string) (models.Peon, error) {
	var peon models.Peon
	err := db.QueryRow(
		"SELECT id, status, last_heartbeat, current_task, queues FROM peon WHERE id = ?",
		peonID,
	).Scan(
		&peon.ID,
		&peon.Status,
		&peon.LastHeartbeat,
		&peon.CurrentTask,
		&peon.Queues,
	)

	return peon, err
}

func InsertPeonIntoDb(db *sql.DB, peonID string, peonQueues string) error {
	var queues interface{} = nil
	if peonQueues != "NULL" {
		queues = peonQueues
	}
	query := "INSERT INTO peon (id, queues) VALUES (?, ?) ON CONFLICT (id) DO UPDATE SET status = 'IDLE', last_heartbeat = datetime('now'), current_task = NULL, queues = ?"
	_, err := db.Exec(query, peonID, queues, queues)
	return err
}

func UpdatePeon(db *sql.DB, peonID string, updates models.PeonUpdate) (models.Peon, error) {
	val := reflect.ValueOf(updates)
	typ := reflect.TypeOf(updates)

	var setClauses []string
	var args []interface{}

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		if !field.IsNil() { // Only include non-nil fields
			dbTag := typ.Field(i).Tag.Get("db")
			setClauses = append(setClauses, fmt.Sprintf("%s = ?", dbTag))
			args = append(args, field.Elem().Interface())
		}
	}

	if len(setClauses) == 0 {
		return models.Peon{}, nil
	}

	args = append(args, peonID)

	query := fmt.Sprintf(
		"UPDATE peon SET %s WHERE id = ?",
		strings.Join(setClauses, ", "),
	)

	result, err := db.Exec(query, args...)
	if err != nil {
		return models.Peon{}, fmt.Errorf("failed to update peon: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return models.Peon{}, fmt.Errorf("error checking rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return models.Peon{}, fmt.Errorf("peon not found: %s", peonID)
	}

	return GetPeonByID(db, peonID)
}

func CreateTask(db *sql.DB, task models.Task) error {
	payloadJSON, err := json.Marshal(task.Payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Validate required fields
	if task.ID == "" {
		return fmt.Errorf("task ID is required")
	}
	if task.TaskName == "" {
		return fmt.Errorf("task name is required")
	}

	// Use default values if not provided
	if task.Status == "" {
		task.Status = "PENDING"
	}
	if task.Queue == "" {
		task.Queue = "DEFAULT"
	}
	if task.RetryLimit < 0 {
		return fmt.Errorf("retry limit cannot be negative")
	}

	query := `
        INSERT INTO bountyboard (
            id,
            status,
            task_name,
            queue,
            payload,
            retry_on_failure,
            retry_limit
        ) VALUES (?, ?, ?, ?, ?, ?, ?)`

	result, err := db.Exec(query,
		task.ID,
		task.Status,
		task.TaskName,
		task.Queue,
		string(payloadJSON),
		task.RetryOnFailure,
		task.RetryLimit,
	)
	if err != nil {
		return fmt.Errorf("failed to create task: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("error checking rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("no rows affected when creating task")
	}

	_, err = db.Exec("INSERT INTO queue (task_id) VALUES (?)", task.ID)
	if err != nil {
		return fmt.Errorf("failed to insert into queue: %w", err)
	}

	return nil
}

func UpdateTask(db *sql.DB, taskID string, updates models.TaskUpdate) (models.Task, error) {
	val := reflect.ValueOf(updates)
	typ := reflect.TypeOf(updates)

	var setClauses []string
	var args []interface{}

	validStatuses := map[string]bool{
		"PENDING":   true,
		"RUNNING":   true,
		"SUCCESS":   true,
		"FAILURE":   true,
		"INVALID":   true,
		"CANCELLED": true,
	}

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		if !field.IsNil() {
			dbTag := typ.Field(i).Tag.Get("db")

			// Special handling for payload which needs JSON marshaling
			if dbTag == "payload" {
				payload := field.Elem().Interface()
				payloadJSON, err := json.Marshal(payload)
				if err != nil {
					return models.Task{}, fmt.Errorf("failed to marshal payload: %w", err)
				}
				setClauses = append(setClauses, fmt.Sprintf("%s = ?", dbTag))
				args = append(args, string(payloadJSON))
				continue
			}

			// Validate status if it's being updated
			if dbTag == "status" {
				status := field.Elem().Interface().(string)
				if !validStatuses[status] {
					return models.Task{}, fmt.Errorf("invalid status: %s", status)
				}
			}

			// Validate retry_limit if it's being updated
			if dbTag == "retry_limit" {
				retryLimit := field.Elem().Interface().(int)
				if retryLimit < 0 {
					return models.Task{}, fmt.Errorf("retry limit cannot be negative")
				}
			}

			setClauses = append(setClauses, fmt.Sprintf("%s = ?", dbTag))
			args = append(args, field.Elem().Interface())
		}
	}

	if len(setClauses) == 0 {
		return models.Task{}, nil
	}

	args = append(args, taskID)

	query := fmt.Sprintf(
		"UPDATE bountyboard SET %s WHERE id = ?",
		strings.Join(setClauses, ", "),
	)

	result, err := db.Exec(query, args...)
	if err != nil {
		return models.Task{}, fmt.Errorf("failed to update task: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return models.Task{}, fmt.Errorf("error checking rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return models.Task{}, fmt.Errorf("task not found: %s", taskID)
	}

	return GetTaskByID(db, taskID)
}

func GetTaskByID(db *sql.DB, taskID string) (models.Task, error) {
	var task models.Task
	var payloadJSON string
	err := db.QueryRow(
		"SELECT * FROM bountyboard WHERE id = ?",
		taskID,
	).Scan(
		&task.ID, &task.Status, &task.CreatedAt, &task.UpdatedAt, &task.TaskName, &task.PeonId, &task.Queue, &payloadJSON, &task.Result, &task.RetryOnFailure, &task.RetryCount, &task.RetryLimit,
	)

	if err != nil {
		return models.Task{}, err
	}

	err = json.Unmarshal([]byte(payloadJSON), &task.Payload)
	if err != nil {
		return models.Task{}, err
	}

	return task, nil
}

func CreateStats(db *sql.DB, stats models.Stats) error {
	valueJSON, err := json.Marshal(stats.Value)
	if err != nil {
		return fmt.Errorf("failed to serialize stats value: %w", err)
	}

	query := `INSERT INTO stats (type, value, peon_id, task_id) VALUES (?, ?, ?, ?)`
	_, err = db.Exec(query, stats.Type, valueJSON, stats.PeonID, stats.TaskID)
	if err != nil {
		return fmt.Errorf("failed to insert stats: %w", err)
	}

	return nil
}

func DeleteTaskFromQueue(db *sql.DB, taskID string) error {
	_, err := db.Exec("DELETE FROM queue WHERE task_id = ?", taskID)
	return err
}

func UpdateQueue(db *sql.DB, taskID string) error {
	_, err := db.Exec("UPDATE queue SET queued = TRUE WHERE task_id = ?", taskID)
	return err
}

func GetTaskFromQueue(db *sql.DB) (models.Task, error) {
	var taskID string
	err := db.QueryRow("SELECT task_id FROM queue WHERE queued = FALSE ORDER BY created_at ASC LIMIT 1").Scan(&taskID)
	if err != nil {
		return models.Task{}, err
	}
	return GetTaskByID(db, taskID)
}

func GetPeonForTask(db *sql.DB, queue string) (models.Peon, error) {
	var peonID string
	err := db.QueryRow(GetIdlePeons(), queue).Scan(&peonID)
	if err != nil {
		return models.Peon{}, err
	}

	return GetPeonByID(db, peonID)
}

func CleanInconsistencies(db *sql.DB) error {
	_, err := db.Exec(CleanPeons())
	if err != nil {
		return fmt.Errorf("failed to clean peons: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}

	defer tx.Rollback()
	rows, err := tx.Query(CleanBountyboard())
	if err == sql.ErrNoRows {
		return nil
	} else if err != nil {
		slog.Error("Failed to find inconsistent tasks", "err", err)
	}

	defer rows.Close()
	for rows.Next() {
		var taskID string
		err := rows.Scan(&taskID)
		if err != nil {
			slog.Error("Failed to extract ID into string", "err", err)
		}
		updateQuery := `UPDATE bountyboard SET status = 'PENDING', peon_id = NULL WHERE id = ?`
		_, err = tx.Exec(updateQuery, taskID)
		if err != nil {
			slog.Error("Failed to update taskID back to PENDING after worker went offline: ", "err", err)
		}

		_, err = db.Exec("INSERT INTO queue (task_id) VALUES (?)", taskID)
		if err != nil {
			return fmt.Errorf("failed to insert into queue: %w", err)
		}
	}
	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
