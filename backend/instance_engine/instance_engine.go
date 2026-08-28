package instance_engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"wtfsaga/utils"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/redis/rueidis"
)

var ctx = context.Background()

// ---- shared helpers ----

func httpError(w http.ResponseWriter, code int, msg string) {
	log.Println(msg)
	w.WriteHeader(code)
	fmt.Fprint(w, msg)
}

// requireParams writes a 400 listing every missing query parameter and returns
// false if any of the named parameters is absent.
func requireParams(r *http.Request, w http.ResponseWriter, names ...string) bool {
	msg := ""
	for _, n := range names {
		if r.URL.Query().Get(n) == "" {
			msg += n + " required. "
		}
	}
	if msg != "" {
		httpError(w, 400, msg)
		return false
	}
	return true
}

// jsonAll runs a RedisJSON path query (which always yields an array) and
// unmarshals the matches.
func jsonAll[T any](rdb *redis.Client, key, path string) []T {
	res, err := rdb.JSONGet(ctx, key, path).Result()
	if err != nil {
		return nil
	}
	var out []T
	json.Unmarshal([]byte(res), &out)
	return out
}

// jsonFirst returns the first match of a RedisJSON path query, if any.
func jsonFirst[T any](rdb *redis.Client, key, path string) (T, bool) {
	out := jsonAll[T](rdb, key, path)
	if len(out) == 0 {
		var zero T
		return zero, false
	}
	return out[0], true
}

// markTask sets a task's state and stamps the matching timestamp field.
func markTask(rdb *redis.Client, key, index, state, stampField string) {
	s, _ := json.Marshal(state)
	rdb.JSONSet(ctx, key, "$."+index+".state", s)
	rdb.JSONSet(ctx, key, "$."+index+"."+stampField, time.Now().Unix())
}

var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890")

func generateID() string {
	b := make([]rune, 20)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// failureURL looks up a service's failure webhook in services.json.
func failureURL(service string) string {
	data, err := os.ReadFile("services.json")
	var services []utils.Service
	if err == nil {
		err = json.Unmarshal(data, &services)
	}
	if err != nil {
		log.Printf("Error reading services.json: %v", err)
		return ""
	}
	for _, s := range services {
		if s.ServiceName == service {
			return s.FailureUrl
		}
	}
	return ""
}

// ---- instance lifecycle ----

// Start_instance clones a workflow template into a new workflow_instance doc.
func Start_instance(r *http.Request, w http.ResponseWriter, rdb *redis.Client) {
	if !requireParams(r, w, "workflow_name", "workflow_version") {
		return
	}
	name := r.URL.Query().Get("workflow_name")
	version := r.URL.Query().Get("workflow_version")

	workflow, ok := jsonFirst[utils.Workflow](rdb, "workflow_template:"+name, "$")
	if !ok {
		httpError(w, 400, "workflow_name does not exist")
		return
	}
	if workflow.Version != version {
		httpError(w, 400, "Invalid workflow_version "+version)
		return
	}

	instance := map[string]interface{}{
		"name":           name,
		"version":        workflow.Version,
		"schema_version": workflow.Schema_version,
		"state":          "PENDING",
		"startedAt":      time.Now().Unix(),
	}
	for i, task := range workflow.Tasks {
		index := strconv.Itoa(i)
		instance[index] = map[string]interface{}{
			"topic":   task.Topic,
			"from":    task.From,
			"to":      task.To,
			"state":   "PENDING",
			"timeout": task.Timeout,
			"index":   index,
		}
	}

	id := generateID()
	if err := rdb.JSONSet(ctx, "workflow_instance:"+id, ".", instance).Err(); err != nil {
		httpError(w, 500, "Error: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"workflow_instance_id": id})
}

// Update_instance dispatches a publish/consume/fail report onto the instance.
func Update_instance(r *http.Request, w http.ResponseWriter, rdb *redis.Client, conn *pgx.Conn) {
	if !requireParams(r, w, "action_type", "workflow_instance_id", "workflow_version", "event_name", "is_retry") {
		return
	}
	q := r.URL.Query()
	action := q.Get("action_type")
	id := q.Get("workflow_instance_id")
	topic := q.Get("event_name")
	isRetry, _ := strconv.ParseBool(q.Get("is_retry"))

	if action != "consume" && action != "publish" && action != "fail" {
		httpError(w, 400, "Invalid action_type value. ")
		return
	}

	key := "workflow_instance:" + id
	version, ok := jsonFirst[string](rdb, key, "$.version")
	if !ok {
		httpError(w, 400, "workflow_instance Not Found")
		return
	}
	if version != q.Get("workflow_version") {
		httpError(w, 400, "Invalid workflow version "+q.Get("workflow_version"))
		return
	}

	if action == "publish" {
		handlePublish(rdb, id, w, topic, isRetry, r.Body)
		return
	}
	if !requireParams(r, w, "service_name") {
		return
	}
	handleConsumeOrFail(rdb, conn, id, w, topic, q.Get("service_name"), action, isRetry)
}

// handlePublish marks every task carrying the topic as PUBLISHED, stores the
// message payload, and schedules a consumption deadline per task.
func handlePublish(rdb *redis.Client, id string, w http.ResponseWriter, topic string, isRetry bool, body io.ReadCloser) {
	key := "workflow_instance:" + id
	indexes := jsonAll[string](rdb, key, "$..[?(@.topic=='"+topic+"')].index")
	if len(indexes) == 0 {
		httpError(w, http.StatusNotFound, "Task Not Found")
		return
	}

	bodyBytes, err := io.ReadAll(body)
	body.Close()
	var payload map[string]interface{}
	if err == nil {
		err = json.Unmarshal(bodyBytes, &payload)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	allOk := true
	for _, index := range indexes {
		state, ok := jsonFirst[string](rdb, key, "$."+index+".state")
		if !ok {
			log.Printf("Task %s has no state; skipping", index)
			allOk = false
			continue
		}
		if state != "PENDING" && !isRetry {
			log.Println("Task Already " + state)
			allOk = false
			continue
		}

		markTask(rdb, key, index, "PUBLISHED", "publishedAt")
		rdb.JSONSet(ctx, key, "$."+index+".payload", payload)

		timeout, ok := jsonFirst[int](rdb, key, "$."+index+".timeout")
		if !ok {
			log.Printf("Task %s has no timeout; no deadline scheduled", index)
			continue
		}
		err := rdb.ZAdd(ctx, deadlinesKey, redis.Z{
			Score:  float64(time.Now().UnixMilli() + int64(timeout)),
			Member: deadlineMember(id, index),
		}).Err()
		if err != nil {
			log.Printf("Error scheduling deadline for %s task %s: %v", id, index, err)
		}
	}

	if allOk {
		fmt.Fprint(w, "Instance State Updated")
	} else {
		httpError(w, http.StatusForbidden, "Task Already COMPLETED or FAILED")
	}
}

// handleConsumeOrFail resolves the task by topic + consuming service, then
// completes it (consume) or fails it (fail). Both only act on a PUBLISHED task
// unless the report is a retry.
func handleConsumeOrFail(rdb *redis.Client, conn *pgx.Conn, id string, w http.ResponseWriter, topic, service, action string, isRetry bool) {
	key := "workflow_instance:" + id
	index, ok := jsonFirst[string](rdb, key, "$..[?(@.topic=='"+topic+"' && @.to=='"+service+"')].index")
	if !ok {
		httpError(w, http.StatusNotFound, "Task Not Found")
		return
	}
	state, ok := jsonFirst[string](rdb, key, "$."+index+".state")
	if !ok {
		httpError(w, http.StatusNotFound, "Task Not Found")
		return
	}
	if state != "PUBLISHED" && !isRetry {
		if state == "PENDING" {
			httpError(w, http.StatusForbidden, "Task NOT started Yet")
		} else {
			httpError(w, http.StatusForbidden, "Task Already "+state)
		}
		return
	}

	if action == "fail" {
		reportFailure(rdb, conn, key, id, index)
		fmt.Fprint(w, "Instance State Updated")
		return
	}

	// Claim the deadline before completing the task. ZREM returns 1 for exactly
	// one caller, so this loses to a reaper that already claimed it and is about
	// to mark the task FAILED.
	removed, err := rdb.ZRem(ctx, deadlinesKey, deadlineMember(id, index)).Result()
	if err == nil && removed == 0 && !isRetry {
		httpError(w, http.StatusForbidden, "Task Already FAILED")
		return
	}
	markTask(rdb, key, index, "COMPLETED", "consumedAt")
	fmt.Fprint(w, "Instance State Updated")
	checkWorkflowState(rdb, conn, key)
}

// reportFailure marks the task FAILED, archives the workflow if that made it
// terminal, and POSTs the task payload to the publishing service's failure_url
// so it can compensate.
func reportFailure(rdb *redis.Client, conn *pgx.Conn, key, id, index string) {
	// Whether this came from the reaper or an explicit fail report, the deadline is spent.
	rdb.ZRem(ctx, deadlinesKey, deadlineMember(id, index))
	markTask(rdb, key, index, "FAILED", "failedAt")
	checkWorkflowState(rdb, conn, key)

	from, okFrom := jsonFirst[string](rdb, key, "$."+index+".from")
	to, okTo := jsonFirst[string](rdb, key, "$."+index+".to")
	if !okFrom || !okTo {
		log.Printf("Task %s of %s has no from/to service; cannot report failure", index, key)
		return
	}
	// A task that timed out before anything was published has no payload; report with an empty one.
	payload, _ := jsonFirst[map[string]interface{}](rdb, key, "$."+index+".payload")
	if payload == nil {
		payload = map[string]interface{}{}
	}

	url := failureURL(from)
	if url == "" {
		log.Printf("No failure_url registered for service %q; skipping failure report", from)
		return
	}

	log.Println("Reporting Failure to: " + from)
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("Error building failure request for %s: %v", from, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.URL.RawQuery = "service=" + to
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Failed to send request: %v", err)
		return
	}
	resp.Body.Close()
	log.Println("Response status: ", resp.Status)
}

// checkWorkflowState stamps the instance with a terminal state and archives it
// to Postgres. A workflow is terminal in two ways: every task COMPLETED, or any
// single task FAILED (a failed task means the saga is compensating and will
// never complete).
func checkWorkflowState(rdb *redis.Client, conn *pgx.Conn, key string) {
	doc, ok := jsonFirst[map[string]interface{}](rdb, key, "$")
	if !ok {
		log.Printf("Failed to read instance %s", key)
		return
	}

	name, _ := doc["name"].(string)
	startedAt, _ := doc["startedAt"].(float64)
	total, completed, failed := 0, 0, false
	for field, value := range doc {
		// Task instances are keyed by their numeric index; everything else is metadata.
		if _, err := strconv.Atoi(field); err != nil {
			continue
		}
		total++
		task, _ := value.(map[string]interface{})
		switch task["state"] {
		case "COMPLETED":
			completed++
		case "FAILED":
			failed = true
		}
	}

	var terminal string
	switch {
	case total == 0: // an instance with no tasks is not "complete"; treat as still pending
		return
	case failed:
		terminal = "FAILED"
	case completed == total:
		terminal = "COMPLETED"
	default:
		return
	}

	// Only the first writer to move the instance out of a non-terminal state
	// archives it, so a second failing task in the same workflow does not
	// produce a duplicate archive row.
	if cur, _ := jsonFirst[string](rdb, key, "$.state"); cur == "COMPLETED" || cur == "FAILED" {
		return
	}

	log.Printf("Workflow %s...", terminal)
	finishedAt := time.Now().Unix()
	state, _ := json.Marshal(terminal)
	if err := rdb.JSONSet(ctx, key, "$.state", state).Err(); err != nil {
		log.Printf("Error setting state on %s: %v", key, err)
		return
	}
	if err := rdb.JSONSet(ctx, key, "$.completedAt", finishedAt).Err(); err != nil {
		log.Printf("Error setting completedAt on %s: %v", key, err)
	}

	go func() {
		// Re-read so the archived document includes the terminal state.
		final, ok := jsonFirst[map[string]interface{}](rdb, key, "$")
		if !ok {
			log.Printf("Error re-reading %s for archive", key)
			return
		}
		data, err := json.Marshal(final)
		if err != nil {
			log.Printf("Error encoding %s for archive: %v", key, err)
			return
		}
		archiveInstance(conn, key, name, int64(startedAt), finishedAt, string(data))
	}()
}

// archiveInstance inserts a finished workflow into Postgres. The instance is
// intentionally left in Redis: Redis remains the live store the read endpoints
// query; Postgres is the long-term archive.
func archiveInstance(conn *pgx.Conn, key, name string, startedAt, finishedAt int64, document string) {
	id := strings.Split(key, ":")[1]
	_, err := conn.Exec(ctx, `INSERT INTO instance_history ("id", "name", "startedAt", "completedAt", "instance_data")
		VALUES ($1, $2, TO_TIMESTAMP($3), TO_TIMESTAMP($4), $5)
		ON CONFLICT ("id") DO NOTHING`, id, name, startedAt, finishedAt, document)
	if err != nil {
		log.Printf("Error archiving instance %s: %v", id, err)
	}
}

// ---- read endpoints ----

// List_workflows returns the names of all registered workflow templates.
func List_workflows(w http.ResponseWriter, rdb *redis.Client) {
	result, err := rdb.Do(ctx, "FT.SEARCH", "workflow_templates_index", "*", "RETURN", "1", "workflow_name").Result()
	if err != nil {
		log.Printf("Error searching workflow templates: %v", err)
		httpError(w, 500, "Error searching workflow templates")
		return
	}

	resultMap, _ := result.(map[interface{}]interface{})
	results, _ := resultMap["results"].([]interface{})
	if len(results) == 0 {
		httpError(w, 404, "No Workflows Found")
		return
	}

	names := []string{}
	for _, item := range results {
		itemMap, _ := item.(map[interface{}]interface{})
		attrs, _ := itemMap["extra_attributes"].(map[interface{}]interface{})
		if name, ok := attrs["workflow_name"].(string); ok {
			names = append(names, name)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(names)
}

// List_workflow_instances returns instance IDs matching the query-string
// filters, ANDed together; with no filters it returns everything.
func List_workflow_instances(r *http.Request, w http.ResponseWriter, client rueidis.Client) {
	q := r.URL.Query()
	now := time.Now()

	// timeCond turns "5m"/"15m" into a RediSearch numeric range on a field.
	timeCond := func(field, val string) string {
		var d time.Duration
		switch val {
		case "5m":
			d = 5 * time.Minute
		case "15m":
			d = 15 * time.Minute
		default:
			return ""
		}
		return fmt.Sprintf("@%s:[%d %d]", field, now.Add(-d).Unix(), now.Unix())
	}
	tag := func(field, val string) string {
		if val == "" {
			return ""
		}
		return "@" + field + ":" + val
	}

	var clauses []string
	for _, c := range []string{
		tag("workflow_name", q.Get("workflow_name")),
		tag("workflow_state", q.Get("workflow_state")),
		timeCond("started_at", q.Get("started_at")),
		timeCond("completed_at", q.Get("completed_at")),
		timeCond("failed_at", q.Get("failed_at")),
		tag("topic", q.Get("topic")),
		tag("from", q.Get("from")),
		tag("to", q.Get("to")),
	} {
		if c != "" {
			clauses = append(clauses, c)
		}
	}
	query := "*"
	if len(clauses) > 0 {
		query = strings.Join(clauses, " && ")
	}

	cmd := client.B().FtSearch().Index("workflows_index").Query(query).Build()
	n, resp, _ := client.Do(ctx, cmd).AsFtSearch()
	if n == 0 {
		httpError(w, 404, "No Instances Found")
		return
	}

	var ids []string
	for _, doc := range resp {
		ids = append(ids, doc.Key)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ids)
}

// Get_workflow_instance returns the full JSON document for one instance.
func Get_workflow_instance(r *http.Request, w http.ResponseWriter, rdb *redis.Client) {
	docKey := r.URL.Query().Get("doc_key")
	if docKey == "" {
		httpError(w, 400, "doc_key required. ")
		return
	}
	if !strings.Contains(docKey, ":") {
		httpError(w, 400, "doc_key format Invalid. ")
		return
	}

	instance, ok := jsonFirst[map[string]interface{}](rdb, docKey, "$")
	if !ok {
		httpError(w, 404, "Instance Not Found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(instance)
}
