package main

import (
	"log"
	"log/slog"
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
		slog.Info("Loading environment variables from .env file, if present")
		if err := godotenv.Load(); err != nil {
			slog.Info("No .env file found - checking environment variables")
		}

		apiKey = os.Getenv("WORKCRAFT_API_KEY")
		chieftainUser = os.Getenv("WORKCRAFT_CHIEFTAIN_USER")
		chieftainPass = os.Getenv("WORKCRAFT_CHIEFTAIN_PASS")

		if apiKey == "" {
			slog.Error("WORKCRAFT_API_KEY not set in environment or .env file")
		}
		if chieftainUser == "" {
			slog.Error("WORKCRAFT_CHIEFTAIN_USER not set in environment or .env file")
		}
		if chieftainPass == "" {
			slog.Error("WORKCRAFT_CHIEFTAIN_PASS not set in environment or .env file")
		}

		if apiKey == "" || chieftainUser == "" || chieftainPass == "" {
			slog.Error("Missing required environment variables")
			os.Exit(1)
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
