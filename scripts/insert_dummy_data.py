import json
import sqlite3
import uuid
from datetime import datetime, timedelta


# Helper function to generate UUIDs
def gen_uuid():
    return str(uuid.uuid4())


# Helper function to get current timestamp in ISO format
def get_timestamp():
    return datetime.utcnow().isoformat()


# Connect to database
with sqlite3.connect("../workcraft.db") as connection:
    cursor = connection.cursor()

    # Create 3 offline peons
    peon_ids = []
    for i in range(3):
        peon_id = gen_uuid()
        peon_ids.append(peon_id)

        cursor.execute(
            """
            INSERT INTO peons (
                id, created_at, updated_at, status, last_heartbeat, queues
            ) VALUES (?, ?, ?, ?, ?, ?)
        """,
            (
                peon_id,
                get_timestamp(),
                get_timestamp(),
                "OFFLINE",  # Status set to offline
                (
                    datetime.utcnow() - timedelta(minutes=30)
                ).isoformat(),  # Old heartbeat
                json.dumps(["default"]),  # Default queue
            ),
        )

    # Create 5 tasks
    tasks = []
    for i in range(5):
        task_id = gen_uuid()
        tasks.append(task_id)

        # Create a basic task payload
        payload = {
            "task_args": [],
            "task_kwargs": {"test": f"task_{i}"},
            "prerun_handler_args": [],
            "prerun_handler_kwargs": {},
            "postrun_handler_args": [],
            "postrun_handler_kwargs": {},
        }

        # The first task will be set to RUNNING and assigned to the first peon
        status = "RUNNING" if i == 0 else "PENDING"
        peon_id = peon_ids[0] if i == 0 else None

        cursor.execute(
            """
            INSERT INTO tasks (
                id, created_at, updated_at, task_name, status, peon_id,
                queue, payload, retry_on_failure, retry_count, retry_limit
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        """,
            (
                task_id,
                get_timestamp(),
                get_timestamp(),
                f"test_task_{i}",
                status,
                peon_id,
                "default",
                json.dumps(payload),
                True,  # retry_on_failure
                0,  # retry_count
                3,  # retry_limit
            ),
        )

    # Commit the changes
    connection.commit()

print("Created inconsistent state:")
print(f"- Created 3 offline peons with IDs: {peon_ids}")
print(f"- Created 5 tasks with IDs: {tasks}")
print(f"- Task {tasks[0]} is set to RUNNING and assigned to offline peon {peon_ids[0]}")
