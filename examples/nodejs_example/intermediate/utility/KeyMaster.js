exports.EventKey = Object.freeze({
	TENANT_CREATED: "tenant_created",
	TENANT_UPDATED: "tenant_updated",
	TENANT_DELETED: "tenant_deleted",
	TENANT_PROFILE_CREATED: "tenant_profile_created",
	TENANT_PROFILE_UPDATED: "tenant_profile_updated",

	TENANT_CLIENT_CREATED: "tenant_clients_created",

	USER_CREATED: "user_created",
	USER_CREATED_SAGA: "user_created_saga",
	USER_CREATED_SAGA_FINAL: "user_created_saga_final",
	USER_ADDED: "user_added",
	USER_UPDATED: "user_updated",
	USER_DELETED: "user_deleted",
	RESET_PASSWORD: "reset_password",
	USER_PROFILE_CREATED: "user_profile_created",
	USER_PROFILE_UPDATED: "user_profile_updated",
	EMAIL_ACTIVATION: "email_activation_requested",

	CANDIDATE_CREATED: "candidate_created",
	CANDIDATE_INVITED: "candidate_invited",

	ASSESSMENT_REMINDER_CREATED: "assessment_reminder_created",

	ASSESS_SPEC_CREATED: "assess_spec_created",
	ASSESS_SPEC_STATUS_UPDATED: "assess_spec_status_updated",
	ASSESS_STATUS_UPDATED: "assess_status_updated",

	TEAM_MEMBER_INVITED: "team_member_invited",
	TEAM_MEMBER_DELETED: "team_member_deleted",

	CANDIDATE_REFERRER_CREATED: "candidate_referrer_created",

	ALERT_THRESHOLD_REACHED: "alert_threshold_reached",

	FREE_ASSESSMENTS_CONSUMED: 'free_assessments_consumed',
	BILLING_THRESHOLD_REACHED: 'billing_threshold_reached',
	AUTOMATIC_CHARGE_FAILED: 'automatic_charge_failed',
	CHARGE_RETRIES_FAILED: 'charge_retries_failed',
	TRIAL_PERIOD_UPDATED: 'trial_period_updated',
	COUPON_APPLIED: 'coupon_applied',
	FREE_ASSESSMENT_COUNT_UPDATED: 'free_assessment_count_updated',
});

exports.StatusCode = Object.freeze({
	/**
	 * 200 | The request was successfully completed.
	 */
	SUCCESS: 200,

	/**
	 * 201 | A new resource was successfully created.
	 */
	CREATED: 201,

	/**
	 * 204 | The server successfully processed the request, but is not returning any content.
	 */
	NO_CONTENT: 204,

	/**
	 * 304 | Used for conditional GET calls to reduce band-width usage.
	 * If used, must set the Date, Content-Location, ETag headers to what they would have been on a regular GET call.
	 */
	NOT_MODIFIED: 304,

	/**
	 * 400 | The request was invalid.
	 */
	BAD_REQUEST: 400,

	/**
	 * 401 | The request did not include an authentication token or the authentication token was expired.
	 */
	UNAUTHORIZED: 401,

	/**
	 * 403 | The client did not have permission to access the requested resource.
	 */
	FORBIDDEN: 403,

	/**
	 * 404 | The requested resource was not found.
	 */
	NOT_FOUND: 404,

	/**
	 * The HTTP method in the request was not supported by the resource. For example, the DELETE method cannot be used with the Agent API.
	 */
	METHOD_NOT_ALLOWED: 405,

	/**
	 * 409 | The request could not be completed due to a conflict. For example,
	 * POST ContentStore Folder API cannot complete if the given file or folder name already exists in the parent location.
	 */
	CONFLICT: 409,

	/**
	 * 500 | The request was not completed due to an internal error on the server side.
	 */
	INTERNAL_SERVER_ERROR: 500,

	/**
	 * 503 | The server was unavailable.
	 */
	SERVICE_UNAVAILABLE: 503,
});
