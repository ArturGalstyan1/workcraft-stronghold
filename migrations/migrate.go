package main

import (
	"log/slog"

	"github.com/Artur-Galstyan/workcraft-stronghold/database"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
)

func main() {
	database.InitDB()
	var err error
	err = database.DB.AutoMigrate(&models.Peon{})
	if err != nil {
		panic(err)
	}
	slog.Info("Peon model migrated")

	err = database.DB.AutoMigrate(&models.Task{})
	if err != nil {
		panic(err)
	}
	slog.Info("Task model migrated")

	err = database.DB.AutoMigrate(&models.Stats{})
	if err != nil {
		panic(err)
	}
	slog.Info("Stats model migrated")

	err = database.DB.AutoMigrate(&models.Queue{})
	if err != nil {
		panic(err)
	}
	slog.Info("Queue model migrated")

}
