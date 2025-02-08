package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/sqls"
	"github.com/Artur-Galstyan/workcraft-stronghold/stronghold"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
	"github.com/Artur-Galstyan/workcraft-stronghold/worker"
)

func TestMockWorker(t *testing.T) {
	db, cleanUp := utils.GetDB()
	defer cleanUp()

	eventSender := events.NewEventSender()
	s := stronghold.NewStronghold("abcd", db, eventSender, models.WorkcraftConfig{TimeBeforeDeadPeon: 5 * time.Second})
	go s.Run()
	time.Sleep(1 * time.Second)

	worker := worker.NewMockWorker("1", []string{"queue1", "queue2"}, "abcd")
	if worker.ID != "1" {
		t.Fatal("ID should be 1")
	}

	ctx := context.Background()
	t.Log("Starting worker")
	go worker.Start(ctx)

	// Wait for worker to initialize
	time.Sleep(2 * time.Second)

	// Verify initial worker state
	p, err := sqls.GetPeon(db, "1")
	if err != nil {
		t.Fatal("Failed to get peon:", err)
	}
	if p.Status != "IDLE" {
		t.Fatal("Initial peon status should be IDLE, got:", p.Status)
	}

	// Create a test task
	task, err := sqls.CreateTask(db, models.Task{
		Queue:    "queue1",
		TaskName: "test",
	})
	if err != nil {
		t.Fatal("Failed to create task:", err)
	}

	// Verify task was created in queue
	q, err := sqls.GetTaskFromQueueByTaskID(db, task.ID)
	if err != nil {
		t.Fatal("Failed to get task from queue:", err)
	}
	if q.TaskID != task.ID {
		t.Fatal("Task ID mismatch")
	}

	// Wait for task processing
	time.Sleep(10 * time.Second)

	// Verify task final state
	processedTask, err := sqls.GetTask(db, task.ID)
	if err != nil {
		t.Fatal("Failed to get processed task:", err)
	}
	if processedTask.Status != "SUCCESSFUL" {
		t.Fatal("Expected task status to be SUCCESSFUL, got:", processedTask.Status)
	}

	// Verify worker final state
	latestPeon, err := sqls.GetPeon(db, "1")
	if err != nil {
		t.Fatal("Failed to get final peon state:", err)
	}
	if latestPeon.Status != "IDLE" {
		t.Fatal("Expected final peon status to be IDLE, got:", latestPeon.Status)
	}

	// Stop the worker
	worker.Stop()

	// Give some time for cleanup
	time.Sleep(1 * time.Second)
}
