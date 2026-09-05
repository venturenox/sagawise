package templating

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"wtfsaga/utils"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Validate checks one workflow against contract §7: a non-empty name, at
// least one task, and for every task a non-empty topic/from/to, an integer
// timeout > 0, and no two tasks with the same (topic, to) pair.
func Validate(wf utils.Workflow) error {
	if strings.TrimSpace(wf.Name) == "" {
		return errors.New("workflow has no name")
	}
	if len(wf.Tasks) == 0 {
		return fmt.Errorf("workflow %q has no tasks", wf.Name)
	}
	seen := map[[2]string]int{}
	for i, t := range wf.Tasks {
		switch {
		case t.Topic == "":
			return fmt.Errorf("workflow %q task %d: topic is empty", wf.Name, i)
		case t.From == "":
			return fmt.Errorf("workflow %q task %d (%s): from is empty", wf.Name, i, t.Topic)
		case t.To == "":
			return fmt.Errorf("workflow %q task %d (%s): to is empty", wf.Name, i, t.Topic)
		case t.Timeout <= 0:
			return fmt.Errorf("workflow %q task %d (%s): timeout must be a positive number of milliseconds, got %d (missing or zero)", wf.Name, i, t.Topic, t.Timeout)
		}
		pair := [2]string{t.Topic, t.To}
		if j, dup := seen[pair]; dup {
			return fmt.Errorf("workflow %q tasks %d and %d both have topic %q consumed by %q; a consume would be ambiguous", wf.Name, j, i, t.Topic, t.To)
		}
		seen[pair] = i
	}
	return nil
}

// listFiles returns every *.json file in dir, sorted. The directory must
// exist and hold at least one file: an empty or mis-mounted DSL directory is
// a configuration error, not "no workflows".
func listFiles(dir string) ([]string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("DSL directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("DSL directory %s is not a directory", dir)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("list DSL files in %s: %w", dir, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no DSL files (*.json) found in %s", dir)
	}
	return files, nil
}

// loadFiles parses and validates every DSL file. Nothing is stored until
// every file is known to be valid, so a broken file never leaves a partial
// set of templates behind.
func loadFiles(files []string) ([]utils.Workflow, error) {
	var workflows []utils.Workflow
	names := map[string]string{}
	for _, file := range files {
		data, err := os.ReadFile(file) // #nosec G304 -- file comes from a fixed glob over the DSL dir, not user input
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		var workflowData utils.WorkflowData
		if err := json.Unmarshal(data, &workflowData); err != nil {
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}
		wf := workflowData.Workflow
		if err := Validate(wf); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if other, dup := names[wf.Name]; dup {
			return nil, fmt.Errorf("%s: workflow %q is already defined in %s", file, wf.Name, other)
		}
		names[wf.Name] = file
		workflows = append(workflows, wf)
	}
	return workflows, nil
}

// ParseDSL reads every *.json DSL file in dir, validates them all, stores
// each workflow as a RedisJSON template, ensures the RediSearch indexes and
// the Postgres instance_history table exist, and returns the loaded
// workflows. Any failure is returned; the caller must not serve on error.
func ParseDSL(ctx context.Context, rdb *redis.Client, conn *pgxpool.Pool, dir string) ([]utils.Workflow, error) {
	files, err := listFiles(dir)
	if err != nil {
		return nil, err
	}
	workflows, err := loadFiles(files)
	if err != nil {
		return nil, err
	}

	for _, wf := range workflows {
		if err := rdb.JSONSet(ctx, "workflow_template:"+wf.Name, ".", wf).Err(); err != nil {
			return nil, fmt.Errorf("store template %q: %w", wf.Name, err)
		}
	}
	log.Printf("Loaded %d workflow template(s) from %s", len(workflows), dir)

	if err := ensureIndex(ctx, rdb, "workflow_templates_index", "workflow_template:",
		"$.name", "AS", "workflow_name", "TEXT",
	); err != nil {
		return nil, err
	}
	// Filterable fields are TAG so a value is matched literally: a hyphen in a
	// workflow name is a hyphen, not RediSearch negation. (#10) Every path is
	// explicit (no `$..`): task fields are read from $.tasks[*] only, so a
	// payload key named "topic" is never indexed and a type-mismatched payload
	// field cannot knock the document out of the index. (design note §1)
	if err := ensureIndex(ctx, rdb, "workflows_index", "workflow_instance:",
		"$.name", "AS", "workflow_name", "TAG",
		"$.state", "AS", "workflow_state", "TAG",
		"$.tasks[*].topic", "AS", "topic", "TAG",
		"$.tasks[*].from", "AS", "from", "TAG",
		"$.tasks[*].to", "AS", "to", "TAG",
		"$.startedAt", "AS", "started_at", "NUMERIC",
		"$.completedAt", "AS", "completed_at", "NUMERIC",
		"$.failedAt", "AS", "failed_at", "NUMERIC",
	); err != nil {
		return nil, err
	}
	log.Println("Redis Indexes Created Successfully")

	// The primary key is added only when missing, so this is safe to run on
	// every start and by several processes at once.
	query := `CREATE TABLE IF NOT EXISTS "instance_history" (
		"id" text NOT NULL,
		"name" text NOT NULL,
		"startedAt" timestamp NOT NULL,
		"completedAt" timestamp NOT NULL,
		"instance_data" json NOT NULL
	);
	DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'instance_history_id') THEN
			ALTER TABLE "instance_history" ADD CONSTRAINT "instance_history_id" PRIMARY KEY ("id");
		END IF;
	END $$;`
	if _, err := conn.Exec(ctx, query); err != nil {
		return nil, fmt.Errorf("create instance_history table: %w", err)
	}
	log.Println("PostgreSQL Table & Index Created Successfully")
	return workflows, nil
}

// ensureIndex creates a RediSearch index on JSON documents with the given
// key prefix and schema. The schema's hash is stored under
// index_schema:<name>; when the index exists with a different schema it is
// dropped (documents are kept) and recreated so a code upgrade that changes
// the schema takes effect without manual intervention.
func ensureIndex(ctx context.Context, rdb *redis.Client, name, prefix string, schema ...string) error {
	h := sha256.Sum256([]byte(strings.Join(schema, "\x00")))
	want := hex.EncodeToString(h[:8])
	verKey := "index_schema:" + name

	have, err := rdb.Get(ctx, verKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("read %s: %w", verKey, err)
	}
	exists := true
	if err := rdb.Do(ctx, "FT.INFO", name).Err(); err != nil {
		if !isUnknownIndex(err) {
			return fmt.Errorf("FT.INFO %s: %w", name, err)
		}
		exists = false
	}
	if exists && have == want {
		return nil
	}
	if exists {
		log.Printf("Index %s has an outdated schema; recreating it", name)
		if err := rdb.Do(ctx, "FT.DROPINDEX", name).Err(); err != nil && !isUnknownIndex(err) {
			return fmt.Errorf("FT.DROPINDEX %s: %w", name, err)
		}
	}

	args := []interface{}{"FT.CREATE", name, "ON", "JSON", "PREFIX", "1", prefix, "SCHEMA"}
	for _, s := range schema {
		args = append(args, s)
	}
	// Another process (a parallel test binary, a second replica) may have
	// created the same index between our check and this call.
	if err := rdb.Do(ctx, args...).Err(); err != nil && !strings.Contains(err.Error(), "Index already exists") {
		return fmt.Errorf("FT.CREATE %s: %w", name, err)
	}
	if err := rdb.Set(ctx, verKey, want, 0).Err(); err != nil {
		return fmt.Errorf("write %s: %w", verKey, err)
	}
	return nil
}

func isUnknownIndex(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unknown index") || strings.Contains(msg, "no such index")
}
