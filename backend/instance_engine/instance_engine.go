package instance_engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"wtfsaga/utils"

	"github.com/redis/go-redis/v9"
)

// ---- shared helpers ----

func httpError(w http.ResponseWriter, code int, msg string) {
	log.Println(msg)
	// msg can echo caller-supplied params; pin the content type so it is never sniffed as HTML.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	writeText(w, msg)
}

// writeText writes a plain-text body. A failed write means the client went
// away; there is nothing left to tell it, so it is only logged.
func writeText(w http.ResponseWriter, msg string) {
	// #nosec G705 -- callers pin Content-Type to text/plain with nosniff, or write a fixed string; never rendered as HTML
	if _, err := fmt.Fprint(w, msg); err != nil {
		log.Printf("Error writing response: %v", err)
	}
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

// jsonMatches runs a RedisJSON path query (which always yields an array) and
// unmarshals the matches.
func jsonMatches[T any](ctx context.Context, rdb *redis.Client, key, path string) []T {
	res, err := rdb.JSONGet(ctx, key, path).Result()
	if err != nil {
		return nil
	}
	var out []T
	if err := json.Unmarshal([]byte(res), &out); err != nil {
		log.Printf("Error decoding %s %s: %v", key, path, err)
		return nil
	}
	return out
}

// writeJSON writes v as the JSON response body. A failed write means the
// client went away; there is nothing left to tell it, so it is only logged.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("Error writing response: %v", err)
	}
}

// parseRetry parses is_retry strictly: "true" or "false", case-insensitive
// (the Python SDK sends True/False). Anything else is a client error, never
// silently false. (contract §4)
func parseRetry(v string) (bool, bool) {
	switch strings.ToLower(v) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// jsonFirstMatch returns the first match of a RedisJSON path query, if any.
func jsonFirstMatch[T any](ctx context.Context, rdb *redis.Client, key, path string) (T, bool) {
	out := jsonMatches[T](ctx, rdb, key, path)
	if len(out) == 0 {
		var zero T
		return zero, false
	}
	return out[0], true
}

// markTask sets a task's state and stamps the matching timestamp field.
func (e *Engine) markTask(ctx context.Context, key, index, state, stampField string) {
	s, _ := json.Marshal(state)
	if err := e.RDB.JSONSet(ctx, key, "$."+index+".state", s).Err(); err != nil {
		log.Printf("Error setting state on %s task %s: %v", key, index, err)
	}
	if err := e.RDB.JSONSet(ctx, key, "$."+index+"."+stampField, e.Clock.Now().Unix()).Err(); err != nil {
		log.Printf("Error setting %s on %s task %s: %v", stampField, key, index, err)
	}
}

var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890")

func generateID() string {
	b := make([]rune, 20)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			panic("crypto/rand unavailable: " + err.Error())
		}
		b[i] = letters[n.Int64()]
	}
	return string(b)
}

// ---- instance lifecycle ----

// StartInstance clones a workflow template into a new workflow_instance doc.
// The template is the single source of truth for the version; any
// workflow_version query parameter is ignored.
func (e *Engine) StartInstance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !requireParams(r, w, "workflow_name") {
		return
	}
	name := r.URL.Query().Get("workflow_name")

	workflow, ok := jsonFirstMatch[utils.Workflow](ctx, e.RDB, "workflow_template:"+name, "$")
	if !ok {
		httpError(w, 400, "workflow_name does not exist")
		return
	}

	instance := map[string]interface{}{
		"name":           name,
		"version":        workflow.Version,
		"schema_version": workflow.Schema_version,
		"state":          "PENDING",
		"startedAt":      e.Clock.Now().Unix(),
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
	if err := e.RDB.JSONSet(ctx, instanceKey(id), ".", instance).Err(); err != nil {
		httpError(w, 500, "Error: "+err.Error())
		return
	}
	writeJSON(w, map[string]string{"workflow_instance_id": id})
}

// UpdateInstance dispatches a publish/consume/fail report onto the instance.
func (e *Engine) UpdateInstance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !requireParams(r, w, "action_type", "workflow_instance_id", "event_name", "is_retry") {
		return
	}
	q := r.URL.Query()
	action := q.Get("action_type")
	id := q.Get("workflow_instance_id")
	topic := q.Get("event_name")
	isRetry, ok := parseRetry(q.Get("is_retry"))
	if !ok {
		httpError(w, 400, "Invalid is_retry value; must be true or false. ")
		return
	}

	if action != "consume" && action != "publish" && action != "fail" {
		httpError(w, 400, "Invalid action_type value. ")
		return
	}

	if _, ok := jsonFirstMatch[string](ctx, e.RDB, instanceKey(id), "$.version"); !ok {
		httpError(w, 400, "workflow_instance Not Found")
		return
	}

	if action == "publish" {
		e.handlePublish(ctx, w, id, topic, isRetry, r.Body)
		return
	}
	if !requireParams(r, w, "service_name") {
		return
	}
	e.handleConsumeOrFail(ctx, w, id, topic, q.Get("service_name"), action, isRetry)
}

// handlePublish marks every task carrying the topic as PUBLISHED, stores the
// message payload, and schedules a consumption deadline per task.
func (e *Engine) handlePublish(ctx context.Context, w http.ResponseWriter, id, topic string, isRetry bool, body io.ReadCloser) {
	key := instanceKey(id)
	indexes := jsonMatches[string](ctx, e.RDB, key, "$..[?(@.topic=='"+topic+"')].index")
	if len(indexes) == 0 {
		httpError(w, http.StatusNotFound, "Task Not Found")
		return
	}

	bodyBytes, err := io.ReadAll(body)
	_ = body.Close()
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
		state, ok := jsonFirstMatch[string](ctx, e.RDB, key, "$."+index+".state")
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

		e.markTask(ctx, key, index, "PUBLISHED", "publishedAt")
		e.RDB.JSONSet(ctx, key, "$."+index+".payload", payload)

		timeout, ok := jsonFirstMatch[int](ctx, e.RDB, key, "$."+index+".timeout")
		if !ok {
			log.Printf("Task %s has no timeout; no deadline scheduled", index)
			continue
		}
		err := e.RDB.ZAdd(ctx, deadlinesKey, redis.Z{
			Score:  float64(e.Clock.Now().UnixMilli() + int64(timeout)),
			Member: deadlineMember(id, index),
		}).Err()
		if err != nil {
			log.Printf("Error scheduling deadline for %s task %s: %v", id, index, err)
		}
	}

	if allOk {
		writeText(w, "Instance State Updated")
	} else {
		httpError(w, http.StatusForbidden, "Task Already COMPLETED or FAILED")
	}
}

// handleConsumeOrFail resolves the task by topic + consuming service, then
// completes it (consume) or fails it (fail). Both only act on a PUBLISHED task
// unless the report is a retry.
func (e *Engine) handleConsumeOrFail(ctx context.Context, w http.ResponseWriter, id, topic, service, action string, isRetry bool) {
	key := instanceKey(id)
	index, ok := jsonFirstMatch[string](ctx, e.RDB, key, "$..[?(@.topic=='"+topic+"' && @.to=='"+service+"')].index")
	if !ok {
		httpError(w, http.StatusNotFound, "Task Not Found")
		return
	}
	state, ok := jsonFirstMatch[string](ctx, e.RDB, key, "$."+index+".state")
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
		e.reportFailure(ctx, key, id, index)
		writeText(w, "Instance State Updated")
		return
	}

	// Claim the deadline before completing the task. ZREM returns 1 for exactly
	// one caller, so this loses to a reaper that already claimed it and is about
	// to mark the task FAILED.
	removed, err := e.RDB.ZRem(ctx, deadlinesKey, deadlineMember(id, index)).Result()
	if err == nil && removed == 0 && !isRetry {
		httpError(w, http.StatusForbidden, "Task Already FAILED")
		return
	}
	e.markTask(ctx, key, index, "COMPLETED", "consumedAt")
	writeText(w, "Instance State Updated")
	e.checkWorkflowState(ctx, key)
}

// reportFailure marks the task FAILED, archives the workflow if that made it
// terminal, and POSTs the task payload to the publishing service's failure_url
// so it can compensate.
func (e *Engine) reportFailure(ctx context.Context, key, id, index string) {
	// Whether this came from the reaper or an explicit fail report, the deadline is spent.
	if err := e.RDB.ZRem(ctx, deadlinesKey, deadlineMember(id, index)).Err(); err != nil {
		log.Printf("Error removing deadline for %s task %s: %v", id, index, err)
	}
	e.markTask(ctx, key, index, "FAILED", "failedAt")
	e.checkWorkflowState(ctx, key)

	from, okFrom := jsonFirstMatch[string](ctx, e.RDB, key, "$."+index+".from")
	to, okTo := jsonFirstMatch[string](ctx, e.RDB, key, "$."+index+".to")
	if !okFrom || !okTo {
		log.Printf("Task %s of %s has no from/to service; cannot report failure", index, key)
		return
	}
	// A task that timed out before anything was published has no payload; report with an empty one.
	payload, _ := jsonFirstMatch[map[string]interface{}](ctx, e.RDB, key, "$."+index+".payload")
	if payload == nil {
		payload = map[string]interface{}{}
	}

	url, err := e.Services.FailureURL(from)
	if err != nil {
		log.Printf("Error looking up failure_url for %q: %v", from, err)
		return
	}
	if url == "" {
		log.Printf("No failure_url registered for service %q; skipping failure report", from)
		return
	}

	log.Println("Reporting Failure to: " + from)
	body, _ := json.Marshal(payload)
	// #nosec G704 -- url is operator configuration (the ServiceRegistry, e.g. services.json)
	// looked up by a DSL-declared service name; it never comes from request input.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("Error building failure request for %s: %v", from, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.URL.RawQuery = "service=" + to
	resp, err := e.HTTPClient.Do(req) // #nosec G704 -- see above
	if err != nil {
		log.Printf("Failed to send request: %v", err)
		return
	}
	_ = resp.Body.Close()
	log.Println("Response status: ", resp.Status)
}

// checkWorkflowState stamps the instance with a terminal state and archives it
// to Postgres. A workflow is terminal in two ways: every task COMPLETED, or any
// single task FAILED (a failed task means the saga is compensating and will
// never complete).
func (e *Engine) checkWorkflowState(ctx context.Context, key string) {
	doc, ok := jsonFirstMatch[map[string]interface{}](ctx, e.RDB, key, "$")
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
	if cur, _ := jsonFirstMatch[string](ctx, e.RDB, key, "$.state"); cur == "COMPLETED" || cur == "FAILED" {
		return
	}

	log.Printf("Workflow %s...", terminal)
	finishedAt := e.Clock.Now().Unix()
	state, _ := json.Marshal(terminal)
	if err := e.RDB.JSONSet(ctx, key, "$.state", state).Err(); err != nil {
		log.Printf("Error setting state on %s: %v", key, err)
		return
	}
	if err := e.RDB.JSONSet(ctx, key, "$.completedAt", finishedAt).Err(); err != nil {
		log.Printf("Error setting completedAt on %s: %v", key, err)
	}

	// The archive must outlive the request that triggered it, so detach from
	// the caller's cancellation while keeping its values (trace context).
	bg := context.WithoutCancel(ctx)
	go func() {
		// Re-read so the archived document includes the terminal state.
		final, ok := jsonFirstMatch[map[string]interface{}](bg, e.RDB, key, "$")
		if !ok {
			log.Printf("Error re-reading %s for archive", key)
			return
		}
		data, err := json.Marshal(final)
		if err != nil {
			log.Printf("Error encoding %s for archive: %v", key, err)
			return
		}
		e.archiveInstance(bg, key, name, int64(startedAt), finishedAt, string(data))
	}()
}

// archiveInstance inserts a finished workflow into Postgres. The instance is
// intentionally left in Redis: Redis remains the live store the read endpoints
// query; Postgres is the long-term archive.
func (e *Engine) archiveInstance(ctx context.Context, key, name string, startedAt, finishedAt int64, document string) {
	id := strings.Split(key, ":")[1]
	_, err := e.DB.Exec(ctx, `INSERT INTO instance_history ("id", "name", "startedAt", "completedAt", "instance_data")
		VALUES ($1, $2, TO_TIMESTAMP($3), TO_TIMESTAMP($4), $5)
		ON CONFLICT ("id") DO NOTHING`, id, name, startedAt, finishedAt, document)
	if err != nil {
		log.Printf("Error archiving instance %s: %v", id, err)
	}
}

// ---- read endpoints ----

// ListWorkflows returns the names of all registered workflow templates.
func (e *Engine) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	result, err := e.RDB.Do(ctx, "FT.SEARCH", "workflow_templates_index", "*", "RETURN", "1", "workflow_name").Result()
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
	writeJSON(w, names)
}

const (
	listDefaultLimit = 50
	listMaxLimit     = 1000
)

// escapeTag makes a query value a literal inside a RediSearch TAG filter:
// every ASCII byte that is not a letter, digit or underscore is
// backslash-escaped, so `order-flow` matches "order-flow" and nothing else. (#10)
func escapeTag(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
		if c < 0x80 && !alnum {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}

// pageParam parses a non-negative integer query parameter with a default for
// the empty string and an upper bound (0 = unbounded).
func pageParam(v string, def, max int) (int, error) {
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("must be a non-negative integer")
	}
	if max > 0 && n > max {
		return 0, fmt.Errorf("must be at most %d", max)
	}
	return n, nil
}

// ListWorkflowInstances returns a page of instance IDs matching the
// query-string filters, ANDed together; with no filters it lists everything.
// `limit` (default 50, max 1000) and `offset` page through the result;
// `total` is the full match count. No match is an empty page, not an error;
// an index error is a 500. (#10)
func (e *Engine) ListWorkflowInstances(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	now := e.Clock.Now()

	limit, err := pageParam(q.Get("limit"), listDefaultLimit, listMaxLimit)
	if err != nil {
		httpError(w, 400, "Invalid limit: "+err.Error()+". ")
		return
	}
	if limit == 0 {
		limit = listDefaultLimit
	}
	offset, err := pageParam(q.Get("offset"), 0, 0)
	if err != nil {
		httpError(w, 400, "Invalid offset: "+err.Error()+". ")
		return
	}

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
		return "@" + field + ":{" + escapeTag(val) + "}"
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
		query = strings.Join(clauses, " ")
	}

	res, err := e.RDB.FTSearchWithArgs(ctx, "workflows_index", query, &redis.FTSearchOptions{
		NoContent: true, LimitOffset: offset, Limit: limit,
	}).Result()
	if err != nil {
		log.Printf("Error searching workflow instances (%s): %v", query, err)
		httpError(w, 500, "Error searching workflow instances")
		return
	}

	ids := make([]string, 0, len(res.Docs))
	for _, doc := range res.Docs {
		ids = append(ids, strings.TrimPrefix(doc.ID, "workflow_instance:"))
	}
	writeJSON(w, map[string]interface{}{"ids": ids, "total": res.Total, "limit": limit, "offset": offset})
}

// instanceIDPattern is the shape of a workflow_instance_id. Anything else is
// rejected before touching Redis, so the endpoint can only ever read
// workflow_instance:* keys. (contract D7)
var instanceIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

// GetWorkflowInstance returns the full JSON document for one instance,
// addressed by workflow_instance_id.
func (e *Engine) GetWorkflowInstance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !requireParams(r, w, "workflow_instance_id") {
		return
	}
	id := r.URL.Query().Get("workflow_instance_id")
	if !instanceIDPattern.MatchString(id) {
		httpError(w, 400, "workflow_instance_id format Invalid. ")
		return
	}

	instance, ok := jsonFirstMatch[map[string]interface{}](ctx, e.RDB, instanceKey(id), "$")
	if !ok {
		httpError(w, 404, "Instance Not Found")
		return
	}
	writeJSON(w, instance)
}
