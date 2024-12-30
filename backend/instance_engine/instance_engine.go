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

// The function `DetectServiceFailureUrl` reads service data from a JSON file and returns the failure
// URL for a given service name.
func DetectServiceFailureUrl(service_name string) string {
	var servicesList []utils.Service
	data, _ := os.ReadFile("services.json")
	json.Unmarshal(data, &servicesList)

	for _, service := range servicesList {
		if service.ServiceName == service_name {
			return service.FailureUrl
		}
	}

	return ""
}

// The function `ReportFailure` marks a task instance as failed in Redis, retrieves relevant service
// information, and sends a failure report HTTP request to the corresponding service.
func ReportFailure(ctx context.Context, rdb *redis.Client, conn *pgx.Conn, key string, workflow_instance_id string, task_index string) {
	var to, from string
	var toList, val []string
	client := &http.Client{}
	var payload map[string]interface{}
	var valPayload []map[string]interface{}

	// Mark task instance state as "FAILED" in redis
	statePath := "$." + task_index + ".state"
	new_state, _ := json.Marshal("FAILED")
	rdb.JSONSet(ctx, key, statePath, new_state).Result()
	rdb.JSONSet(ctx, key, "$."+task_index+".failedAt", time.Now().Unix())

	checkWorkflowState(rdb, conn, key)

	// Send Failure Report HTTP Request to relevant "FROM" service
	taskInstanceFrom, _ := rdb.JSONGet(ctx, key, "$."+task_index+".from").Result()
	json.Unmarshal([]byte(taskInstanceFrom), &val)
	from = val[0]

	// Get name of relevant "TO" service
	taskInstanceTo, _ := rdb.JSONGet(ctx, key, "$."+task_index+".to").Result()
	json.Unmarshal([]byte(taskInstanceTo), &toList)
	to = toList[0]

	taskInstancePayload, _ := rdb.JSONGet(ctx, key, "$."+task_index+".payload").Result()
	json.Unmarshal([]byte(taskInstancePayload), &valPayload)
	payload = valPayload[0]

	log.Println("Reporting Failure to: " + from)

	jsonBody, _ := json.Marshal(payload)
	bodyReader := bytes.NewReader(jsonBody)
	req, _ := http.NewRequest(http.MethodPost, DetectServiceFailureUrl(from), bodyReader)

	req.Header.Set("Content-Type", "application/json")
	q := req.URL.Query()
	q.Add("service", to)
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to send request: %v", err)
		return
	}
	defer resp.Body.Close()

	log.Println("Response status: ", resp.Status)
}

// The function `CheckTaskState` checks the state of a task instance in a workflow and takes
// appropriate actions based on the state.
func CheckTaskState(ctx context.Context, rdb *redis.Client, conn *pgx.Conn, workflow_instance_id string, path string, task_index string, taskInstanceTo string, timeout time.Duration, startTime time.Time) bool {
	// Check if the state property of the task instance is still "published"
	key := "workflow_instance:" + workflow_instance_id
	var value, workflowValue []string
	var taskInstanceState, workflowInstanceState string

	taskInstanceStateResult, err := rdb.JSONGet(ctx, key, path).Result()
	if err != nil {
		log.Printf("Error retrieving task: %v\n", err)
		return true
	} else {
		json.Unmarshal([]byte(taskInstanceStateResult), &value)
		taskInstanceState = value[0]
	}

	workflowInstanceStateResult, _ := rdb.JSONGet(ctx, key, "$.state").Result()
	json.Unmarshal([]byte(workflowInstanceStateResult), &workflowValue)
	workflowInstanceState = workflowValue[0]

	if taskInstanceState == "COMPLETED" {
		log.Println("Task COMPLETED: " + taskInstanceTo)
		if workflowInstanceState != "COMPLETED" {
			checkWorkflowState(rdb, conn, key)
		}
		return true

	} else if taskInstanceState == "FAILED" {
		log.Println("Task FAILED: " + taskInstanceTo)
		return true
	}

	elapsed := time.Since(startTime)
	if elapsed >= timeout {
		log.Println("Timeout for " + taskInstanceTo)
		ReportFailure(ctx, rdb, conn, key, workflow_instance_id, task_index)
		return true
	}

	return false
}

// The function `MonitorAfterTaskPublish` monitors the state of a task instance in a workflow until
// completion or timeout.
func MonitorAfterTaskPublish(ctx context.Context, rdb *redis.Client, conn *pgx.Conn, workflow_instance_id string, path string, task_index string, taskInstanceTimeout time.Duration) {
	ticker := time.NewTicker(taskInstanceTimeout)
	defer ticker.Stop()
	timeout := taskInstanceTimeout
	startTime := time.Now()

	var toList []string
	taskInstanceToResult, _ := rdb.JSONGet(ctx, "workflow_instance:"+workflow_instance_id, "$."+task_index+".to").Result()
	json.Unmarshal([]byte(taskInstanceToResult), &toList)
	var taskInstanceTo = toList[0]

	for {
		select {
		case <-ticker.C:
			log.Println("Monitoring State for " + taskInstanceTo + "...")
			if CheckTaskState(ctx, rdb, conn, workflow_instance_id, path, task_index, taskInstanceTo, timeout, startTime) {
				return
			}

		case <-ctx.Done():
			return
		}
	}
}

var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890")

// The Generate_ID function generates a random string of 20 characters using a predefined set of
// letters.
func Generate_ID() string {
	b := make([]rune, 20)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// The Start_instance function creates a new workflow instance based on provided parameters and
// template data stored in Redis.
func Start_instance(r *http.Request, w http.ResponseWriter, rdb *redis.Client) {
	// Get workflow_name + service name
	workflow_name := r.URL.Query().Get("workflow_name")
	workflow_version := r.URL.Query().Get("workflow_version")

	errMsg := ""
	isMissing := false
	if workflow_name == "" {
		errMsg = errMsg + "workflow_name required. "
		isMissing = true
	}
	if workflow_version == "" {
		errMsg = errMsg + "workflow_version required. "
		isMissing = true
	}

	if isMissing {
		log.Println("Error: ", errMsg)
		w.WriteHeader(400)
		fmt.Fprint(w, errMsg)
		return

	} else {

		// Find workflow for name
		data, err := rdb.JSONGet(ctx, "workflow_template:"+workflow_name, "$").Result()
		if err == redis.Nil || data == "" {
			log.Println("workflow_name does not exist")
			w.WriteHeader(400)
			fmt.Fprint(w, "workflow_name does not exist")
			return
		}

		var workflowArray []utils.Workflow
		err = json.Unmarshal([]byte(data), &workflowArray)
		if err != nil {
			errMsg := "Error unmarshalling JSON:" + err.Error()
			fmt.Println(errMsg)
			w.WriteHeader(500)
			fmt.Fprint(w, errMsg)
			return
		}
		workflow := workflowArray[0]

		if workflow.Version != workflow_version {
			log.Println("Invalid workflow_version " + workflow_version)
			w.WriteHeader(400)
			fmt.Fprint(w, "Invalid workflow_version "+workflow_version)
			return
		}

		// Create Tasks and Workflow instances using template
		workflow_instance := map[string]interface{}{
			"name":           workflow_name,
			"version":        workflow.Version,
			"schema_version": workflow.Schema_version,
			"state":          "PENDING",
			"startedAt":      time.Now().Unix(),
		}

		for i, task := range workflow.Tasks {
			index := strconv.Itoa(i)
			// Create a map for each task
			taskMap := map[string]interface{}{
				"topic":       task.Topic,
				"from":        task.From,
				"to":          task.To,
				"state":       "PENDING",
				"timeout":     task.Timeout,
				"fail_reason": "null",
				"index":       index,
			}

			workflow_instance[index] = taskMap
		}

		workflow_instance_id := Generate_ID()
		resp := map[string]interface{}{
			"workflow_instance_id": workflow_instance_id,
		}

		workflow_err := rdb.JSONSet(ctx, "workflow_instance:"+workflow_instance_id, ".", workflow_instance).Err()
		if workflow_err != nil {
			log.Println("Error: ", workflow_err)
			w.WriteHeader(500)
			fmt.Fprint(w, "Error: "+workflow_err.Error())
		}

		// fmt.Fprint(w, "Instance Started Successfully")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(resp)
	}
}

// The function `BackupCompletedWorkflows` inserts workflow data into a PostgreSQL database and deletes
// the corresponding JSON data from a Redis instance.
func BackupCompletedWorkflows(ctx context.Context, rdb *redis.Client, conn *pgx.Conn, key string, name string, startedAt string, completedAt string, document string) {
	instance_id := strings.Split(key, ":")[1]

	query := `INSERT INTO instance_history ("id", "name", "startedAt", "completedAt", "instance_data") VALUES ('` + instance_id + `', '` + name + `', TO_TIMESTAMP(` + startedAt + `), TO_TIMESTAMP(` + completedAt + `), '` + document + `')`
	conn.Exec(ctx, query)
	// conn.Close(ctx)

	// rdb.JSONDel(ctx, key, "$")
}

// The function `checkWorkflowState` retrieves and processes task states within a workflow instance,
// updating the workflow state to "COMPLETED" if all tasks are completed.
func checkWorkflowState(rdb *redis.Client, conn *pgx.Conn, key string) {
	// Get states of all task instances inside that workflow instance
	workflow_instance_doc, _ := rdb.JSONGet(ctx, key, "$").Result()

	var docs []map[string]interface{}
	err := json.Unmarshal([]byte(workflow_instance_doc), &docs)
	if err != nil {
		log.Printf("Failed to parse JSON document: %v", err)
	}

	var isWorkflowCompleteFlag = false
	var name, startedAt string
	var startedAtFloat float64

	// Iterate over the map and log the properties of each object
	for key, value := range docs[0] {
		// Check if the key follows the "task_" prefix pattern
		_, err := strconv.Atoi(key)
		if err == nil {
			// Assert that the value is a map
			task, _ := value.(map[string]interface{})

			// Accessing properties within each task
			state, stateOk := task["state"].(string)
			/* from, fromOk := task["from"].(string)
			to, toOk := task["to"].(string)
			topic, topicOk := task["topic"].(string)
			failReason, failReasonOk := task["fail_reason"].(string) */

			if stateOk {
				if state == "COMPLETED" {
					isWorkflowCompleteFlag = true
					continue
				} else {
					isWorkflowCompleteFlag = false
					break
				}
			} else {
				log.Println("Unexpected task structure")
			}
		} else if strings.Compare(key, "name") == 0 {
			name = value.(string)
		} else if strings.Compare(key, "startedAt") == 0 {
			startedAtFloat = value.(float64)
			startedAt = strconv.FormatFloat(startedAtFloat, 'f', -1, 64)
		}
		/* else {
		Handle non-task properties if needed
		log.Printf("Non-task property - %s: %v", key, value)
		} */
	}

	if isWorkflowCompleteFlag {
		log.Println("Workflow COMPLETE...")

		new_state, _ := json.Marshal("COMPLETED")
		rdb.JSONSet(ctx, key, "$.state", new_state)

		completedAt, _ := json.Marshal(time.Now().Unix())
		rdb.JSONSet(ctx, key, "$.completedAt", completedAt)

		// Write new go-routine code here...
		go func() {
			workflow_instance_doc, _ = rdb.JSONGet(ctx, key, "$").Result()
			json.Unmarshal([]byte(workflow_instance_doc), &docs)
			byteData, _ := json.Marshal(docs[0])
			BackupCompletedWorkflows(ctx, rdb, conn, key, name, startedAt, string(completedAt), string(byteData))
		}()

	} /* else {
		log.Println("Workflow PENDING...")
	} */
}

// The function `Handle_consume_cases` processes task states based on conditions and updates the state
// accordingly in a Redis database.
func Handle_consume_cases(rdb *redis.Client, conn *pgx.Conn, key string, w http.ResponseWriter, event_name string, service_name string, isRetry bool) {
	// Calculate Task name depending on "to" & "topic" fields...
	var taskIndexesList []string
	taskIndexesResult, err := rdb.JSONGet(ctx, key, "$..[?(@.topic=='"+event_name+"' && @.to=='"+service_name+"')].index").Result()
	json.Unmarshal([]byte(taskIndexesResult), &taskIndexesList)

	if err == redis.Nil || err != nil || len(taskIndexesList) == 0 {
		log.Println("Task Not Found")
		return
	}
	task_index := taskIndexesList[0]

	path := "$." + task_index + ".state"
	var value []string
	taskInstanceStateResult, _ := rdb.JSONGet(ctx, key, path).Result()
	json.Unmarshal([]byte(taskInstanceStateResult), &value)
	taskInstanceState := value[0]

	new_state, _ := json.Marshal("COMPLETED")

	if taskInstanceState == "PENDING" && !isRetry {
		log.Println("Task NOT started Yet.")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Task NOT started Yet"))

	} else if taskInstanceState == "PUBLISHED" || isRetry {
		rdb.JSONSet(ctx, key, path, new_state).Result()
		rdb.JSONSet(ctx, key, "$."+task_index+".consumedAt", time.Now().Unix())
		fmt.Fprint(w, "Instance State Updated")
		if isRetry {
			checkWorkflowState(rdb, conn, key)
		}

	} else if taskInstanceState == "FAILED" && !isRetry {
		log.Println("Task Already FAILED.")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Task Already FAILED"))

	} else if taskInstanceState == "COMPLETED" && !isRetry {
		log.Println("Task Already COMPLETED.")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Task Already COMPLETED"))

	}
}

// The function `Handle_publish_cases` processes tasks based on a given event name, updating their
// state and handling retries if needed.
func Handle_publish_cases(rdb *redis.Client, conn *pgx.Conn, key string, w http.ResponseWriter, event_name string, isRetry bool, workflow_instance_id string, body io.ReadCloser) {
	// Calculate Consuming Task Names List based on "topic"
	var taskIndexesList []string
	taskIndexesResult, _ := rdb.JSONGet(ctx, key, "$..[?(@.topic=='"+event_name+"')].index").Result()
	json.Unmarshal([]byte(taskIndexesResult), &taskIndexesList)

	isAllOk := true

	bodyBytes, _ := io.ReadAll(body)
	body.Close()

	for _, task_index := range taskIndexesList {
		task_index := task_index
		payloadPath := "$." + task_index + ".payload"

		var reqPayload map[string]interface{}
		err := json.Unmarshal(bodyBytes, &reqPayload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		new_state, _ := json.Marshal("PUBLISHED")
		path := "$." + task_index + ".state"
		var value []string
		taskInstanceStateResult, _ := rdb.JSONGet(ctx, key, path).Result()
		json.Unmarshal([]byte(taskInstanceStateResult), &value)
		taskInstanceState := value[0]

		if taskInstanceState == "PENDING" || isRetry {
			rdb.JSONSet(ctx, key, "$."+task_index+".publishedAt", time.Now().Unix())
			rdb.JSONSet(ctx, key, path, new_state).Result()
			rdb.JSONSet(ctx, key, payloadPath, reqPayload).Err()

			var timeoutList []int
			taskInstanceTimeoutResult, _ := rdb.JSONGet(ctx, "workflow_instance:"+workflow_instance_id, "$."+task_index+".timeout").Result()
			json.Unmarshal([]byte(taskInstanceTimeoutResult), &timeoutList)
			var taskInstanceTimeout = timeoutList[0]

			ctx, cancel := context.WithTimeout(context.Background(), time.Duration((taskInstanceTimeout+1000)*1000000))
			go func() {
				defer cancel()
				MonitorAfterTaskPublish(ctx, rdb, conn, workflow_instance_id, path, task_index, time.Duration(taskInstanceTimeout*1000000))
			}()

		} else if taskInstanceState == "PUBLISHED" && !isRetry {
			log.Println("Task Already PUBLISHED.")
			isAllOk = false

		} else if taskInstanceState == "FAILED" && !isRetry {
			log.Println("Task Already FAILED.")
			isAllOk = false

		} else if taskInstanceState == "COMPLETED" && !isRetry {
			log.Println("Task Already COMPLETED.")
			isAllOk = false

		}
	}

	// @TODO: See a better response with count of updated and already failed and already completed
	if isAllOk {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Instance State Updated"))
	} else {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Task Already COMPLETED or FAILED"))
	}
}

// The function `Handle_fail_cases` checks the state of a task in a workflow and handles different
// failure cases accordingly.
func Handle_fail_cases(rdb *redis.Client, conn *pgx.Conn, key string, w http.ResponseWriter, event_name string, service_name string, isRetry bool, workflow_instance_id string) {
	// Calculate Task name depending on "to" & "topic" fields...
	var taskIndexesList []string
	taskIndexesResult, err := rdb.JSONGet(ctx, key, "$..[?(@.topic=='"+event_name+"' && @.to=='"+service_name+"')].index").Result()
	json.Unmarshal([]byte(taskIndexesResult), &taskIndexesList)

	if err == redis.Nil || err != nil || len(taskIndexesList) == 0 {
		log.Println("Task Not Found")
		return
	}

	task_index := taskIndexesList[0]

	path := "$." + task_index + ".state"
	var value []string
	taskInstanceStateResult, _ := rdb.JSONGet(ctx, key, path).Result()
	json.Unmarshal([]byte(taskInstanceStateResult), &value)
	taskInstanceState := value[0]

	if taskInstanceState == "PENDING" && !isRetry {
		log.Println("Task NOT started Yet.")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Task NOT started Yet"))

	} else if taskInstanceState == "COMPLETED" && !isRetry {
		log.Println("Task Already COMPLETED.")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Task Already COMPLETED"))

	} else if taskInstanceState == "PUBLISHED" || isRetry {
		ReportFailure(ctx, rdb, conn, key, workflow_instance_id, task_index)
		fmt.Fprint(w, "Instance State Updated")

	} else if taskInstanceState == "FAILED" && !isRetry {
		log.Println("Task Already FAILED.")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Task Already FAILED"))

	}
}

// The function `Match_workflow_version` checks if a given workflow version matches the version stored
// in Redis and returns a success message or an error message accordingly.
func Match_workflow_version(rdb *redis.Client, key string, workflow_version string) (bool, string) {
	var value []string
	var workflow_version_json string

	workflow_version_json_result, err := rdb.JSONGet(ctx, key, "$.version").Result()

	if err == redis.Nil || err != nil || workflow_version_json_result == "" {

		log.Println("workflow_instance Not Found")
		return false, "workflow_instance Not Found"
	} else {

		err := json.Unmarshal([]byte(workflow_version_json_result), &value)
		if err != nil {
			log.Println("Error Unmarshalling JSON")
			return false, "Error Unmarshalling JSON"
		}
		if len(value) == 0 {
			log.Println("Workflow Instance Not Found")
			return false, "Workflow Instance Not Found"
		} else if len(value) > 0 {
			workflow_version_json = value[0]
			return true, "Success"
		}
	}

	if workflow_version != workflow_version_json {
		log.Println("Invalid worklfow version ", workflow_version)
		return false, "Invalid worklfow version " + workflow_version
	}

	return false, "Error"
}

// The function `Update_instance` processes requests to update workflow instances based on specified
// parameters and actions.
func Update_instance(r *http.Request, w http.ResponseWriter, rdb *redis.Client, conn *pgx.Conn) {
	action_type := r.URL.Query().Get("action_type")
	workflow_instance_id := r.URL.Query().Get("workflow_instance_id")
	event_name := r.URL.Query().Get("event_name")
	workflow_version := r.URL.Query().Get("workflow_version")
	is_retry := r.URL.Query().Get("is_retry")

	errMsg := ""
	isMissing := false
	if action_type == "" {
		errMsg = errMsg + "action_type required. "
		isMissing = true
	}
	if action_type != "consume" && action_type != "publish" && action_type != "fail" {
		errMsg = errMsg + "Invalid action_type value. "
		isMissing = true
	}
	if workflow_instance_id == "" {
		errMsg = errMsg + "workflow_instance_id required. "
		isMissing = true
	}
	if workflow_version == "" {
		errMsg = errMsg + "workflow_version required. "
		isMissing = true
	}
	if event_name == "" {
		errMsg = errMsg + "event_name required. "
		isMissing = true
	}
	if is_retry == "" {
		errMsg = errMsg + "is_retry required. "
		isMissing = true
	}

	if isMissing {
		w.WriteHeader(400)
		fmt.Fprint(w, errMsg)
		return

	} else {
		isRetry, _ := strconv.ParseBool(is_retry)
		key := "workflow_instance:" + workflow_instance_id

		status, errMsg := Match_workflow_version(rdb, key, workflow_version)
		if !status {

			w.WriteHeader(400)
			fmt.Fprint(w, errMsg)
			return
		} else {

			if action_type == "consume" {
				service_name := r.URL.Query().Get("service_name")

				errMsg := ""
				isMissing := false
				if service_name == "" {
					errMsg = errMsg + "service_name required. "
					isMissing = true
				}
				if isMissing {
					w.WriteHeader(400)
					fmt.Fprint(w, errMsg)
					return
				}

				Handle_consume_cases(rdb, conn, key, w, event_name, service_name, isRetry)
				return

			} else if action_type == "publish" {
				Handle_publish_cases(rdb, conn, key, w, event_name, isRetry, workflow_instance_id, r.Body)
				return

			} else if action_type == "fail" {
				service_name := r.URL.Query().Get("service_name")

				errMsg := ""
				isMissing := false
				if service_name == "" {
					errMsg = errMsg + "service_name required. "
					isMissing = true
				}
				if isMissing {
					w.WriteHeader(400)
					fmt.Fprint(w, errMsg)
					return
				}

				Handle_fail_cases(rdb, conn, key, w, event_name, service_name, isRetry, workflow_instance_id)
				return
			}
		}
	}
}

// The function `List_workflows` retrieves workflow names from a Redis database and returns them as a
// JSON-encoded list.
func List_workflows(r *http.Request, w http.ResponseWriter, rdb *redis.Client) {
	result, _ := rdb.Do(ctx, "FT.SEARCH", "workflow_templates_index", "*", "RETURN", "1", "workflow_name").Result()

	resultMap, ok := result.(map[interface{}]interface{})
	if !ok {
		log.Printf("Unexpected result format: %T", result)
	}

	// Convert results to slice of maps
	resultsInterface, ok := resultMap[interface{}("results")].([]interface{})
	if !ok {
		log.Printf("Unexpected results format: %T", resultMap[interface{}("results")])
	}

	var workflow_list []string

	if len(resultsInterface) == 0 {
		log.Println("No Workflows Found...!")
		w.WriteHeader(404)
		fmt.Fprintf(w, "No Workflows Found")

	} else {

		// Iterate over the results
		for _, item := range resultsInterface {
			itemMap, ok := item.(map[interface{}]interface{})
			if !ok {
				log.Printf("Unexpected item format: %T", item)
			}

			extraAttributes, ok := itemMap[interface{}("extra_attributes")].(map[interface{}]interface{})
			if !ok {
				log.Printf("Unexpected extra_attributes format: %T", itemMap[interface{}("extra_attributes")])
			}

			workflowName, ok := extraAttributes[interface{}("workflow_name")].(string)
			if !ok {
				log.Printf("workflow_name not found or not a string")
			}

			workflow_list = append(workflow_list, workflowName)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(workflow_list)
	}
}

// The function `List_workflow_instances` retrieves workflow instances based on specified filters and
// returns their IDs in JSON format.
func List_workflow_instances(r *http.Request, w http.ResponseWriter, client rueidis.Client) {

	workflow_name := r.URL.Query().Get("workflow_name")
	workflow_state := r.URL.Query().Get("workflow_state")
	started_at := r.URL.Query().Get("started_at")
	completed_at := r.URL.Query().Get("completed_at")
	failed_at := r.URL.Query().Get("failed_at")
	topic := r.URL.Query().Get("topic")
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	isMultiFilter := false
	var query, startedTimeCondition, completedTimeCondition, failedTimeCondition string

	now := strconv.FormatInt(time.Now().Unix(), 10)
	before5m := strconv.FormatInt(time.Now().Add(-time.Minute*5).Unix(), 10)
	before15m := strconv.FormatInt(time.Now().Add(-time.Minute*15).Unix(), 10)

	if workflow_name == "" && workflow_state == "" && started_at == "" && completed_at == "" && failed_at == "" && topic == "" && from == "" && to == "" {
		query = "*"
	} else if workflow_name != "" && workflow_state != "" && started_at != "" && completed_at != "" && failed_at != "" && topic != "" && from != "" && to != "" {
		// Started At
		if started_at == "5m" {
			startedTimeCondition = "@started_at:[" + before5m + " " + now + "]"
		} else if started_at == "15m" {
			startedTimeCondition = "@started_at:[" + before15m + " " + now + "]"
		}

		// Completed At
		if completed_at == "5m" {
			completedTimeCondition = "@completed_at:[" + before5m + " " + now + "]"
		} else if completed_at == "15m" {
			completedTimeCondition = "@completed_at:[" + before15m + " " + now + "]"
		}

		// Completed At
		if failed_at == "5m" {
			failedTimeCondition = "@failed_at:[" + before5m + " " + now + "]"
		} else if failed_at == "15m" {
			failedTimeCondition = "@failed_at:[" + before15m + " " + now + "]"
		}

		query = "@workflow_name:" + workflow_name + " && @workflow_state:" + workflow_state +
			" && @topic:" + topic + " && @from:" + from + " && @to:" + to +
			" && " + startedTimeCondition + " && " + completedTimeCondition + " && " + failedTimeCondition
		isMultiFilter = true
	} else {
		if workflow_name != "" {
			if !isMultiFilter {
				query = "@workflow_name:" + workflow_name
				isMultiFilter = true
			} else {
				query = query + " && @workflow_name:" + workflow_name
			}
		}
		if workflow_state != "" {
			if !isMultiFilter {
				query = "@workflow_state:" + workflow_state
				isMultiFilter = true
			} else {
				query = query + " && @workflow_state:" + workflow_state
			}
		}
		if started_at != "" {
			if started_at == "5m" {
				startedTimeCondition = "@started_at:[" + before5m + " " + now + "]"
			} else if started_at == "15m" {
				startedTimeCondition = "@started_at:[" + before15m + " " + now + "]"
			}

			if !isMultiFilter {
				query = startedTimeCondition
				isMultiFilter = true
			} else {
				query = query + " && " + startedTimeCondition
			}
		}
		if completed_at != "" {
			if completed_at == "5m" {
				completedTimeCondition = "@completed_at:[" + before5m + " " + now + "]"
			} else if completed_at == "15m" {
				completedTimeCondition = "@completed_at:[" + before15m + " " + now + "]"
			}

			if !isMultiFilter {
				query = completedTimeCondition
				isMultiFilter = true
			} else {
				query = query + " && " + completedTimeCondition
			}
		}
		if failed_at != "" {
			if failed_at == "5m" {
				failedTimeCondition = "@failed_at:[" + before5m + " " + now + "]"
			} else if failed_at == "15m" {
				failedTimeCondition = "@failed_at:[" + before15m + " " + now + "]"
			}

			if !isMultiFilter {
				query = failedTimeCondition
				isMultiFilter = true
			} else {
				query = query + " && " + failedTimeCondition
			}
		}
		if topic != "" {
			if !isMultiFilter {
				query = "@topic:" + topic
				isMultiFilter = true
			} else {
				query = query + " && @topic:" + topic
			}
		}
		if from != "" {
			if !isMultiFilter {
				query = "@from:" + from
				isMultiFilter = true
			} else {
				query = query + " && @from:" + from
			}
		}
		if to != "" {
			if !isMultiFilter {
				query = "@to:" + to
				isMultiFilter = true
			} else {
				query = query + " && @to:" + to
			}
		}
	}

	cmd := client.B().FtSearch().Index("workflows_index").Query(query).Build()
	n, resp, _ := client.Do(ctx, cmd).AsFtSearch()

	if n == 0 {
		log.Println("No Instances Found...!")
		w.WriteHeader(404)
		fmt.Fprintf(w, "No Instances Found")
	} else {
		var workflowIDs []string
		for _, doc := range resp {
			workflowIDs = append(workflowIDs, doc.Key)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(workflowIDs)
	}
}

// The function `Get_workflow_instance` retrieves a workflow instance from Redis based on a document
// key provided in the request URL and returns it as JSON if found.
func Get_workflow_instance(r *http.Request, w http.ResponseWriter, rdb *redis.Client) {
	doc_key := r.URL.Query().Get("doc_key")

	errMsg := ""
	isMissing := false
	if doc_key == "" {
		errMsg = errMsg + "doc_key required. "
		isMissing = true
	}
	if len(strings.Split(doc_key, ":")) == 1 {
		errMsg = errMsg + "doc_key format Invalid. "
		isMissing = true
	}
	if isMissing {
		w.WriteHeader(400)
		fmt.Fprint(w, errMsg)
		return
	}

	result, _ := rdb.JSONGet(ctx, doc_key, "$").Expanded()

	// Assert result as a slice
	resultSlice, ok := result.([]interface{})
	if !ok {
		log.Printf("Expected result to be a slice, got %T", result)
	}

	if len(resultSlice) == 0 {
		log.Println("Instance Not Found...!!")
		w.WriteHeader(404)
		fmt.Fprintf(w, "Instance Not Found")

	} else {
		// Access the first element and assert as a map
		instance, ok := resultSlice[0].(map[string]interface{})
		if !ok {
			log.Printf("Expected first element to be a map, got %T", resultSlice[0])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(instance)
	}
}
