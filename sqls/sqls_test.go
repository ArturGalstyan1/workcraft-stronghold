package sqls_test

import (
	"testing"

	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/sqls"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func getDB() *gorm.DB {
	path := "file::memory:"
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	err = db.AutoMigrate(
		&models.Peon{},
		&models.Task{},
		&models.Stats{},
		&models.Queue{},
	)
	if err != nil {
		panic(err)
	}
	return db
}

func TestCreateAndPeon(t *testing.T) {
	db := getDB()

	p, err := sqls.CreatePeon(db, models.Peon{})
	if err != nil {
		t.Errorf("CreatePeon failed: %v", err)
	}

	if p.Status != "IDLE" {
		t.Errorf("Expected status to be IDLE, got %s", p.Status)
	}

	pFromDB, err := sqls.GetPeon(db, p.ID)
	if err != nil {
		t.Errorf("GetPeon failed: %v", err)
	}

	if pFromDB.ID != p.ID {
		t.Errorf("Expected ID to be %s, got %s", p.ID, pFromDB.ID)
	}

	if pFromDB.Status != p.Status {
		t.Errorf("Expected status to be %s, got %s", p.Status, pFromDB.Status)
	}

	if pFromDB.LastHeartbeat != p.LastHeartbeat {
		t.Errorf("Expected LastHeartbeat to be %s, got %s", p.LastHeartbeat, pFromDB.LastHeartbeat)
	}
}

func TestUpdatePeon(t *testing.T) {
	db := getDB()
	queues := "['DEFAULT']"
	p, err := sqls.CreatePeon(db, models.Peon{
		Queues: &queues,
	})
	if err != nil {
		t.Errorf("CreatePeon failed: %v", err)
	}

	status := "WORKING"
	updates := models.PeonUpdate{
		Status:    &status,
		StatusSet: true,
	}

	updatedPeon, err := sqls.UpdatePeon(db, p.ID, updates)
	if err != nil {
		t.Errorf("UpdatePeon failed: %v", err)
	}

	if updatedPeon.Status != status {
		t.Errorf("Expected status to be %s, got %s", status, updatedPeon.Status)
	}

	updatedPeonFromGet, err := sqls.GetPeon(db, p.ID)
	if err != nil {
		t.Errorf("GetPeon failed: %v", err)
	}

	if updatedPeonFromGet.Status != status {
		t.Errorf("Expected status to be %s, got %s", status, updatedPeonFromGet.Status)
	}

	if *updatedPeonFromGet.Queues != *p.Queues {
		t.Errorf("Expected queues to be %s, got %s", *p.Queues, *updatedPeonFromGet.Queues)
	}

	updates = models.PeonUpdate{
		Queues:    nil,
		QueuesSet: true,
	}

	updatedPeon, err = sqls.UpdatePeon(db, p.ID, updates)
	if err != nil {
		t.Errorf("UpdatePeon failed: %v", err)
	}

	if updatedPeon.Queues != nil {
		t.Errorf("Expected queues to be nil, got %s", *updatedPeon.Queues)
	}
}

func TestCreateTask(t *testing.T) {
	db := getDB()

	taskName := "test"
	queue := "DEFAULT"
	payload := models.TaskPayload{
		TaskArgs: []interface{}{
			"arg1", "arg2",
		},
	}
	task, err := sqls.CreateTask(db, models.Task{
		TaskName: taskName,
		Queue:    queue,
		Payload:  payload,
	})

	if err != nil {
		t.Errorf("CreateTask failed: %v", err)
	}

	if task.Status != models.TaskStatusPending {
		t.Errorf("Expected status to be %s, got %s", models.TaskStatusPending, task.Status)
	}

	q, err := sqls.GetTaskFromQueueByTaskID(db, task.ID)
	if err != nil {
		t.Errorf("GetTaskFromQueueByTaskID failed: %v", err)
	}

	if q.SentToPeon != false {
		t.Errorf("Expected SentToPeon to be false, got %v", q.SentToPeon)
	}

	taskFromGet, err := sqls.GetTask(db, task.ID)
	if err != nil {
		t.Errorf("GetTask failed: %v", err)
	}

	if taskFromGet.ID != task.ID {
		t.Errorf("Expected ID to be %s, got %s", task.ID, taskFromGet.ID)
	}

	if taskFromGet.Status != task.Status {
		t.Errorf("Expected status to be %s, got %s", task.Status, taskFromGet.Status)
	}

	if taskFromGet.Queue != task.Queue {
		t.Errorf("Expected queue to be %s, got %s", task.Queue, taskFromGet.Queue)
	}

	if taskFromGet.Payload.TaskArgs[0] != task.Payload.TaskArgs[0] {
		t.Errorf("Expected payload to be %v, got %v", task.Payload.TaskArgs[0], taskFromGet.Payload.TaskArgs[0])
	}

	if taskFromGet.Status != models.TaskStatusPending {
		t.Errorf("Expected status to be %s, got %s", models.TaskStatusPending, taskFromGet.Status)
	}
}

func TestDequeueEnqueueTask(t *testing.T) {
	db := getDB()

	taskName := "test"
	queue := "DEFAULT"
	payload := models.TaskPayload{
		TaskArgs: []interface{}{
			"arg1", "arg2",
		},
	}
	task, err := sqls.CreateTask(db, models.Task{
		TaskName: taskName,
		Queue:    queue,
		Payload:  payload,
	})

	if err != nil {
		t.Errorf("CreateTask failed: %v", err)
	}

	q, err := sqls.GetTaskFromQueueByTaskID(db, task.ID)

	if err != nil {
		t.Errorf("GetTaskFromQueueByTaskID failed: %v", err)
	}
	if q.SentToPeon != false {
		t.Errorf("Expected SentToPeon to be false, got %v", q.SentToPeon)
	}

	err = sqls.UpdateSentToPeonQueueByTaskID(db, task.ID, true)
	if err != nil {
		t.Errorf("UpdateSentToPeonQueueByTaskID failed: %v", err)
	}

	q, err = sqls.GetTaskFromQueueByTaskID(db, task.ID)
	if err != nil {
		t.Errorf("GetTaskFromQueueByTaskID failed: %v", err)
	}

	if q.SentToPeon != true {
		t.Errorf("Expected SentToPeon to be true, got %v", q.SentToPeon)
	}
}

func TestUpdateTask(t *testing.T) {
	db := getDB()

	taskName := "test"
	queue := "DEFAULT"
	payload := models.TaskPayload{
		TaskArgs: []interface{}{
			"arg1", "arg2",
		},
	}
	task, err := sqls.CreateTask(db, models.Task{
		TaskName: taskName,
		Queue:    queue,
		Payload:  payload,
	})

	if err != nil {
		t.Errorf("CreateTask failed: %v", err)
	}

	newTaskName := "newTest"
	updates := models.TaskUpdate{
		TaskName:    &newTaskName,
		TaskNameSet: true,
	}

	updatedTask, err := sqls.UpdateTask(db, task.ID, updates)
	if err != nil {
		t.Errorf("UpdateTask failed: %v", err)
	}

	if updatedTask.TaskName != newTaskName {
		t.Errorf("Expected task name to be %s, got %s", newTaskName, updatedTask.TaskName)
	}

	updatedTaskFromGet, err := sqls.GetTask(db, task.ID)
	if err != nil {
		t.Errorf("GetTask failed: %v", err)
	}

	if updatedTaskFromGet.TaskName != newTaskName {
		t.Errorf("Expected task name to be %s, got %s", newTaskName, updatedTaskFromGet.TaskName)
	}

	newTaskStatus := "PENDING"
	updates = models.TaskUpdate{
		Status:    &newTaskStatus,
		StatusSet: true,
	}

	updatedTask, err = sqls.UpdateTask(db, task.ID, updates)
	if err != nil {
		t.Errorf("UpdateTask failed: %v", err)
	}

	if updatedTask.Status != models.TaskStatusPending {
		t.Errorf("Expected status to be %s, got %s", models.TaskStatusPending, updatedTask.Status)
	}

	q, err := sqls.GetTaskFromQueueByTaskID(db, task.ID)
	if err != nil {
		t.Errorf("GetTaskFromQueueByTaskID failed: %v", err)
	}

	if q.SentToPeon != false {
		t.Errorf("Expected SentToPeon to be false, got %v", q.SentToPeon)
	}

	err = sqls.UpdateSentToPeonQueueByTaskID(db, task.ID, true)
	if err != nil {
		t.Errorf("UpdateSentToPeonQueueByTaskID failed: %v", err)
	}

	q, err = sqls.GetTaskFromQueueByTaskID(db, task.ID)
	if err != nil {
		t.Errorf("GetTaskFromQueueByTaskID failed: %v", err)
	}

	if q.SentToPeon != true {
		t.Errorf("Expected SentToPeon to be true, got %v", q.SentToPeon)
	}
}

func TestGetPeonsPagination(t *testing.T) {
	db := getDB()

	var createdIDs []string
	for i := 0; i < 10; i++ {
		p, err := sqls.CreatePeon(db, models.Peon{})
		if err != nil {
			t.Errorf("CreatePeon failed: %v", err)
		}
		createdIDs = append(createdIDs, p.ID)
	}

	var count int64
	if err := db.Model(&models.Peon{}).Count(&count).Error; err != nil {
		t.Fatalf("Failed to count peons in database: %v", err)
	}

	if count != 10 {
		t.Errorf("Expected number of peons to be 10, got %d", count)
	}

	peons, err := sqls.GetPeons(db, models.PeonQuery{})
	if err != nil {
		t.Fatalf("GetPeons failed: %v", err)
	}

	var directPeons []models.Peon
	if err := db.Find(&directPeons).Error; err != nil {
		t.Fatalf("Direct query failed: %v", err)
	}

	if peons.TotalItems != 10 {
		t.Errorf("Expected total items to be 10, got %d", peons.TotalItems)
	}

	peonItems, ok := peons.Items.([]models.Peon)
	if !ok {
		t.Fatalf("Expected Items to be []models.Peon, got %T", peons.Items)
	}

	if len(peonItems) == 0 {
		t.Fatal("Expected Items to contain peons, got empty slice")
	}

	for i := 0; i < 5; i++ {
		status := "WORKING"
		updates := models.PeonUpdate{
			Status:    &status,
			StatusSet: true,
		}
		_, err := sqls.UpdatePeon(db, peonItems[i].ID, updates)
		if err != nil {
			t.Errorf("UpdatePeon failed: %v", err)
		}
	}

	peons, err = sqls.GetPeons(db, models.PeonQuery{
		Filter: &models.PeonFilter{
			Status: &models.FilterCondition{
				Op:    models.FilterOpEquals,
				Value: "WORKING",
			},
		},
	})

	if err != nil {
		t.Fatalf("GetPeons failed: %v", err)
	}

	if peons.TotalItems != 5 {
		t.Errorf("Expected total items to be 5, got %d", peons.TotalItems)
	}

	peonItems, ok = peons.Items.([]models.Peon)
	if !ok {
		t.Fatalf("Expected Items to be []models.Peon, got %T", peons.Items)
	}
}

func TestGetTasksPagination(t *testing.T) {
	db := getDB()

	var createdIDs []string
	for i := 0; i < 10; i++ {
		task, err := sqls.CreateTask(db, models.Task{
			TaskName: "test",
			Queue:    "DEFAULT",
			Payload: models.TaskPayload{
				TaskArgs: []interface{}{
					"arg1", "arg2",
				},
			},
		})
		if err != nil {
			t.Errorf("CreateTask failed: %v", err)
		}
		createdIDs = append(createdIDs, task.ID)
	}

	var count int64
	if err := db.Model(&models.Task{}).Count(&count).Error; err != nil {
		t.Fatalf("Failed to count tasks in database: %v", err)
	}

	if count != 10 {
		t.Errorf("Expected number of tasks to be 10, got %d", count)
	}

	tasks, err := sqls.GetTasks(db, models.TaskQuery{})
	if err != nil {
		t.Fatalf("GetTasks failed: %v", err)
	}

	var directTasks []models.Task
	if err := db.Find(&directTasks).Error; err != nil {
		t.Fatalf("Direct query failed: %v", err)
	}

	if tasks.TotalItems != 10 {
		t.Errorf("Expected total items to be 10, got %d", tasks.TotalItems)
	}

	if tasks.TotalItems != int(count) {
		t.Errorf("Expected total items to be %d, got %d", count, tasks.TotalItems)
	}

	taskItems, ok := tasks.Items.([]models.Task)
	if !ok {
		t.Fatalf("Expected Items to be []models.Task, got %T", tasks.Items)
	}

	if len(taskItems) == 0 {
		t.Fatal("Expected Items to contain tasks, got empty slice")
	}

	for i := 0; i < 5; i++ {
		newStatus := "RUNNING"
		updates := models.TaskUpdate{
			Status:    &newStatus,
			StatusSet: true,
		}
		_, err := sqls.UpdateTask(db, taskItems[i].ID, updates)
		if err != nil {
			t.Errorf("UpdateTask failed: %v", err)
		}
	}

	tasks, err = sqls.GetTasks(db, models.TaskQuery{
		Filter: &models.TaskFilter{
			Status: &models.FilterCondition{
				Op:    models.FilterOpEquals,
				Value: "RUNNING",
			},
		},
	})

	if err != nil {
		t.Fatalf("GetTasks failed: %v", err)
	}

	if tasks.TotalItems != 5 {
		t.Errorf("Expected total items to be 5, got %d", tasks.TotalItems)
	}

}

func TestUpdatePeonWithTaskToOffline(t *testing.T) {
	db := getDB()
	task, err := sqls.CreateTask(db, models.Task{
		TaskName: "test",
		Queue:    "['DEFAULT']",
		Payload: models.TaskPayload{
			TaskArgs: []interface{}{
				"arg1", "arg2",
			},
		},
	})
	if err != nil {
		t.Errorf("CreateTask failed: %v", err)
	}

	peon, err := sqls.CreatePeon(db, models.Peon{
		Queues:      &task.Queue,
		CurrentTask: &task.ID,
		Status:      "WORKING",
	})

	if err != nil {
		t.Errorf("CreatePeon failed: %v", err)
	}

	if peon.Status != "WORKING" {
		t.Errorf("Expected status to be WORKING, got %s", peon.Status)
	}
	runningStatus := "RUNNING"
	updatedTask, err := sqls.UpdateTask(db, task.ID, models.TaskUpdate{
		Status:    &runningStatus,
		StatusSet: true,
		PeonID:    &peon.ID,
		PeonIDSet: true,
	})

	if err != nil {
		t.Errorf("UpdateTask failed: %v", err)
	}

	if updatedTask.Status != "RUNNING" {
		t.Errorf("Expected status to be RUNNING, got %s", updatedTask.Status)
	}

	if *updatedTask.PeonID != peon.ID {
		t.Errorf("Expected peon ID to be %s, got %s", peon.ID, *updatedTask.PeonID)
	}

	offlineStatus := "OFFLINE"
	updatedPeon, err := sqls.UpdatePeon(db, peon.ID, models.PeonUpdate{
		Status:    &offlineStatus,
		StatusSet: true,
	})

	if err != nil {
		t.Errorf("UpdatePeon failed: %v", err)
	}

	if updatedPeon.Status != "OFFLINE" {
		t.Errorf("Expected status to be OFFLINE, got %s", updatedPeon.Status)
	}

	updatedTask, err = sqls.GetTask(db, task.ID)
	if err != nil {
		t.Errorf("GetTask failed: %v", err)
	}

	if updatedTask.Status != "PENDING" {
		t.Errorf("Expected status to be PENDING, got %s", updatedTask.Status)
	}

	if updatedTask.PeonID != nil {
		t.Errorf("Expected peon ID to be nil, got %s", *updatedTask.PeonID)
	}

	updatedPeon, err = sqls.GetPeon(db, peon.ID)
	if err != nil {
		t.Errorf("GetPeon failed: %v", err)
	}

	if updatedPeon.CurrentTask != nil {
		t.Errorf("Expected current task to be nil, got %s", *updatedPeon.CurrentTask)
	}

	q, err := sqls.GetTaskFromQueueByTaskID(db, task.ID)
	if err != nil {
		t.Errorf("GetTaskFromQueueByTaskID failed: %v", err)
	}

	if q.SentToPeon != false {
		t.Errorf("Expected SentToPeon to be false, got %v", q.SentToPeon)
	}
}

func TestGetTasksByPeonID(t *testing.T) {
	db := getDB()
	p, err := sqls.CreatePeon(db, models.Peon{})

	if err != nil {
		t.Errorf("CreatePeon failed: %v", err)
	}

	task, err := sqls.CreateTask(db, models.Task{
		TaskName: "test",
		Queue:    "['DEFAULT']",
		Payload: models.TaskPayload{
			TaskArgs: []interface{}{
				"arg1", "arg2",
			},
		},
		PeonID: &p.ID,
	})

	if err != nil {
		t.Errorf("CreateTask failed: %v", err)
	}

	tasks, err := sqls.GetTasksByPeonID(db, p.ID)
	if err != nil {
		t.Errorf("GetTasksByPeonID failed: %v", err)
	}

	if len(tasks) != 1 {
		t.Errorf("Expected 1 task, got %d", len(tasks))
	}

	if tasks[0].ID != task.ID {
		t.Errorf("Expected task ID to be %s, got %s", task.ID, tasks[0].ID)
	}

	if tasks[0].PeonID == nil {
		t.Errorf("Expected task PeonID to be %s, got nil", p.ID)
	}

	if *tasks[0].PeonID != p.ID {
		t.Errorf("Expected task PeonID to be %s, got %s", p.ID, *tasks[0].PeonID)
	}
}

func TestGetIdlePeon(t *testing.T) {
	db := getDB()
	peonQueue := "['DEFAULT']"
	peon, err := sqls.CreatePeon(db, models.Peon{
		Status: "WORKING",
		Queues: &peonQueue,
	})

	if err != nil {
		t.Errorf("CreatePeon failed: %v", err)
	}

	p, err := sqls.GetAvailablePeon(db, "DEFAULT")
	if err == nil {
		t.Errorf("Expected GetAvailablePeon to return error, got nil")
	}

	peonStatus := "IDLE"
	updatedPeon, err := sqls.UpdatePeon(db, peon.ID, models.PeonUpdate{
		Status:    &peonStatus,
		StatusSet: true,
	})

	if err != nil {
		t.Errorf("UpdatePeon failed: %v", err)
	}

	p, err = sqls.GetAvailablePeon(db, "DEFAULT")

	if err != nil {
		t.Errorf("GetAvailablePeon failed: %v", err)
	}

	if p.ID == "" {
		t.Errorf("Expected peon ID to be not empty, got %s", p.ID)
	}

	if p.Status != "IDLE" {
		t.Errorf("Expected peon status to be IDLE, got %s", p.Status)
	}

	if p.ID != updatedPeon.ID {
		t.Errorf("Expected peon ID to be %s, got %s", updatedPeon.ID, p.ID)
	}

	peonQueue = "['DEFAULT', 'OTHER']"
	peon, err = sqls.CreatePeon(db, models.Peon{
		Status: "IDLE",
		Queues: &peonQueue,
	})

	if err != nil {
		t.Errorf("CreatePeon failed: %v", err)
	}

	p, err = sqls.GetAvailablePeon(db, "DEFAULT")

	if err != nil {
		t.Errorf("GetAvailablePeon failed: %v", err)
	}

	if p.ID == "" {
		t.Errorf("Expected peon ID to be not empty, got %s", p.ID)
	}

	if p.Status != "IDLE" {
		t.Errorf("Expected peon status to be IDLE, got %s", p.Status)
	}

	p, err = sqls.GetAvailablePeon(db, "OTHER")

	if err != nil {
		t.Errorf("GetAvailablePeon failed: %v", err)
	}

	if p.ID == "" {
		t.Errorf("Expected peon ID to be not empty, got %s", p.ID)
	}

	if p.Status != "IDLE" {
		t.Errorf("Expected peon status to be IDLE, got %s", p.Status)
	}

	p, err = sqls.GetAvailablePeon(db, "NON_EXISTENT")

	if err == nil {
		t.Errorf("Expected GetAvailablePeon to return error, got nil")
	}

	if p.ID != "" {
		t.Errorf("Expected peon ID to be empty, got %s", p.ID)
	}
}
