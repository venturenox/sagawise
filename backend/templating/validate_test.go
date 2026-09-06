package templating

import (
	"os"
	"strings"
	"testing"

	"wtfsaga/utils"
)

func good() utils.Workflow {
	return utils.Workflow{
		Name: "flow", Version: "1.0", Schema_version: "1.0",
		Tasks: []utils.Task{
			{Topic: "t1", From: "a", To: "b", Timeout: 1000},
			{Topic: "t1", From: "a", To: "c", Timeout: 1000}, // shared topic, different consumer: legal
			{Topic: "t2", From: "b", To: "c", Timeout: 1},
		},
	}
}

func TestValidate_Accepts(t *testing.T) {
	if err := Validate(good()); err != nil {
		t.Fatalf("valid workflow rejected: %v", err)
	}
}

func TestValidate_Rejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*utils.Workflow)
		want   string
	}{
		{"empty name", func(w *utils.Workflow) { w.Name = " " }, "no name"},
		{"no tasks", func(w *utils.Workflow) { w.Tasks = nil }, "no tasks"},
		{"zero timeout", func(w *utils.Workflow) { w.Tasks[0].Timeout = 0 }, "timeout"},
		{"negative timeout", func(w *utils.Workflow) { w.Tasks[2].Timeout = -1 }, "timeout"},
		{"empty topic", func(w *utils.Workflow) { w.Tasks[1].Topic = "" }, "topic is empty"},
		{"empty from", func(w *utils.Workflow) { w.Tasks[1].From = "" }, "from is empty"},
		{"empty to", func(w *utils.Workflow) { w.Tasks[1].To = "" }, "to is empty"},
		{"duplicate topic+to", func(w *utils.Workflow) { w.Tasks[1].To = "b" }, "ambiguous"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wf := good()
			c.mutate(&wf)
			err := Validate(wf)
			if err == nil {
				t.Fatalf("workflow with %s accepted", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func TestLoadFiles_MissingTimeoutAndDuplicateName(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := dir + "/" + name
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	noTimeout := write("a.json", `{"workflow":{"name":"x","version":"1.0","schema_version":"1.0","tasks":[{"topic":"t","from":"a","to":"b"}]}}`)
	if _, err := loadFiles([]string{noTimeout}); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Errorf("missing timeout: err = %v", err)
	}
	one := write("b.json", `{"workflow":{"name":"dup","version":"1.0","schema_version":"1.0","tasks":[{"topic":"t","from":"a","to":"b","timeout":5}]}}`)
	two := write("c.json", `{"workflow":{"name":"dup","version":"1.0","schema_version":"1.0","tasks":[{"topic":"t","from":"a","to":"b","timeout":5}]}}`)
	if _, err := loadFiles([]string{one, two}); err == nil || !strings.Contains(err.Error(), "already defined") {
		t.Errorf("duplicate name: err = %v", err)
	}
	bad := write("d.json", `{"workflow": nope}`)
	if _, err := loadFiles([]string{bad}); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("malformed json: err = %v", err)
	}
	if wfs, err := loadFiles([]string{one}); err != nil || len(wfs) != 1 || wfs[0].Name != "dup" {
		t.Errorf("valid file: %v, %v", wfs, err)
	}
}

func TestListFiles(t *testing.T) {
	if _, err := listFiles(t.TempDir() + "/missing"); err == nil {
		t.Error("missing dir accepted")
	}
	empty := t.TempDir()
	if _, err := listFiles(empty); err == nil || !strings.Contains(err.Error(), "no DSL files") {
		t.Errorf("empty dir: err = %v", err)
	}
	if err := os.WriteFile(empty+"/x.json", []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if files, err := listFiles(empty); err != nil || len(files) != 1 {
		t.Errorf("one file: %v, %v", files, err)
	}
}
