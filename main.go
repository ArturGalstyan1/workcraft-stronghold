package main

import (
	"log"
	"os"

	"github.com/Artur-Galstyan/workcraft-stronghold/database"
	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/stronghold"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
)

var apiKey string

func init() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
	}

	apiKey := os.Getenv("WORKCRAFT_API_KEY")
	if apiKey == "" {
		log.Fatal("WORKCRAFT_API_KEY not set in environment")
	}
}

func main() {
	database.InitDB()
	eventSender := events.NewEventSender()

	stronhold := stronghold.NewStronghold(apiKey, database.DB, eventSender)
	stronhold.Run()
}
