package templating

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"os"
	"path"

	"wtfsaga/utils"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

var ctx = context.Background()

// The `listFiles` function takes a directory path as input, lists all JSON files in that directory,
// and returns a slice of file paths.
func listFiles(dir string) []string {
	root := os.DirFS(dir)

	jsonFiles, _ := fs.Glob(root, "*.json")

	var files []string
	for _, v := range jsonFiles {
		files = append(files, path.Join(dir, v))
	}
	return files
}

// The `ParseDSL` function reads DSL files, processes the data, stores templates in Redis, creates
// indexes in Redis, and creates a table with an index in PostgreSQL.
func ParseDSL(rdb *redis.Client, conn *pgx.Conn) {
	// List file
	files := listFiles("/sagawise")

	if len(files) == 0 {
		log.Println("No DSL files found...!!")
	} else {

		for _, file := range files {

			// Read the JSON file
			data, err := os.ReadFile(file)
			if err != nil {
				log.Printf("Error reading JSON file %s: %v", file, err)
				continue
			}

			// Unmarshal JSON into DSL struct. One malformed DSL must not stop the remaining
			// templates from loading, nor skip index creation below.
			var workflowData utils.WorkflowData
			if err := json.Unmarshal(data, &workflowData); err != nil {
				log.Printf("Error unmarshalling JSON in %s: %v", file, err)
				continue
			}

			if workflowData.Workflow.Name == "" {
				log.Printf("Skipping %s: workflow has no name", file)
				continue
			}

			workflow_err := rdb.JSONSet(ctx, "workflow_template:"+workflowData.Workflow.Name, ".", workflowData.Workflow).Err()
			if workflow_err != nil {
				log.Println("Error: ", workflow_err)
			}
		}

		log.Println("Template Generated from DSL Successfully")

		// Create Workflow Templates Index
		rdb.Do(
			ctx,
			"FT.CREATE", "workflow_templates_index",
			"ON", "JSON",
			"PREFIX", "1", "workflow_template:",
			"SCHEMA",
			"$.name", "AS", "workflow_name", "TEXT",
		).Result()

		// Create Workflows Index
		rdb.Do(
			ctx,
			"FT.CREATE", "workflows_index",
			"ON", "JSON",
			"PREFIX", "1", "workflow_instance:",
			"SCHEMA",
			"$.name", "AS", "workflow_name", "TEXT",
			"$.state", "AS", "workflow_state", "TEXT",
			"$..topic", "AS", "topic", "TEXT",
			"$..from", "AS", "from", "TEXT",
			"$..to", "AS", "to", "TEXT",
			"$.startedAt", "AS", "started_at", "NUMERIC",
			"$.completedAt", "AS", "completed_at", "NUMERIC",
			"$..failedAt", "AS", "failed_at", "NUMERIC",
		).Result()

		log.Println("Redis Indexes Created Successfully")

		// Create PostgreSQL Table & Index
		query := `CREATE TABLE IF NOT EXISTS "instance_history" (
			"id" text NOT NULL,
			"name" text NOT NULL,
			"startedAt" timestamp NOT NULL,
			"completedAt" timestamp NOT NULL,
			"instance_data" json NOT NULL
		);
		ALTER TABLE "instance_history"
		DROP CONSTRAINT IF EXISTS "instance_history_id";
		ALTER TABLE "instance_history"
		ADD CONSTRAINT "instance_history_id" PRIMARY KEY ("id");`
		_, err := conn.Exec(ctx, query)
		if err != nil {
			log.Println("PostgreSQL Error: ", err)
		} else {
			log.Println("PostgreSQL Table & Index Created Successfully")
		}
	}
}
