package handlers

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Artur-Galstyan/workcraft-stronghold/database"
)

func DumpDatabaseHandler(w http.ResponseWriter, r *http.Request) {
    data, err := os.ReadFile(database.DBPath)
    if err != nil {
        http.Error(w, "Failed to read database", http.StatusInternalServerError)
        return
    }

    filename := fmt.Sprintf("workcraft-backup-%s.db", time.Now().Format("20060102150405"))
    w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
    w.Header().Set("Content-Type", "application/x-sqlite3")
    w.Write(data)
}
