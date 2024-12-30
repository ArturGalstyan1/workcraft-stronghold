.PHONY: dev

dev:
	templ generate -watch & \
	npx tailwindcss -i ./static/css/input.css -o ./static/css/output.css --watch & \
	go run main.go

recreate_db:
	@rm workcraft.db
	@sqlite3 workcraft.db ".databases" ".quit"
	@echo "Database created"

clear_db:
	@sqlite3 ./workcraft.db "DELETE FROM peon; DELETE FROM bountyboard;"
	@echo "Database cleared"
