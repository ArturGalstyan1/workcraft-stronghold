package database

import (
	"embed"
	"os"
	"path/filepath"

	"github.com/Artur-Galstyan/workcraft-stronghold/migrations"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

//go:embed workcraft.db
var Embedded embed.FS
var DB *gorm.DB
var DBPath string

func InitDB() {
	// Extract embedded database to a temporary file
	dbBytes, err := Embedded.ReadFile("workcraft.db")
	if err != nil {
		panic(err)
	}

	tempDir, err := os.MkdirTemp("", "workcraft-*")
	if err != nil {
		panic(err)
	}

	DBPath = filepath.Join(tempDir, "workcraft.db")
	if err := os.WriteFile(DBPath, dbBytes, 0600); err != nil {
		panic(err)
	}

	dbName := DBPath + "?_journal_mode=WAL&_synchronous=NORMAL"
	config := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	}

	db, err := gorm.Open(sqlite.Open(dbName), config)
	if err != nil {
		panic(err)
	}

	// Run automigrations
	if err := migrations.Migrate(db); err != nil {
		panic(err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		panic(err)
	}

	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)
	DB = db
}
