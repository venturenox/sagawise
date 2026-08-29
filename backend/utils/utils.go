package utils

// Workflow is a saga definition: an ordered list of tasks plus the versions used to
// validate reports against it.
type Workflow struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	Schema_version string `json:"schema_version"`
	Tasks          []Task `json:"tasks"`
}

// WorkflowData is the top-level shape of a DSL file, which wraps the workflow in a
// single "workflow" key.
type WorkflowData struct {
	Workflow Workflow `json:"workflow"`
}

// Task is one hop of a saga: service From publishes Topic, service To is expected to
// consume it within Timeout milliseconds. Tasks are identified by their index in the
// workflow's Tasks slice.
type Task struct {
	Topic   string `json:"topic"`
	From    string `json:"from"`
	To      string `json:"to"`
	Timeout int    `json:"timeout"`
}

// Service maps a service name to the webhook Sagawise POSTs to when one of that
// service's published tasks fails, so it can compensate.
type Service struct {
	ServiceName string `json:"service_name"`
	FailureUrl  string `json:"failure_url"`
}
