package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/database"
	"github.com/Artur-Galstyan/workcraft-stronghold/events"
	"github.com/Artur-Galstyan/workcraft-stronghold/logger"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"github.com/Artur-Galstyan/workcraft-stronghold/stronghold"
	"github.com/getsentry/sentry-go"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
)

var apiKey string

var (
	Version    = "dev"
	BuildDate  = "unknown"
	CommitHash = "unknown"
)

var workcraftConfig models.WorkcraftConfig

func init() {
	apiKey = os.Getenv("WORKCRAFT_API_KEY")
	chieftainUser := os.Getenv("WORKCRAFT_CHIEFTAIN_USER")
	chieftainPass := os.Getenv("WORKCRAFT_CHIEFTAIN_PASS")

	if apiKey == "" || chieftainUser == "" || chieftainPass == "" {
		logger.Log.Info("Loading environment variables from .env file, if present")
		if err := godotenv.Load(); err != nil {
			logger.Log.Info("No .env file found - checking environment variables")
		}

		apiKey = os.Getenv("WORKCRAFT_API_KEY")
		chieftainUser = os.Getenv("WORKCRAFT_CHIEFTAIN_USER")
		chieftainPass = os.Getenv("WORKCRAFT_CHIEFTAIN_PASS")

		if apiKey == "" {
			logger.Log.Error("WORKCRAFT_API_KEY not set in environment or .env file")
		}
		if chieftainUser == "" {
			logger.Log.Error("WORKCRAFT_CHIEFTAIN_USER not set in environment or .env file")
		}
		if chieftainPass == "" {
			logger.Log.Error("WORKCRAFT_CHIEFTAIN_PASS not set in environment or .env file")
		}

		if apiKey == "" || chieftainUser == "" || chieftainPass == "" {
			logger.Log.Error("Missing required environment variables")
			os.Exit(1)
		}
	}
	sentryDsn := os.Getenv("SENTRY_DSN")
	logger.Init(sentryDsn)
	defer sentry.Flush(2 * time.Second)

	timeBeforeDeadPeonString := os.Getenv("WORKCRAFT_TIME_BEFORE_DEAD_PEON")
	if timeBeforeDeadPeonString == "" {
		timeBeforeDeadPeonString = "5"
	}

	timeBeforeDeadPeon, err := strconv.Atoi(timeBeforeDeadPeonString)
	if err != nil {
		logger.Log.Error("Invalid value for WORKCRAFT_TIME_BEFORE_DEAD_PEON, must be an integer")
		os.Exit(1)
	}

	workcraftConfig = models.WorkcraftConfig{
		TimeBeforeDeadPeon: time.Duration(timeBeforeDeadPeon) * time.Second,
	}
}

func main() {
	log.Printf("Version: %s, Build Date: %s, Commit: %s\n", Version, BuildDate, CommitHash)

	database.InitDB()
	eventSender := events.NewEventSender()

	stronhold := stronghold.NewStronghold(apiKey, database.DB, eventSender, workcraftConfig)
	stronhold.Run()
}
