package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/worker"
)

func TestMockWorker(t *testing.T) {
	worker := worker.NewMockWorker("1", []string{"queue1", "queue2"})
	if worker.ID != "1" {
		t.Error("ID should be 1")
	}

	context := context.Background()

	t.Log("Starting worker for 10 seconds")

	worker.Start(context)

	time.Sleep(10 * time.Second)

	worker.Stop()
}
