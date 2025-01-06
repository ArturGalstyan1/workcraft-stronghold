package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Artur-Galstyan/workcraft-stronghold/handlers"
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

func TestGetPeonHandler(t *testing.T) {
	db := getDB()
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
