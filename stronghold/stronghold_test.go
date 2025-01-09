package stronghold_test

import (
	"testing"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/stronghold"
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

func TestStronghold(t *testing.T) {
	db := getDB()
	es := events.NewEventSender()
	s := stronghold.NewStronghold("abcd", db, es)

	t.Log("Running the stronghold for 10 seconds")
	go s.Run()
	time.Sleep(10 * time.Second)

}
