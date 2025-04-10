package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/handlers"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/sqls"
	"github.com/Artur-Galstyan/workcraft-stronghold/utils"
)

func TestGetPeonHandler(t *testing.T) {
	db, cleanUp := utils.GetDB()
	defer cleanUp()
	getPeonHandler := handlers.CreateGetPeonHandler(db)

	q := "['DEFAULT']"
	p, err := sqls.CreatePeon(db, models.Peon{
		Queues: &q,
	})
	if err != nil {
		t.Fatalf("Error creating peon: %v", err)
	}
	req := httptest.NewRequest("GET", "/api/peon/", nil)
	req.SetPathValue("id", p.ID)

	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	getPeonHandler.ServeHTTP(w, req)

	if status := w.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	requestJSON := w.Body.String()
	requestJSONBytes := []byte(requestJSON)
	var peon models.Peon
	err = json.Unmarshal(requestJSONBytes, &peon)
	if err != nil {
		t.Fatalf("Error unmarshalling response: %v", err)
	}

	if peon.ID != p.ID {
		t.Errorf("handler returned unexpected body: got %v want %v",
			peon.ID, p.ID)
	}

	if peon.Queues == nil {
		t.Errorf("handler returned unexpected body: got %v want %v",
			peon.Queues, q)
	}

	if *peon.Queues != q {
		t.Errorf("handler returned unexpected body: got %v want %v",
			*peon.Queues, q)
	}
}

func TestUpdatePeonHandler(t *testing.T) {

	db, cleanUp := utils.GetDB()
	defer cleanUp()

	eventSender := events.NewEventSender()
	updatePeonHandler := handlers.CreateUpdatePeonHandler(db, eventSender)

	q := "['DEFAULT']"
	p, err := sqls.CreatePeon(db, models.Peon{
		Queues: &q,
	})
	if err != nil {
		t.Fatalf("Error creating peon: %v", err)
	}

	task, err := sqls.CreateTask(db, models.Task{
		TaskName: "test",
	})
	if err != nil {
		t.Fatalf("Error creating task: %v", err)
	}

	acknowledgedStatus := "ACKNOWLEDGED"
	task, err = sqls.UpdateTask(db, task.ID, models.TaskUpdate{
		Status:    &acknowledgedStatus,
		StatusSet: true,
		PeonID:    &p.ID,
		PeonIDSet: true,
	})
	if err != nil {
		t.Fatalf("Error updating task: %v", err)
	}

	reqBody := map[string]interface{}{
		"status":       "WORKING",
		"current_task": task.ID,
	}
	reqBodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("Error marshalling request body: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/peon/", bytes.NewBuffer(reqBodyBytes))
	req.SetPathValue("id", p.ID)

	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	updatePeonHandler.ServeHTTP(w, req)

	if status := w.Code; status != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	peon, err := sqls.GetPeon(db, p.ID)
	if err != nil {
		t.Fatalf("Error querying peon: %v", err)
	}

	if peon.Status != "WORKING" {
		t.Errorf("handler returned unexpected body: got %v want %v",
			peon.Status, "WORKING")
	}

	if *peon.CurrentTask != task.ID {
		t.Errorf("handler returned unexpected body: got %v want %v",
			peon.CurrentTask, task.ID)
	}

	runningStatus := "RUNNING"
	_, err = sqls.UpdateTask(db, task.ID, models.TaskUpdate{
		Status:    &runningStatus,
		StatusSet: true,
	})
	if err != nil {
		t.Fatalf("Error updating task: %v", err)
	}

	task, err = sqls.GetTask(db, task.ID)

	if err != nil {
		t.Fatalf("Error querying task: %v", err)
	}

	if task.Status != "RUNNING" {
		t.Errorf("handler returned unexpected body: got %v want %v",
			task.Status, "RUNNING")
	}

	if *task.PeonID != p.ID {
		t.Errorf("handler returned unexpected body: got %v want %v",
			task.PeonID, p.ID)
	}

	reqBody = map[string]interface{}{
		"status": "OFFLINE",
	}

	reqBodyBytes, err = json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("Error marshalling request body: %v", err)
	}

	req = httptest.NewRequest("POST", "/api/peon/", bytes.NewBuffer(reqBodyBytes))
	req.SetPathValue("id", p.ID)

	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	updatePeonHandler.ServeHTTP(w, req)

	if status := w.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	peon, err = sqls.GetPeon(db, p.ID)
	if err != nil {
		t.Fatalf("Error querying peon: %v", err)
	}

	if peon.Status != "OFFLINE" {
		t.Errorf("handler returned unexpected body: got %v want %v",
			peon.Status, "OFFLINE")
	}

	if peon.CurrentTask != nil {
		t.Errorf("handler returned unexpected body: got %v want %v",
			peon.CurrentTask, nil)
	}

	task, err = sqls.GetTask(db, task.ID)

	if err != nil {
		t.Fatalf("Error querying task: %v", err)
	}

	if task.Status != "PENDING" {
		t.Errorf("handler returned unexpected body: got %v want %v",
			task.Status, "PENDING")
	}

	if task.PeonID != nil {
		t.Errorf("handler returned unexpected body: got %v want %v",
			task.PeonID, nil)
	}
}

func TestGetPeonsHandler(t *testing.T) {

	db, cleanUp := utils.GetDB()
	defer cleanUp()

	handler := handlers.CreateGetPeonsHandler(db)

	var taskIDs []string

	for i := 0; i < 5; i++ {
		task, err := sqls.CreateTask(db, models.Task{
			TaskName: "test",
		})
		if err != nil {
			t.Fatalf("Error creating task: %v", err)
		}
		taskIDs = append(taskIDs, task.ID)
	}

	var peonIDs []string
	for i := 0; i < 10; i++ {
		q := "['DEFAULT']"
		p, err := sqls.CreatePeon(db, models.Peon{
			Queues: &q,
		})
		if err != nil {
			t.Fatalf("Error creating peon: %v", err)
		}
		peonIDs = append(peonIDs, p.ID)
	}

	for i := 0; i < 5; i++ {
		workingStatus := "WORKING"
		_, err := sqls.UpdatePeon(db, peonIDs[i], models.PeonUpdate{
			Status:         &workingStatus,
			StatusSet:      true,
			CurrentTask:    &taskIDs[i],
			CurrentTaskSet: true,
		})
		if err != nil {
			t.Fatalf("Error updating peon: %v", err)
		}
	}

	workingStatus := "WORKING"
	filter := models.PeonFilter{
		Status: &models.FilterCondition{
			Op:    models.FilterOpEquals,
			Value: &workingStatus,
		},
	}
	q := models.PeonQuery{
		QueryParams: models.QueryParams{},
		Filter:      &filter,
	}

	qBytes, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("Error marshalling query: %v", err)
	}

	path := "/api/peons/?query=" + string(qBytes)
	req := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if status := w.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	requestJSON := w.Body.String()
	requestJSONBytes := []byte(requestJSON)

	var response models.PaginatedResponse
	err = json.Unmarshal(requestJSONBytes, &response)
	if err != nil {
		t.Fatalf("Error unmarshalling response: %v", err)
	}

	if response.TotalItems != 5 {
		t.Errorf("handler returned unexpected body: got %v want %v",
			response.TotalItems, 5)
	}

	idleStatus := "IDLE"
	q = models.PeonQuery{
		QueryParams: models.QueryParams{},
		Filter: &models.PeonFilter{
			Status: &models.FilterCondition{
				Op:    models.FilterOpEquals,
				Value: &idleStatus,
			},
		},
	}

	qBytes, err = json.Marshal(q)

	if err != nil {
		t.Fatalf("Error marshalling query: %v", err)
	}

	path = "/api/peons/?query=" + string(qBytes)
	req = httptest.NewRequest("GET", path, nil)
	w = httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if status := w.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	requestJSON = w.Body.String()
	requestJSONBytes = []byte(requestJSON)

	err = json.Unmarshal(requestJSONBytes, &response)
	if err != nil {
		t.Fatalf("Error unmarshalling response: %v", err)
	}

	if response.TotalItems != 5 {
		t.Errorf("handler returned unexpected body: got %v want %v",
			response.TotalItems, 5)
	}

	offlineStatus := "OFFLINE"
	q = models.PeonQuery{
		QueryParams: models.QueryParams{},
		Filter: &models.PeonFilter{
			Status: &models.FilterCondition{
				Op:    models.FilterOpEquals,
				Value: &offlineStatus,
			},
		},
	}

	qBytes, err = json.Marshal(q)
	if err != nil {
		t.Fatalf("Error marshalling query: %v", err)
	}

	path = "/api/peons/?query=" + string(qBytes)
	req = httptest.NewRequest("GET", path, nil)
	w = httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if status := w.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	requestJSON = w.Body.String()
	requestJSONBytes = []byte(requestJSON)

	err = json.Unmarshal(requestJSONBytes, &response)
	if err != nil {
		t.Fatalf("Error unmarshalling response: %v", err)
	}

	if response.TotalItems != 0 {
		t.Errorf("handler returned unexpected body: got %v want %v",
			response.TotalItems, 0)
	}
}

func TestGetPeonTaskHandler(t *testing.T) {

	db, cleanUp := utils.GetDB()
	defer cleanUp()

	handler := handlers.CreateGetPeonTaskHandler(db)

	p, err := sqls.CreatePeon(db, models.Peon{})
	if err != nil {
		t.Fatalf("Error creating peon: %v", err)
	}

	for i := 0; i < 5; i++ {
		_, err := sqls.CreateTask(db, models.Task{
			TaskName: "test",
			PeonID:   &p.ID,
		})
		if err != nil {
			t.Fatalf("Error creating task: %v", err)
		}
	}

	req := httptest.NewRequest("GET", "/api/peon/{id}/tasks", nil)
	req.SetPathValue("id", p.ID)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if status := w.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	requestJSON := w.Body.String()
	requestJSONBytes := []byte(requestJSON)

	var response []models.Task
	err = json.Unmarshal(requestJSONBytes, &response)
	if err != nil {
		t.Fatalf("Error unmarshalling response: %v", err)
	}

	if len(response) != 5 {
		t.Errorf("handler returned unexpected body: got %v want %v",
			len(response), 5)
	}

}

func TestPostStatisticsHandler(t *testing.T) {

	db, cleanUp := utils.GetDB()
	defer cleanUp()

	handler := handlers.CreatePostStatisticsHandler(db)

	stats := map[string]interface{}{
		"type": "cpu_usage",
		"value": map[string]interface{}{
			"percentage": 75.5,
			"cores_used": 6,
		},
		"peon_id": "330617ac-c981-472f-ac3c-c428b6eea42e",
		"task_id": "550e8400-e29b-41d4-a716-446655440001",
	}

	reqBody, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("Error marshalling request body: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/peon/{id}/statistics", bytes.NewBuffer(reqBody))
	req.SetPathValue("id", "330617ac-c981-472f-ac3c-c428b6eea42e")

	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if status := w.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

}
