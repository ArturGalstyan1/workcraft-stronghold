package migrations

import (
	"log/slog"

	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"gorm.io/gorm"
)

func Migrate(db *gorm.DB) error {
	// Migrate Peon model
	if err := db.AutoMigrate(&models.Peon{}); err != nil {
		return err
	}
	slog.Info("Peon model migrated")

	// Migrate Task model
	if err := db.AutoMigrate(&models.Task{}); err != nil {
		return err
	}
	slog.Info("Task model migrated")

	// Migrate Stats model
	if err := db.AutoMigrate(&models.Stats{}); err != nil {
		return err
	}
	slog.Info("Stats model migrated")

	// Migrate Queue model
	if err := db.AutoMigrate(&models.Queue{}); err != nil {
		return err
	}
	slog.Info("Queue model migrated")

	return nil
}
