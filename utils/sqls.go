package utils

func GetIdlePeons() string {
	return `
	SELECT id FROM peon
	WHERE status = 'IDLE'
	AND (
	queues LIKE '[%''' || ? || '''%]'
	OR queues IS NULL
	)
	ORDER BY last_heartbeat ASC
	LIMIT 1
	`
}

func GetAnyOnlinePeon() string {
	return `
	SELECT id FROM peon
	WHERE status != 'OFFLINE'
	AND (
	queues LIKE '[%''' || ? || '''%]'
	OR queues IS NULL
	)
	ORDER BY last_heartbeat ASC
	LIMIT 1
	`
}

func GetPendingTasks() string {
	return `
	SELECT id, task_name, queue, payload, retry_count, retry_limit, retry_on_failure
	FROM bountyboard
	WHERE status = 'PENDING'
	   OR (status = 'FAILURE'
	       AND retry_on_failure = TRUE
	       AND retry_count < retry_limit
	       AND updated_at < datetime('now', '-1 minutes'))
	ORDER BY created_at ASC
	LIMIT 10`
}

func MarkPeonAsOffline() string {
	return "UPDATE peon SET status = 'OFFLINE' WHERE id = ?"
}

func CleanPeons() string {
	return "UPDATE peon SET status = 'OFFLINE', current_task = NULL WHERE last_heartbeat < datetime('now', '-1 minutes')"
}

func CleanBountyboard() string {
	return `
    UPDATE bountyboard
    SET status = 'PENDING',
        peon_id = NULL
    WHERE status = 'RUNNING'
    AND peon_id IS NOT NULL
    AND (
        NOT EXISTS (
            SELECT 1 FROM peon
            WHERE peon.id = bountyboard.peon_id
            AND peon.status != 'OFFLINE'
        )
        OR
        EXISTS (
            SELECT 1 FROM peon
            WHERE peon.id = bountyboard.peon_id
            AND peon.status != 'OFFLINE'
            AND peon.current_task != bountyboard.id
        )
    )`
}

func InsertPeon() string {
	return `INSERT INTO peon (id, queues) VALUES (?, ?) ON CONFLICT (id) DO UPDATE SET status = 'IDLE'`
}

func CreatePeonTable() string {
	return `CREATE TABLE IF NOT EXISTS peon (
		id VARCHAR(36) PRIMARY KEY,
		status TEXT DEFAULT 'IDLE' CHECK (status IN ('IDLE', 'PREPARING', 'WORKING', 'OFFLINE')),
		last_heartbeat TIMESTAMP DEFAULT (datetime ('now')),
		current_task CHAR(36),
		queues TEXT
	);`
}

func CreatePeonTrigger() string {
	return `CREATE TRIGGER IF NOT EXISTS peon_last_heartbeat_update AFTER
	UPDATE ON peon WHEN NEW.status != OLD.status BEGIN
	UPDATE peon
	SET last_heartbeat = datetime ('now')
	WHERE id = NEW.id;
	END;`
}

func CreateBountyboardTable() string {
	return `CREATE TABLE IF NOT EXISTS bountyboard (
	    id CHAR(36) PRIMARY KEY,
	    status TEXT NOT NULL DEFAULT 'PENDING' CHECK (
	        status IN (
	            'PENDING',
	            'RUNNING',
	            'SUCCESS',
	            'FAILURE',
	            'INVALID',
                'CANCELLED'
	        )
	    ),
	    created_at TIMESTAMP DEFAULT (datetime ('now')),
	    updated_at TIMESTAMP DEFAULT (datetime ('now')),
	    task_name VARCHAR(255) NOT NULL,
	    peon_id VARCHAR(36),
	    queue VARCHAR(255) DEFAULT 'DEFAULT',
	    payload TEXT,
	    result TEXT,
	    retry_on_failure BOOLEAN DEFAULT FALSE,
	    retry_count INTEGER DEFAULT 0,
	    retry_limit INTEGER NOT NULL DEFAULT 0,
	    CONSTRAINT chk_retry_limit CHECK (retry_limit >= 0),
	    CONSTRAINT chk_retry_consistency CHECK (
	        (retry_on_failure = 0)
	        OR (
	            retry_on_failure = 1
	            AND retry_limit > 1
	        )
	    ),
	    FOREIGN KEY (peon_id) REFERENCES peon (id)
	)`
}

func CreateBountyboardTrigger() string {
	return `CREATE TRIGGER IF NOT EXISTS bountyboard_updated_at AFTER
		UPDATE ON bountyboard BEGIN
		UPDATE bountyboard
		SET
		    updated_at = datetime ('now')
		WHERE
		    id = NEW.id;
		END`
}

func CreateStatsTable() string {
	return `
	CREATE TABLE IF NOT EXISTS stats (
		id INTEGER PRIMARY KEY,
		type TEXT NOT NULL,
		value TEXT NOT NULL,
		task_id CHAR(36),
		peon_id CHAR(36),
		created_at TIMESTAMP DEFAULT (datetime ('now')),
		FOREIGN KEY (task_id) REFERENCES bountyboard (id),
		FOREIGN KEY (peon_id) REFERENCES peon (id)
	);
	`
}

func InsertIntoBountyboard() string {
	return `
		INSERT INTO bountyboard (id, status, task_name, queue, payload, retry_on_failure, retry_limit) VALUES (?, ?, ?, ?, ?, ?, ?)
	`
}
