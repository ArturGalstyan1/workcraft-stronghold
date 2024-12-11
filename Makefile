.PHONY: dev

dev:
	@trap 'kill 0' EXIT; \
	templ generate && \
	air & \
	templ generate -watch & \
	npx tailwindcss -i ./static/css/input.css -o ./static/css/output.css --watch & \
	wait

create_db:
	@sqlite3 workcraft.db ".databases" ".quit"
	@echo "Database created"

clear_db:
	@sqlite3 ./workcraft.db "DELETE FROM peon; DELETE FROM bountyboard;"
	@echo "Database cleared"
