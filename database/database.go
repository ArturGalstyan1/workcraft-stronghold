package database

import (
	"github.com/Artur-Galstyan/workcraft-stronghold/migrations"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB
var DBPath string

func InitDB() {
	DBPath = "workcraft.db"
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
