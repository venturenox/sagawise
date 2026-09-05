package instance_engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"wtfsaga/utils"
	"wtfsaga/webhooksig"

	"github.com/redis/go-redis/v9"
)

// ---- HTTP helpers (contract §9: every response is JSON) ----

// codeStatus maps the contract's stable error codes to HTTP status codes.
var codeStatus = map[string]int{
	"MISSING_PARAM": http.StatusBadRequest,
	"INVALID_PARAM": http.StatusBadRequest,
	"INVALID_BODY":  http.StatusBadRequest,

	"WORKFLOW_NOT_FOUND": http.StatusNotFound,
	"INSTANCE_NOT_FOUND": http.StatusNotFound,
	"TASK_NOT_FOUND":     http.StatusNotFound,

	"TASK_NOT_PUBLISHED":     http.StatusConflict,
	"TASK_ALREADY_PUBLISHED": http.StatusConflict,
	"TASK_ALREADY_COMPLETED": http.StatusConflict,
	"TASK_ALREADY_FAILED":    http.StatusConflict,
	"INSTANCE_TERMINAL":      http.StatusConflict,

	"PAYLOAD_TOO_LARGE": http.StatusRequestEntityTooLarge,

	"INTERNAL": http.StatusInternalServerError,
}

// writeJSON writes v as the JSON response body with the given status. A
// failed write means the client went away; it is only logged.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("Error writing response: %v", err)
	}
}

// writeError writes {"error": code, "message": msg} with the status the code
// implies. Unknown codes are a 500 so a bug here is never a silent 200.
func writeError(w http.ResponseWriter, code, msg string) {
	status, ok := codeStatus[code]
	if !ok {
		status = http.StatusInternalServerError
	}
	log.Printf("%d %s: %s", status, code, msg)
	writeJSON(w, status, map[string]string{"error": code, "message": msg})
}

// internalError logs the underlying error and answers 500 INTERNAL. The
// client sees no detail: an infrastructure failure is not a business answer
// and is never phrased as one (D5). (#7)
func internalError(w http.ResponseWriter, what string, err error) {
	log.Printf("%s: %v", what, err)
	writeError(w, "INTERNAL", what+" failed; retry later")
}

// requireParams answers 400 MISSING_PARAM listing every absent query
// parameter and returns false if any is missing.
func requireParams(r *http.Request, w http.ResponseWriter, names ...string) bool {
	var missing []string
	for _, n := range names {
		if r.URL.Query().Get(n) == "" {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		writeError(w, "MISSING_PARAM", "missing query parameter(s): "+strings.Join(missing, ", "))
		return false
	}
	return true
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

// ---- reading documents ----

// errNotFound is returned by jsonGet when the key does not exist.
var errNotFound = errors.New("key not found")

// errBadSchema is returned for a document without a $.tasks array: a
// schema 1 document from before phase 6. There is no migration; a dev stack
// is upgraded with `make clean` (design note P4).
var errBadSchema = errors.New("document is not a schema 2 workflow instance (no $.tasks); upgrade requires make clean")

// jsonGet runs JSON.GET with one or more JSONPaths and decodes the reply
// into out. With one path the reply is an array of matches; with several it
// is an object keyed by path.
func (e *Engine) jsonGet(ctx context.Context, key string, out interface{}, paths ...string) error {
	raw, err := e.RDB.JSONGet(ctx, key, paths...).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return errNotFound
		}
		return err
	}
	if raw == "" {
		return errNotFound
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("decode %s %v: %w", key, paths, err)
	}
	return nil
}

// ---- instance lifecycle ----

// instanceTask is one element of $.tasks in a schema 2 document.
type instanceTask struct {
	Topic       string      `json:"topic"`
	From        string      `json:"from"`
	To          string      `json:"to"`
	Timeout     int         `json:"timeout"`
	State       string      `json:"state"`
	PublishedAt int64       `json:"publishedAt"`
	ConsumedAt  int64       `json:"consumedAt"`
	FailedAt    int64       `json:"failedAt"`
	Payload     interface{} `json:"payload,omitempty"`
}

// instanceDoc is a schema 2 workflow instance document.
type instanceDoc struct {
	Schema        int            `json:"schema"`
	Name          string         `json:"name"`
	Version       string         `json:"version"`
	SchemaVersion string         `json:"schema_version"`
	State         string         `json:"state"`
	StartedAt     int64          `json:"startedAt"`
	CompletedAt   int64          `json:"completedAt"`
	FailedAt      int64          `json:"failedAt"`
	Tasks         []instanceTask `json:"tasks"`
}

// StartInstance clones a workflow template into a new workflow_instance doc.
// The template is the single source of truth for the version; any
// workflow_version query parameter is ignored.
func (e *Engine) StartInstance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !requireParams(r, w, "workflow_name") {
		return
	}
	name := r.URL.Query().Get("workflow_name")

	var templates []utils.Workflow
	err := e.jsonGet(ctx, "workflow_template:"+name, &templates, "$")
	if errors.Is(err, errNotFound) || (err == nil && len(templates) == 0) {
		writeError(w, "WORKFLOW_NOT_FOUND", "workflow "+strconv.Quote(name)+" does not exist")
		return
	}
	if err != nil {
		internalError(w, "read workflow template", err)
		return
	}
	workflow := templates[0]

	doc := instanceDoc{
		Schema: 2, Name: name, Version: workflow.Version, SchemaVersion: workflow.Schema_version,
		State: "PENDING", StartedAt: e.Clock.Now().Unix(),
		Tasks: make([]instanceTask, 0, len(workflow.Tasks)),
	}
	for _, task := range workflow.Tasks {
		doc.Tasks = append(doc.Tasks, instanceTask{
			Topic: task.Topic, From: task.From, To: task.To, Timeout: task.Timeout, State: "PENDING",
		})
	}

	id := generateID()
	if err := e.RDB.JSONSet(ctx, instanceKey(id), "$", doc).Err(); err != nil {
		internalError(w, "create workflow instance", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"workflow_instance_id": id})
}

// updateResponse is the success body of /update_instance (contract §9).
// task_index and task_state are scalars for one task and arrays when a
// publish resolved several tasks sharing a topic.
type updateResponse struct {
	WorkflowInstanceID string      `json:"workflow_instance_id"`
	TaskIndex          interface{} `json:"task_index"`
	TaskState          interface{} `json:"task_state"`
	WorkflowState      string      `json:"workflow_state"`
	Idempotent         bool        `json:"idempotent"`
}

// UpdateInstance handles a publish/consume/fail report: parse, resolve the
// task(s) in Go, run one atomic transition, answer. (design note §2)
func (e *Engine) UpdateInstance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !requireParams(r, w, "action_type", "workflow_instance_id", "event_name", "is_retry") {
		return
	}
	q := r.URL.Query()
	action := q.Get("action_type")
	id := q.Get("workflow_instance_id")
	topic := q.Get("event_name")
	service := q.Get("service_name")
	isRetry, ok := parseRetry(q.Get("is_retry"))
	if !ok {
		writeError(w, "INVALID_PARAM", "is_retry must be true or false")
		return
	}
	if action != "consume" && action != "publish" && action != "fail" {
		writeError(w, "INVALID_PARAM", "action_type must be publish, consume or fail")
		return
	}
	if action != "publish" && !requireParams(r, w, "service_name") {
		return
	}

	payload := ""
	if action == "publish" {
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			// The body cap is installed by main (SAGAWISE_MAX_BODY_BYTES);
			// contract §9. The payload would be stored and replayed in the
			// failure webhook, so it is bounded. (phase 8)
			writeError(w, "PAYLOAD_TOO_LARGE", "publish body exceeds the limit of "+strconv.FormatInt(tooBig.Limit, 10)+" bytes")
			return
		}
		if err != nil {
			writeError(w, "INVALID_BODY", "could not read request body: "+err.Error())
			return
		}
		if !isJSONObject(body) {
			writeError(w, "INVALID_BODY", "publish body must be a JSON object")
			return
		}
		payload = string(body)
	}

	// Resolve: which task(s) does this report name? Plain string equality on
	// the document's own topic/to arrays; query values are data, never
	// query syntax, and payloads are never consulted. (T2; #12, #13)
	tasks, err := e.readTaskIdentity(ctx, id)
	if errors.Is(err, errNotFound) {
		writeError(w, "INSTANCE_NOT_FOUND", "workflow instance "+strconv.Quote(id)+" does not exist")
		return
	}
	if err != nil {
		internalError(w, "read workflow instance", err)
		return
	}
	var indexes []int
	for i, t := range tasks {
		if t.Topic == topic && (action == "publish" || t.To == service) {
			indexes = append(indexes, i)
		}
	}
	if len(indexes) == 0 {
		if action == "publish" {
			writeError(w, "TASK_NOT_FOUND", "no task with topic "+strconv.Quote(topic))
		} else {
			writeError(w, "TASK_NOT_FOUND", "no task with topic "+strconv.Quote(topic)+" consumed by "+strconv.Quote(service))
		}
		return
	}

	res, err := e.transition(ctx, id, action, isRetry, indexes, payload)
	if err != nil {
		internalError(w, "update workflow instance", err)
		return
	}

	switch res.Code {
	case "OK", "IDEMPOTENT":
	case "NOT_FOUND":
		writeError(w, "INSTANCE_NOT_FOUND", "workflow instance "+strconv.Quote(id)+" does not exist")
		return
	case "INSTANCE_TERMINAL":
		writeError(w, res.Code, fmt.Sprintf("instance %s is %s; it accepts no further reports", id, res.InstanceState))
		return
	default:
		writeError(w, res.Code, refusalMessage(res, tasks))
		return
	}

	// Side effects are already queued in Redis by the script; the nudges only
	// make the workers run now instead of at their next tick.
	if res.WebhookQueued {
		e.Webhooks.Nudge()
	}
	if res.TerminalNow {
		e.Archiver.Nudge()
	}

	resp := updateResponse{
		WorkflowInstanceID: id, WorkflowState: res.InstanceState, Idempotent: res.Code == "IDEMPOTENT",
	}
	if len(indexes) == 1 {
		resp.TaskIndex, resp.TaskState = indexes[0], res.TaskStates[0]
	} else {
		resp.TaskIndex, resp.TaskState = indexes, res.TaskStates
	}
	writeJSON(w, http.StatusOK, resp)
}

// isJSONObject reports whether b is a JSON document whose top level is an
// object: the only payload shape the failure webhook can carry. It must
// also be valid UTF-8: encoding/json tolerates stray bytes inside strings
// but RedisJSON refuses them, and that refusal is a client error, not a 500.
func isJSONObject(b []byte) bool {
	trimmed := bytes.TrimSpace(b)
	return len(trimmed) > 0 && trimmed[0] == '{' && utf8.Valid(trimmed) && json.Valid(trimmed)
}

// taskIdentity is what task resolution needs: the (topic, to) of each task.
type taskIdentity struct {
	Topic string
	To    string
}

// readTaskIdentity returns the (topic, to) of every task in the instance,
// in index order, without transferring payloads.
func (e *Engine) readTaskIdentity(ctx context.Context, id string) ([]taskIdentity, error) {
	var got map[string][]string
	if err := e.jsonGet(ctx, instanceKey(id), &got, "$.tasks[*].topic", "$.tasks[*].to"); err != nil {
		return nil, err
	}
	topics, tos := got["$.tasks[*].topic"], got["$.tasks[*].to"]
	if len(topics) == 0 || len(topics) != len(tos) {
		return nil, fmt.Errorf("%s: %w", instanceKey(id), errBadSchema)
	}
	out := make([]taskIdentity, len(topics))
	for i := range topics {
		out[i] = taskIdentity{Topic: topics[i], To: tos[i]}
	}
	return out, nil
}

// refusalMessage phrases a 409 the way contract §9 shows it.
func refusalMessage(res transitionResult, tasks []taskIdentity) string {
	i := res.RefusedIndex
	where := fmt.Sprintf("task %d", i)
	if i >= 0 && i < len(tasks) {
		where = fmt.Sprintf("task %d (%s → %s)", i, tasks[i].Topic, tasks[i].To)
	}
	switch res.Code {
	case "TASK_NOT_PUBLISHED":
		return where + " is PENDING; nothing has been published for it"
	case "TASK_ALREADY_PUBLISHED", "TASK_ALREADY_COMPLETED", "TASK_ALREADY_FAILED":
		return where + " is already " + strings.TrimPrefix(res.Code, "TASK_ALREADY_")
	}
	return where + ": " + res.Code
}

// ---- the atomic transition ----

// transitionResult is what transition.lua returns.
type transitionResult struct {
	Code          string
	TaskStates    []string // state of each target index after the call
	InstanceState string
	TerminalNow   bool // this call moved the instance to COMPLETED/FAILED
	WebhookQueued bool // a task became FAILED; webhook_pending has a job
	RefusedIndex  int  // the task a refusal is about, or -1
}

// transition runs one atomic state transition (contract T4). action is
// publish, consume, fail or reap. A returned error means the script did not
// run to completion and nothing changed; the caller maps it to a 500 or, in
// the reaper, leaves the deadline for the next tick.
func (e *Engine) transition(ctx context.Context, id, action string, retry bool, indexes []int, payload string) (transitionResult, error) {
	now := e.Clock.Now()
	retryArg := 0
	if retry {
		retryArg = 1
	}
	idx := make([]string, len(indexes))
	for i, n := range indexes {
		idx[i] = strconv.Itoa(n)
	}
	keys := []string{instanceKey(id), deadlinesKey, archiveQueueKey, webhookQueueKey}
	raw, err := e.script.Run(ctx, e.RDB, keys,
		action, retryArg, strings.Join(idx, ","), now.Unix(), now.UnixMilli(), payload, id, instanceKeyPrefix).Result()
	if err != nil {
		return transitionResult{}, fmt.Errorf("transition %s %s: %w", action, id, err)
	}

	parts, ok := raw.([]interface{})
	if !ok || len(parts) != 6 {
		return transitionResult{}, fmt.Errorf("transition %s %s: unexpected reply %#v", action, id, raw)
	}
	var res transitionResult
	res.Code, _ = parts[0].(string)
	statesJSON, _ := parts[1].(string)
	if err := json.Unmarshal([]byte(statesJSON), &res.TaskStates); err != nil {
		return transitionResult{}, fmt.Errorf("transition %s %s: task states %q: %w", action, id, statesJSON, err)
	}
	res.InstanceState, _ = parts[2].(string)
	terminal, _ := parts[3].(int64)
	webhook, _ := parts[4].(int64)
	refused, _ := parts[5].(int64)
	res.TerminalNow, res.WebhookQueued, res.RefusedIndex = terminal == 1, webhook == 1, int(refused)
	if res.Code == "BAD_SCHEMA" {
		return res, fmt.Errorf("%s: %w", instanceKey(id), errBadSchema)
	}
	return res, nil
}

// ---- queue jobs ----

// archiveJob inserts one terminal instance into instance_history. The
// document is immutable once terminal, so the row always equals the final
// Redis state (A1). ON CONFLICT DO NOTHING makes retries idempotent (A2).
func (e *Engine) archiveJob(ctx context.Context, id string) error {
	var docs []json.RawMessage
	err := e.jsonGet(ctx, instanceKey(id), &docs, "$")
	if errors.Is(err, errNotFound) || (err == nil && len(docs) == 0) {
		log.Printf("archive: instance %s no longer exists; nothing to archive", id)
		return nil
	}
	if err != nil {
		return err
	}
	var doc instanceDoc
	if err := json.Unmarshal(docs[0], &doc); err != nil {
		return fmt.Errorf("decode %s: %w", instanceKey(id), err)
	}
	if doc.State != "COMPLETED" && doc.State != "FAILED" {
		// Cannot happen: the script enqueues only on a terminal transition.
		// Refuse rather than archive a live instance; the job retries.
		return fmt.Errorf("instance %s is %s, not terminal", id, doc.State)
	}
	_, err = e.DB.Exec(ctx, `INSERT INTO instance_history ("id", "name", "startedAt", "completedAt", "instance_data")
		VALUES ($1, $2, TO_TIMESTAMP($3), TO_TIMESTAMP($4), $5)
		ON CONFLICT ("id") DO NOTHING`, id, doc.Name, doc.StartedAt, doc.CompletedAt, string(docs[0]))
	return err
}

// webhookJob POSTs a failed task's payload to its publisher's failure_url
// (contract §6). Any transport error or non-2xx answer is retried by the
// worker; the outcome never changes state (W4).
func (e *Engine) webhookJob(ctx context.Context, member string) error {
	id, index, ok := splitMember(member)
	if !ok {
		log.Printf("webhook: malformed job %q; dropping it", member)
		return nil
	}
	task := "$.tasks[" + strconv.Itoa(index) + "]"
	var got map[string][]json.RawMessage
	err := e.jsonGet(ctx, instanceKey(id), &got, task+".from", task+".to", task+".payload")
	if errors.Is(err, errNotFound) {
		log.Printf("webhook: instance %s no longer exists; nothing to deliver", id)
		return nil
	}
	if err != nil {
		return err
	}
	var from, to string
	if v := got[task+".from"]; len(v) == 1 {
		_ = json.Unmarshal(v[0], &from)
	}
	if v := got[task+".to"]; len(v) == 1 {
		_ = json.Unmarshal(v[0], &to)
	}
	if from == "" || to == "" {
		log.Printf("webhook: %s task %d has no from/to; dropping it", id, index)
		return nil
	}
	// A task that was never published has no payload (W2); report {}.
	payload := json.RawMessage(`{}`)
	if v := got[task+".payload"]; len(v) == 1 {
		payload = v[0]
	}

	target, err := e.Services.FailureURL(from)
	if err != nil {
		return fmt.Errorf("failure_url for %q: %w", from, err)
	}
	if target == "" {
		// Excluded by startup validation (W5); if it happens anyway there is
		// nowhere to deliver to, and retrying will not change that.
		log.Printf("webhook: no failure_url registered for %q; dropping %s", from, member)
		return nil
	}

	// #nosec G704 -- target is operator configuration (the ServiceRegistry, e.g. services.json)
	// looked up by a DSL-declared service name; it never comes from request input.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request for %s: %w", from, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.URL.RawQuery = url.Values{"service": {to}}.Encode()
	if len(e.WebhookSecret) > 0 {
		// W6: the receiver can check the delivery came from Sagawise and is
		// not a replay. The timestamp is the engine clock so tests are
		// deterministic; receivers compare it with their own clock.
		ts := e.Clock.Now().Unix()
		req.Header.Set(webhooksig.HeaderTimestamp, strconv.FormatInt(ts, 10))
		req.Header.Set(webhooksig.HeaderSignature, webhooksig.Sign(e.WebhookSecret, ts, payload))
	}
	resp, err := e.HTTPClient.Do(req) // #nosec G704 -- see above
	if err != nil {
		return fmt.Errorf("POST %s: %w", from, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s: %s", from, resp.Status)
	}
	log.Printf("webhook: delivered %s to %s (%s)", member, from, resp.Status)
	return nil
}

// ---- read endpoints ----

// ListWorkflows returns the names of all registered workflow templates.
//
// The limit is explicit: FT.SEARCH defaults to the first 10 documents, so
// without one this endpoint silently truncated the list at the eleventh
// workflow. Templates are operator-authored and few, so one page of
// listMaxLimit covers every realistic deployment; a deployment past that
// gets a logged warning rather than a quietly short answer. (phase 7)
func (e *Engine) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	res, err := e.RDB.FTSearchWithArgs(ctx, "workflow_templates_index", "*", &redis.FTSearchOptions{
		Return:      []redis.FTSearchReturn{{FieldName: "workflow_name"}},
		LimitOffset: 0, Limit: listMaxLimit,
	}).Result()
	if err != nil {
		internalError(w, "search workflow templates", err)
		return
	}
	if res.Total > listMaxLimit {
		log.Printf("ListWorkflows: %d templates registered, returning the first %d", res.Total, listMaxLimit)
	}

	names := make([]string, 0, len(res.Docs))
	for _, doc := range res.Docs {
		if name, ok := doc.Fields["workflow_name"]; ok {
			names = append(names, name)
		}
	}
	writeJSON(w, http.StatusOK, names)
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
		writeError(w, "INVALID_PARAM", "limit "+err.Error())
		return
	}
	if limit == 0 {
		limit = listDefaultLimit
	}
	offset, err := pageParam(q.Get("offset"), 0, 0)
	if err != nil {
		writeError(w, "INVALID_PARAM", "offset "+err.Error())
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
		internalError(w, "search workflow instances", fmt.Errorf("%s: %w", query, err))
		return
	}

	ids := make([]string, 0, len(res.Docs))
	for _, doc := range res.Docs {
		ids = append(ids, strings.TrimPrefix(doc.ID, "workflow_instance:"))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ids": ids, "total": res.Total, "limit": limit, "offset": offset})
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
		writeError(w, "INVALID_PARAM", "workflow_instance_id must be 1 to 64 letters or digits")
		return
	}

	var docs []json.RawMessage
	err := e.jsonGet(ctx, instanceKey(id), &docs, "$")
	if errors.Is(err, errNotFound) || (err == nil && len(docs) == 0) {
		writeError(w, "INSTANCE_NOT_FOUND", "workflow instance "+strconv.Quote(id)+" does not exist")
		return
	}
	if err != nil {
		internalError(w, "read workflow instance", err)
		return
	}
	writeJSON(w, http.StatusOK, docs[0])
}
