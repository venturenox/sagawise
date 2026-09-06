//go:build integration

package templating

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"wtfsaga/db_connect"
	"wtfsaga/internal/testx"
	"wtfsaga/utils"
)

// Contract §7: a DSL that fails validation is never stored as a template. (#6)

func setDefault(t testx.T, name, def string) {
	t.Helper()
	if os.Getenv(name) == "" {
		t.Setenv(name, def)
	}
}

// load writes the workflows into a temp dir and runs ParseDSL on it.
// It returns a function reporting whether a template key exists.
func load(t testx.T, workflows ...utils.Workflow) func(name string) bool {
	t.Helper()
	setDefault(t, "REDIS_HOST", "localhost")
	setDefault(t, "REDIS_PORT", "6379")
	setDefault(t, "POSTGRES_HOST", "localhost")
	setDefault(t, "POSTGRES_PORT", "5432")
	setDefault(t, "POSTGRES_USERNAME", "postgres")
	setDefault(t, "POSTGRES_PASSWORD", "venturenox")
	setDefault(t, "POSTGRES_DATABASE", "sagawise")
	t.Setenv("REDIS_CONNECTION_STRING", "")

	ctx := context.Background()
	rdb, err := db_connect.DBConnect(ctx)
	if err != nil {
		t.Fatalf("redis: %v", err)
	}
	db, err := db_connect.ConnectPostgres(ctx)
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}

	dir := t.TempDir()
	for i, wf := range workflows {
		data, _ := json.Marshal(utils.WorkflowData{Workflow: wf})
		name := wf.Name
		if name == "" {
			name = "unnamed"
		}
		if err := os.WriteFile(filepath.Join(dir, name+"_"+string(rune('a'+i))+".json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, wf := range workflows {
			if wf.Name != "" {
				rdb.Del(ctx, "workflow_template:"+wf.Name)
			}
		}
		db.Close()
		_ = rdb.Close()
	})

	_, _ = ParseDSL(ctx, rdb, db, dir) // invalid input is expected to error; the assertions are on what was stored
	return func(name string) bool {
		n, _ := rdb.Exists(ctx, "workflow_template:"+name).Result()
		return n == 1
	}
}

func valid(name string) utils.Workflow {
	return utils.Workflow{
		Name: name, Version: "1.0", Schema_version: "1.0",
		Tasks: []utils.Task{
			{Topic: "dsl_t1", From: "dsl_a", To: "dsl_b", Timeout: 1000},
			{Topic: "dsl_t2", From: "dsl_b", To: "dsl_c", Timeout: 1000},
		},
	}
}

func TestDSL_ValidWorkflowIsStored(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		exists := load(t, valid("dsl_valid"))
		if !exists("dsl_valid") {
			t.Errorf("valid workflow was not stored")
		}
	})
}

// Two tasks sharing a topic with different consumers are legal (user_creation does it).
func TestDSL_SharedTopicDifferentConsumersIsValid(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		wf := valid("dsl_shared")
		wf.Tasks[1].Topic = "dsl_t1"
		wf.Tasks[1].From = "dsl_a"
		wf.Tasks[1].To = "dsl_z"
		exists := load(t, wf)
		if !exists("dsl_shared") {
			t.Errorf("workflow with shared topic / different consumers was rejected")
		}
	})
}

func TestDSL_InvalidWorkflowsAreRejected(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*utils.Workflow)
	}{
		{"zero timeout", func(w *utils.Workflow) { w.Tasks[0].Timeout = 0 }},
		{"negative timeout", func(w *utils.Workflow) { w.Tasks[0].Timeout = -5 }},
		{"no tasks", func(w *utils.Workflow) { w.Tasks = nil }},
		{"empty topic", func(w *utils.Workflow) { w.Tasks[0].Topic = "" }},
		{"empty from", func(w *utils.Workflow) { w.Tasks[0].From = "" }},
		{"empty to", func(w *utils.Workflow) { w.Tasks[0].To = "" }},
		{"duplicate topic+to", func(w *utils.Workflow) { w.Tasks[1].Topic = "dsl_t1"; w.Tasks[1].To = "dsl_b" }},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			testx.Run(t, func(t testx.T) {
				wf := valid("dsl_invalid")
				c.mutate(&wf)
				exists := load(t, wf)
				if exists("dsl_invalid") {
					t.Errorf("workflow with %s was stored; contract §7 rejects it", c.name)
				}
			})
		})
	}
}

// A missing timeout key must be a validation error, not a task with no deadline.
func TestDSL_MissingTimeoutIsRejected(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		_ = load(t, valid("dsl_probe")) // sets env defaults and verifies connectivity
		ctx := context.Background()
		rdb, err := db_connect.DBConnect(ctx)
		if err != nil {
			t.Fatal(err)
		}
		db, err := db_connect.ConnectPostgres(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { rdb.Del(ctx, "workflow_template:dsl_notimeout"); db.Close(); _ = rdb.Close() })

		dir := t.TempDir()
		raw := `{"workflow":{"name":"dsl_notimeout","version":"1.0","schema_version":"1.0",
		  "tasks":[{"topic":"dsl_t1","from":"dsl_a","to":"dsl_b"}]}}`
		if err := os.WriteFile(filepath.Join(dir, "x.json"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ParseDSL(ctx, rdb, db, dir); err == nil {
			t.Errorf("ParseDSL accepted a task with no timeout")
		}
		if n, _ := rdb.Exists(ctx, "workflow_template:dsl_notimeout").Result(); n == 1 {
			t.Errorf("workflow with a missing timeout was stored")
		}
	})
}
