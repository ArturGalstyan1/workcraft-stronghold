package worker

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
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

func (w *MockWorker) Start(ctx context.Context) error {
	w.wg.Add(1)
	go w.heartbeat(ctx)
	go w.sse()
	return nil
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
	sseEndpoint := "http://localhost:6112/events?type=peon&peon_id=" + w.ID
	resp, err := http.Get(sseEndpoint)
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

		fmt.Println(string(line))
	}
}
