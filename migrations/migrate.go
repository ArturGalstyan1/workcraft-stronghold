package migrations

import (
	"github.com/Artur-Galstyan/workcraft-stronghold/logger"
	"github.com/Artur-Galstyan/workcraft-stronghold/models"
	"gorm.io/gorm"
)

func Migrate(db *gorm.DB) error {
	// Migrate Peon model
	if err := db.AutoMigrate(&models.Peon{}); err != nil {
		return err
	}
	logger.Log.Info("Peon model migrated")

	// Migrate Task model
	if err := db.AutoMigrate(&models.Task{}); err != nil {
		return err
	}
	logger.Log.Info("Task model migrated")

	// Migrate Stats model
	if err := db.AutoMigrate(&models.Stats{}); err != nil {
		return err
	}
	logger.Log.Info("Stats model migrated")

	// Migrate Queue model
	if err := db.AutoMigrate(&models.Queue{}); err != nil {
		return err
	}
	logger.Log.Info("Queue model migrated")

	return nil
}
