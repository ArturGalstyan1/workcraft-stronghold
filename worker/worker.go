package worker

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/logger"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
)

type MockWorker struct {
	ID            string
	CurrentTaskID *string
	Queues        []string
	Status        string
	LastHeartbeat string

	hashedAPIKey string
	stopChan     chan struct{}
	wg           sync.WaitGroup
	taskChan     chan models.Task
}

func NewMockWorker(id string, queues []string, apiKey string) *MockWorker {
	hasher := sha256.New()
	hasher.Write([]byte(apiKey))
	hashedAPIKey := hex.EncodeToString(hasher.Sum(nil))

	return &MockWorker{
		ID:            id,
		CurrentTaskID: nil,
		Queues:        queues,
		Status:        "IDLE",
		LastHeartbeat: time.Now().UTC().String(),
		stopChan:      make(chan struct{}),
		hashedAPIKey:  hashedAPIKey,
		taskChan:      make(chan models.Task, 100),
	}
}

func (w *MockWorker) Start(ctx context.Context) {
	w.wg.Add(3)

	go w.heartbeat(ctx)
	go w.sse()
	go w.processTasksLoop(ctx)
	w.SyncWithDB()
}

func (w *MockWorker) Stop() {
	close(w.stopChan)
	w.wg.Wait()
	close(w.taskChan)
}

func (w *MockWorker) heartbeat(ctx context.Context) {
	defer w.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopChan:
			return
		case <-ticker.C:
			w.LastHeartbeat = time.Now().UTC().String()
			w.SyncWithDB()
		}
	}
}

func (w *MockWorker) SyncWithDB() {
	queuesString := "['"
	for i, queue := range w.Queues {
		if i == len(w.Queues)-1 {
			queuesString += queue + "']"
		} else {
			queuesString += queue + "','"
		}
	}

	updateJSONString := fmt.Sprintf(`{
	    "current_task": "%v",
		"current_task_set": true,
	    "status": "%s",
		"status_set": true,
	    "last_heartbeat": "%s",
		"last_heartbeat_set": true,
	    "queues": "%s",
		"queues_set": true,
	    "id": "%s"
    }`,
		w.CurrentTaskID, // Using %v for pointer
		w.Status,
		w.LastHeartbeat,
		queuesString,
		w.ID)
	fmt.Println(updateJSONString)

	updateEndpoint := fmt.Sprintf("http://localhost:6112/api/peon/%s/update", w.ID)
	req, err := http.NewRequest("POST", updateEndpoint, strings.NewReader(updateJSONString))
	if err != nil {
		panic(err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("WORKCRAFT_API_KEY", w.hashedAPIKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}

	defer resp.Body.Close()
	fmt.Println("Synced with DB, got response:", resp.Status)
}

func (w *MockWorker) processTask(task models.Task) {
	logger.Log.Info("Processing task", "taskID", task.ID)
	w.Status = "WORKING"
	taskID := task.ID
	w.CurrentTaskID = &taskID
	w.SyncWithDB()

	logger.Log.Info("Task processing started", "taskID", task.ID)
	w.updateTaskStatus(task.ID, "RUNNING")

	time.Sleep(5 * time.Second)

	logger.Log.Info("Task processing finished", "taskID", task.ID)
	w.updateTaskStatus(task.ID, "SUCCESSFUL")

	logger.Log.Info("Task processing finished", "taskID", task.ID)
	w.Status = "IDLE"
	w.CurrentTaskID = nil
	w.SyncWithDB()
}

func (w *MockWorker) updateTaskStatus(taskID string, status string) {
	updateJSONString := fmt.Sprintf(`{
		"status": "%s",
		"status_set": true,
		"peon_id": "%s",
		"peon_id_set": true
	}`, status, w.ID)

	updateEndpoint := fmt.Sprintf("http://localhost:6112/api/task/%s/update", taskID)
	req, err := http.NewRequest("POST", updateEndpoint, strings.NewReader(updateJSONString))
	if err != nil {
		panic(err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("WORKCRAFT_API_KEY", w.hashedAPIKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	fmt.Printf("Task %s status updated to %s, got response: %s\n", taskID, status, resp.Status)
}

func (w *MockWorker) processTasksLoop(ctx context.Context) {
	defer w.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopChan:
			return
		case task := <-w.taskChan:
			w.processTask(task)
		}
	}
}

func (w *MockWorker) sse() {
	defer w.wg.Done()

	sseEndpoint := "http://localhost:6112/events?type=peon&peon_id=" + w.ID + "&queues=[" + strings.Join(w.Queues, ",") + "]"
	req, err := http.NewRequest("GET", sseEndpoint, nil)
	if err != nil {
		panic(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("WORKCRAFT_API_KEY", w.hashedAPIKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	// Create a channel for the read error
	errChan := make(chan error, 1)

	// Create a channel for the parsed data
	dataChan := make(chan string, 1)

	// Start a goroutine to read from the SSE stream
	go func() {
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				errChan <- err
				return
			}
			dataChan <- string(line)
		}
	}()

	// Main loop
	for {
		select {
		case <-w.stopChan:
			logger.Log.Info("SSE loop stopping", "worker_id", w.ID)
			return
		case err := <-errChan:
			logger.Log.Error("Error reading from SSE", "error", err)
			return
		case stringLine := <-dataChan:
			if stringLine != "" && stringLine != "\n" {
				splitted := strings.Split(stringLine, "data:")
				if len(splitted) < 2 {
					continue
				}
				dataJSON := splitted[1]

				parsedData := make(map[string]interface{})
				err = json.Unmarshal([]byte(dataJSON), &parsedData)
				if err != nil {
					logger.Log.Error("Error parsing JSON", "error", err)
					continue
				}

				if parsedData["type"] == "new_task" {
					taskDataJSON, err := json.Marshal(parsedData["data"])
					if err != nil {
						logger.Log.Error("Error marshaling task data", "error", err)
						continue
					}

					var task models.Task
					if err := json.Unmarshal(taskDataJSON, &task); err != nil {
						logger.Log.Error("Error unmarshaling task", "error", err)
						continue
					}

					w.updateTaskStatus(task.ID, "ACKNOWLEDGED")

					select {
					case w.taskChan <- task:
						logger.Log.Info("Task queued for processing", "task_id", task.ID)
					default:
						logger.Log.Warn("Task queue full, dropping task", "task_id", task.ID)
					}
				}
			}
		}
	}
}
