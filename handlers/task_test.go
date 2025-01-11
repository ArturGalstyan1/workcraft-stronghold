package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Artur-Galstyan/workcraft-stronghold/errs"
	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/handlers"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/sqls"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
)

func TestGetTaskHandler(t *testing.T) {

	db, cleanUp := utils.GetDB()
	defer cleanUp()

	handler := handlers.CreateGetTaskHandler(db)

	task, err := sqls.CreateTask(db, models.Task{
		TaskName: "test",
	})

	if err != nil {
		t.Error(err)
	}

	if task.Status != "PENDING" {
		t.Error("Task status should be PENDING")
	}

	req := httptest.NewRequest("GET", "/api/task/", nil)
	req.SetPathValue("id", task.ID)

	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Error("Expected status OK")
	}

	var response models.Task
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Error(err)
	}

	if reflect.DeepEqual(response, task) {
		t.Error("Task should be equal")
	}
}

func TestCreateNewTask(t *testing.T) {

	db, cleanUp := utils.GetDB()
	defer cleanUp()

	handler := handlers.CreatePostTaskHandler(db)

	taskJSON := `{"task_name": "test", "payload": {"task_args": [1,2,3]}}}`

	req := httptest.NewRequest("POST", "/api/task", strings.NewReader(taskJSON))

	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Error("Expected status 201 but got", w.Code)
	}

	var response models.Task
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Error(err)
	}

	if response.TaskName != "test" {
		t.Error("Task name should be test")
	}

	if response.Status != "PENDING" {
		t.Error("Task status should be PENDING")
	}

	tasks, err := sqls.GetNotYetSentOutTasks(db)
	if err != nil {
		t.Error(err)
	}

	if len(tasks) != 1 {
		t.Error("Expected 1 task in queue")
	}

	if tasks[0].TaskName != "test" {
		t.Error("Task name should be test")
	}

	if tasks[0].Status != "PENDING" {
		t.Error("Task status should be PENDING")
	}

	q, err := sqls.GetTaskFromQueueByTaskID(db, response.ID)

	if err != nil {
		t.Error(err)
	}

	if q.TaskID != response.ID {
		t.Error("Task ID should be equal")
	}
}

func TestUpdateTask(t *testing.T) {

	db, cleanUp := utils.GetDB()
	defer cleanUp()

	eventSender := events.NewEventSender()
	handler := handlers.CreateTaskUpdateHandler(db, eventSender)

	task, err := sqls.CreateTask(db, models.Task{
		TaskName: "test",
		Payload: models.TaskPayload{
			TaskArgs: []interface{}{1, 2, 3},
		},
	})

	if err != nil {
		t.Fatal(err)
	}

	taskJSON := `{"status": "RUNNING"}`
	req := httptest.NewRequest("POST", "/api/task/{id}/update", strings.NewReader(taskJSON))
	req.SetPathValue("id", task.ID)

	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	expectedStatus, _ := errs.Get(errs.TaskWasNotAcknowledgedErr)

	if w.Code != expectedStatus {
		t.Fatal("Expected status", expectedStatus, "but got", w.Code)
	}
	task, err = sqls.GetTask(db, task.ID)
	if err != nil {
		t.Fatal(err)
	}

	if task.Status != "PENDING" {
		t.Fatal("Task status should be PENDING and not yet RUNNING")
	}

	taskJSON = `{"status": "ACKNOWLEDGED"}`

	req = httptest.NewRequest("POST", "/api/task/{id}/update", strings.NewReader(taskJSON))
	req.SetPathValue("id", task.ID)

	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	expectedStatus, _ = errs.Get(errs.PeonIDRequiredForAckErr)

	if w.Code != expectedStatus {
		t.Fatal("Expected status", expectedStatus, "but got", w.Code)
	}

	taskJSON = `{"status": "ACKNOWLEDGED", "peon_id": "test"}`
	req = httptest.NewRequest("POST", "/api/task/{id}/update", strings.NewReader(taskJSON))
	req.SetPathValue("id", task.ID)

	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatal("Expected status OK but got", w.Code)
	}

	task, err = sqls.GetTask(db, task.ID)
	if err != nil {
		t.Fatal(err)
	}

	if task.Status != "ACKNOWLEDGED" {
		t.Fatal("Task status should be ACKNOWLEDGED")
	}

	t.Logf("Task status after ACKNOWLEDGED update: %s", task.Status)

	taskJSON = `{"status": "RUNNING"}`
	req = httptest.NewRequest("POST", "/api/task/{id}/update", strings.NewReader(taskJSON))

	req.SetPathValue("id", task.ID)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatal("Expected status OK but got", w.Code)
	}

	var response models.Task
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}

	if response.Status != "RUNNING" {
		t.Fatal("Task status should be RUNNING")
	}

	if response.TaskName != "test" {
		t.Fatal("Task name should be test")
	}
	if response.Payload.TaskArgs[0].(float64) != 1 {
		t.Fatal("Task arg should be 1")
	}

	taskFromDB, err := sqls.GetTask(db, task.ID)
	if err != nil {
		t.Fatal(err)
	}

	if taskFromDB.Status != "RUNNING" {
		t.Fatal("Task status should be RUNNING")
	}
}
