package database

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func InitDB() {
	dbName := "workcraft.db?_journal_mode=WAL&_synchronous=NORMAL"

	config := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent), // or logger.Error if you want error logs
	}

	db, err := gorm.Open(sqlite.Open(dbName), config)
	if err != nil {
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
