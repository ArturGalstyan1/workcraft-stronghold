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

var (
	Version    = "dev"
	BuildDate  = "unknown"
	CommitHash = "unknown"
)

func init() {
	apiKey = os.Getenv("WORKCRAFT_API_KEY")
	chieftainUser := os.Getenv("WORKCRAFT_CHIEFTAIN_USER")
	chieftainPass := os.Getenv("WORKCRAFT_CHIEFTAIN_PASS")

	if apiKey == "" || chieftainUser == "" || chieftainPass == "" {
		if err := godotenv.Load(); err != nil {
			log.Println("No .env file found - checking environment variables")
		}

		apiKey = os.Getenv("WORKCRAFT_API_KEY")
		chieftainUser = os.Getenv("WORKCRAFT_CHIEFTAIN_USER")
		chieftainPass = os.Getenv("WORKCRAFT_CHIEFTAIN_PASS")

		if apiKey == "" {
			log.Fatal("WORKCRAFT_API_KEY not set in environment or .env file")
		}
		if chieftainUser == "" {
			log.Fatal("WORKCRAFT_CHIEFTAIN_USER not set in environment or .env file")
		}
		if chieftainPass == "" {
			log.Fatal("WORKCRAFT_CHIEFTAIN_PASS not set in environment or .env file")
		}
	}
}

func main() {
	log.Printf("Version: %s, Build Date: %s, Commit: %s\n", Version, BuildDate, CommitHash)

	database.InitDB()
	eventSender := events.NewEventSender()

	stronhold := stronghold.NewStronghold(apiKey, database.DB, eventSender)
	stronhold.Run()
}
