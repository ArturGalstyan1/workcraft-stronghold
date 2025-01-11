package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/sqls"
	"github.com/Artur-Galstyan/workcraft-stronghold/stronghold"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
	"github.com/Artur-Galstyan/workcraft-stronghold/worker"
)

func TestMockWorker(t *testing.T) {
	db, cleanUp := utils.GetDB()
	defer cleanUp()

	var tables []string
	result := db.Raw("SELECT name FROM sqlite_master WHERE type='table';").Scan(&tables)
	if result.Error != nil {
		t.Fatal("Failed to query tables:", result.Error)
	}
	t.Log("Available tables:")
	for _, table := range tables {
		t.Log(table)
	}
	eventSender := events.NewEventSender()
	s := stronghold.NewStronghold("abcd", db, eventSender)
	go s.Run()
	time.Sleep(1 * time.Second)

	worker := worker.NewMockWorker("1", []string{"queue1", "queue2"})
	if worker.ID != "1" {
		t.Error("ID should be 1")
	}
	context := context.Background()
	t.Log("Starting worker")
	go worker.Start(context)
	time.Sleep(5 * time.Second)

	p, err := sqls.GetPeon(db, "1")
	if err != nil {
		t.Fatal("Failed to get peon")
	}

	if p.Status != "IDLE" {
		t.Fatal("Peon status should be IDLE")
	}

	if p.ID != "1" {
		t.Fatal("Peon ID should be 1")
	}

	worker.Stop()
}
