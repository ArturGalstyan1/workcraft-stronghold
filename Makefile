.PHONY: dev

dev:
	@trap 'kill 0' EXIT; \
	air & \
	npx tailwindcss -i ./static/css/input.css -o ./static/css/output.css --watch & \
	wait

recreate_db:
	@rm workcraft.db
	@sqlite3 workcraft.db ".databases" ".quit"
	@echo "Database created"

clear_db:
	@sqlite3 ./workcraft.db "DELETE FROM peon; DELETE FROM bountyboard;"
	@echo "Database cleared"
