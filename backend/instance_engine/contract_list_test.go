//go:build integration

package instance_engine

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"wtfsaga/internal/testx"
	"wtfsaga/utils"
)

// listIDs accepts either today's bare array or the contract's page object
// and returns the ids and the total.
func listIDs(t testx.T, body []byte) ([]string, int) {
	t.Helper()
	var arr []string
	if err := json.Unmarshal(body, &arr); err == nil {
		return arr, len(arr)
	}
	var page struct {
		IDs   []string `json:"ids"`
		Total int      `json:"total"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Errorf("list body is neither array nor page: %s", body)
		return nil, 0
	}
	return page.IDs, page.Total
}

// Contract §9: the list is paged with an explicit default, never a silent
// cap at the RediSearch default of 10. (#10, fixed in phase 5)
func TestContract_ListIsNotCappedAtTen(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		const n = 12
		for i := 0; i < n; i++ {
			e.start()
		}
		w := e.do(e.eng.ListWorkflowInstances, http.MethodGet, "/workflow_instances/list?workflow_name="+itWorkflow, "")
		if w.Code != 200 {
			t.Fatalf("list: %d %s", w.Code, w.Body.String())
		}
		ids, total := listIDs(t, w.Body.Bytes())
		if len(ids) != n || total != n {
			t.Errorf("list returned %d ids (total %d), want %d", len(ids), total, n)
		}
	})
}

// A hyphen in a workflow name is a literal, not RediSearch negation. (#10)
func TestContract_ListHyphenatedName(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		wf := twoTaskFlow()
		wf.Name = "it-hyphen-flow"
		e := newEnv(t, wf)
		e.start(wf.Name)
		w := e.do(e.eng.ListWorkflowInstances, http.MethodGet, "/workflow_instances/list?workflow_name="+wf.Name, "")
		if w.Code != 200 {
			t.Fatalf("list: %d %s", w.Code, w.Body.String())
		}
		ids, _ := listIDs(t, w.Body.Bytes())
		if len(ids) != 1 {
			t.Errorf("list returned %d ids, want 1", len(ids))
		}
	})
}

// No matches is an empty page, not an error. (#10)
func TestContract_ListEmptyIs200(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		w := e.do(e.eng.ListWorkflowInstances, http.MethodGet, "/workflow_instances/list?workflow_name=it_nonexistent_zz", "")
		if w.Code != 200 {
			t.Errorf("empty list: %d %s, want 200", w.Code, w.Body.String())
		}
		ids, total := listIDs(t, w.Body.Bytes())
		if len(ids) != 0 || total != 0 {
			t.Errorf("empty list returned %d ids, total %d", len(ids), total)
		}
	})
}

// limit/offset paging. (#10)
func TestContract_ListPaging(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		const n = 5
		for i := 0; i < n; i++ {
			e.start()
		}
		seen := map[string]bool{}
		for offset := 0; offset < n; offset += 2 {
			w := e.do(e.eng.ListWorkflowInstances, http.MethodGet,
				"/workflow_instances/list?workflow_name="+itWorkflow+"&limit=2&offset="+strconv.Itoa(offset), "")
			if w.Code != 200 {
				t.Fatalf("list offset %d: %d %s", offset, w.Code, w.Body.String())
			}
			ids, total := listIDs(t, w.Body.Bytes())
			if total != n {
				t.Errorf("offset %d: total = %d, want %d", offset, total, n)
			}
			if len(ids) > 2 {
				t.Errorf("offset %d: page has %d ids, limit was 2", offset, len(ids))
			}
			for _, id := range ids {
				seen[id] = true
			}
		}
		if len(seen) != n {
			t.Errorf("paging visited %d distinct ids, want %d", len(seen), n)
		}
	})
}

var _ = utils.Workflow{}
