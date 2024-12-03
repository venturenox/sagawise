package utils

// The `Workflow` type represents a workflow with a name, version, schema version, and a list of tasks.
// @property {string} Name - The `Name` property in the `Workflow` struct represents the name of the
// workflow.
// @property {string} Version - The `Version` property in the `Workflow` struct represents the version
// of the workflow. It is a string type field and is tagged with `json:"version"` for JSON marshaling
// and unmarshaling purposes.
// @property {string} Schema_version - The `Schema_version` property in the `Workflow` struct
// represents the version of the schema used for defining the workflow structure. It helps in ensuring
// compatibility and consistency when working with different versions of workflows.
// @property {[]Task} Tasks - Tasks is a slice of Task structs representing the individual tasks that
// make up the workflow. Each Task struct contains information about a specific task within the
// workflow, such as its name, type, input parameters, and other relevant details.
type Workflow struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	Schema_version string `json:"schema_version"`
	Tasks          []Task `json:"tasks"`
}

// The type WorkflowData contains a field named Workflow of type Workflow.
// @property {Workflow} Workflow - The `WorkflowData` struct contains a single field named `Workflow`
// of type `Workflow`. The `json:"workflow"` tag indicates that when this struct is marshaled to JSON,
// the field should be represented as "workflow".
type WorkflowData struct {
	Workflow Workflow `json:"workflow"`
}

// The Task struct defines properties related to a task, including its name, dependencies, topic,
// source, destination, and timeout.
// @property {string} Name - Name is a string field that represents the name of the task. It is tagged
// with `json:"name"` for JSON marshaling and unmarshaling.
// @property {string} DependsOn - The `DependsOn` property in the `Task` struct represents the task
// that the current task depends on. This means that the current task cannot start until the task
// specified in the `DependsOn` property has been completed. It establishes a dependency relationship
// between tasks, ensuring that they are executed
// @property {string} Topic - The `Topic` property in the `Task` struct represents the topic or
// category of the task. It is used to categorize tasks based on their subject or purpose.
// @property {string} From - The `From` property in the `Task` struct represents the starting point or
// origin of the task. It could be a location, a system, a person, or any other entity from which the
// task initiates or starts.
// @property {string} To - The `To` property in the `Task` struct represents the destination of the
// task. It could be a location, an endpoint, or any other target where the task needs to be completed
// or delivered.
// @property {int} Timeout - The `Timeout` property in the `Task` struct represents the maximum amount
// of time, in seconds, that the task is allowed to run before it is considered to have timed out. This
// property is an integer type and is specified in the JSON representation of the `Task` struct with
// the key `"
type Task struct {
	Name      string `json:"name"`
	DependsOn string `json:"depends_on"`
	Topic     string `json:"topic"`
	From      string `json:"from"`
	To        string `json:"to"`
	Timeout   int    `json:"timeout"`
}

// The type `Service` has two fields, `ServiceName` and `FailureUrl`, with corresponding JSON tags.
// @property {string} ServiceName - ServiceName is a property of the Service struct in Go programming
// language. It is a string field that represents the name of a service. The `json:"service_name"` tag
// is used to specify the key name when marshaling the struct to JSON format.
// @property {string} FailureUrl - The `FailureUrl` property in the `Service` struct represents the URL
// that should be redirected to in case of a failure related to the service.
type Service struct {
	ServiceName string `json:"service_name"`
	FailureUrl  string `json:"failure_url"`
}
