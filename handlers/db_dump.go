package handlers

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/database"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/views"
	"github.com/a-h/templ"
	"gorm.io/gorm"
)

func DumpDatabaseHandler(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(database.DBPath)
	if err != nil {
		http.Error(w, "Failed to read database", http.StatusInternalServerError)
		return
	}

	filename := fmt.Sprintf("workcraft-backup-%s.db", time.Now().Format("20060102150405"))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	w.Header().Set("Content-Type", "application/x-sqlite3")
	w.Write(data)
}

func CreateDumpDatabaseAsCSVHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filename := fmt.Sprintf("tasks-%s.csv", time.Now().Format("20060102150405"))
		var tasks []models.Task
		query := db.Model(&models.Task{})
		if err := query.Find(&tasks).Error; err != nil {
			tasks = []models.Task{}
		}

		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))

		writer := csv.NewWriter(w)
		writer.Comma = rune("\x1f"[0])

		defer writer.Flush()

		headers := []string{
			"id",
			"created_at",
			"updated_at",
			"deleted_at",
			"task_name",
			"status",
			"peon_id",
			"queue",
			"payload", // Added payload field
			"result",  // Added result field
			"retry_on_failure",
			"retry_count",
			"retry_limit",
			"logs",
		}
		if err := writer.Write(headers); err != nil {
			http.Error(w, "Error writing CSV headers", http.StatusInternalServerError)
			return
		}

		// Write data rows
		for _, task := range tasks {
			// Handle nullable fields
			deletedAt := ""
			if task.DeletedAt != nil {
				deletedAt = task.DeletedAt.Format(time.RFC3339)
			}

			peonID := ""
			if task.PeonID != nil {
				peonID = *task.PeonID
			}

			logs := ""
			if task.Logs != nil {
				logs = *task.Logs
			}

			// Format the row data
			row := []string{
				task.ID,
				task.CreatedAt.Format(time.RFC3339),
				task.UpdatedAt.Format(time.RFC3339),
				deletedAt,
				task.TaskName,
				string(task.Status),
				peonID,
				task.Queue,
				task.PayloadStr, // Include the raw payload string
				task.ResultStr,  // Include the raw result string
				strconv.FormatBool(task.RetryOnFailure),
				strconv.Itoa(task.RetryCount),
				strconv.Itoa(task.RetryLimit),
				logs,
			}
			if err := writer.Write(row); err != nil {
				http.Error(w, "Error writing CSV data", http.StatusInternalServerError)
				return
			}
		}
	}
}

func CreateImportCSVToDBHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "Expected multipart form data", http.StatusBadRequest)
			return
		}

		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "Error retrieving file from form", http.StatusBadRequest)
			return
		}
		defer file.Close()

		separator := r.FormValue("separator")
		if separator == "" {
			separator = ","
		}

		reader := csv.NewReader(file)
		reader.Comma = rune(separator[0])

		headers, err := reader.Read()
		if err != nil {
			http.Error(w, "Error reading CSV headers", http.StatusBadRequest)
			return
		}

		records, err := reader.ReadAll()
		if err != nil {
			http.Error(w, "Error reading CSV data", http.StatusBadRequest)
			return
		}

		type FailedRow struct {
			RowNumber int    `json:"row_number"`
			Error     string `json:"error"`
		}
		failedRows := make([]FailedRow, 0)
		successCount := 0

		// Create a header map for easier lookup
		headerMap := make(map[string]int)
		for i, h := range headers {
			headerMap[h] = i
		}

		for i, record := range records {
			task := models.Task{
				Payload: models.TaskPayload{
					TaskArgs:             make([]interface{}, 0),
					TaskKwargs:           make(map[string]interface{}),
					PrerunHandlerArgs:    make([]interface{}, 0),
					PrerunHandlerKwargs:  make(map[string]interface{}),
					PostrunHandlerArgs:   make([]interface{}, 0),
					PostrunHandlerKwargs: make(map[string]interface{}),
				},
			}

			// Map CSV fields to Task struct
			for field, idx := range headerMap {
				if idx >= len(record) {
					continue
				}
				value := record[idx]

				switch field {
				case "id":
					task.ID = value
				case "created_at":
					if t, err := time.Parse(time.RFC3339, value); err == nil {
						task.CreatedAt = t
					}
				case "updated_at":
					if t, err := time.Parse(time.RFC3339, value); err == nil {
						task.UpdatedAt = t
					}
				case "deleted_at":
					if value != "" && value != "null" {
						if t, err := time.Parse(time.RFC3339, value); err == nil {
							task.DeletedAt = &t
						}
					}
				case "task_name":
					task.TaskName = value
				case "status":
					task.Status = models.TaskStatus(value)
				case "peon_id":
					if value != "" && value != "null" {
						task.PeonID = &value
					}
				case "queue":
					task.Queue = value
				case "payload":
					// Store the raw payload string, it will be parsed by BeforeSave
					task.PayloadStr = value
					// Also try to parse it into the Payload struct
					var payload models.TaskPayload
					if err := json.Unmarshal([]byte(value), &payload); err == nil {
						task.Payload = payload
					}
				case "result":
					// Store the raw result string
					task.ResultStr = value
					// Also try to parse it
					var result interface{}
					if err := json.Unmarshal([]byte(value), &result); err == nil {
						task.Result = result
					}
				case "retry_on_failure":
					task.RetryOnFailure, _ = strconv.ParseBool(value)
				case "retry_count":
					task.RetryCount, _ = strconv.Atoi(value)
				case "retry_limit":
					task.RetryLimit, _ = strconv.Atoi(value)
				case "logs":
					if value != "" && value != "null" {
						task.Logs = &value
					}
				}
			}

			// Validate required fields
			if task.ID == "" || task.TaskName == "" || task.Queue == "" {
				failedRows = append(failedRows, FailedRow{
					RowNumber: i + 1,
					Error:     "Missing required fields (ID, TaskName, or Queue)",
				})
				continue
			}

			// Create the task in the database
			if err := db.Create(&task).Error; err != nil {
				failedRows = append(failedRows, FailedRow{
					RowNumber: i + 1,
					Error:     fmt.Sprintf("Database error: %v", err),
				})
				continue
			}

			successCount++
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"total_rows":    len(records),
			"success_count": successCount,
			"failed_rows":   failedRows,
		})
	}
}

func CreateDBDataViewHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		component := views.DBData()
		templ.Handler(component).ServeHTTP(w, r)
	}
}
