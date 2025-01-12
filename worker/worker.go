package worker

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
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
	}
}

func (w *MockWorker) Start(ctx context.Context) {
	w.wg.Add(1)
	w.SyncWithDB()
	go w.heartbeat(ctx)
	go w.sse()
}

func (w *MockWorker) Stop() {
	close(w.stopChan)
	w.wg.Wait()
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

func (w *MockWorker) sse() {
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

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			panic(err)
		}
		stringLine := string(line)
		if stringLine != "" && stringLine != "\n" {
			splitted := strings.Split(stringLine, "data:")
			dataJSON := splitted[1]
			fmt.Println(dataJSON)
		}
	}
}
