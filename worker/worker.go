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
	CurrentTaskID string
	Queues        []string
	Status        string
	LastHeartbeat string

	stopChan chan struct{}
	wg       sync.WaitGroup
}

func NewMockWorker(id string, queues []string) *MockWorker {
	return &MockWorker{
		ID:            id,
		Queues:        queues,
		Status:        "IDLE",
		LastHeartbeat: time.Now().UTC().String(),
		stopChan:      make(chan struct{}),
	}
}

func (w *MockWorker) Start(ctx context.Context) {
	w.wg.Add(1)
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
		}
	}
}

func (w *MockWorker) SyncWithDB() error {
	return nil
}

func (w *MockWorker) sse() {
	apiKey := "abcd"

	hasher := sha256.New()
	hasher.Write([]byte(apiKey))
	hashedAPIKey := hex.EncodeToString(hasher.Sum(nil))
	sseEndpoint := "http://localhost:6112/events?type=peon&peon_id=" + w.ID + "&queues=[" + strings.Join(w.Queues, ",") + "]"
	req, err := http.NewRequest("GET", sseEndpoint, nil)
	if err != nil {
		panic(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("WORKCRAFT_API_KEY", hashedAPIKey)

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
			fmt.Println(stringLine)

			splitted := strings.Split(stringLine, "data:")
			dataJSON := splitted[1]
			fmt.Println(dataJSON)
		}
	}
}
