import requests
import os

class Sagawise:
    def __init__(self, timeout=1000):
        self.base_url = os.getenv('SAGAWISE_URL')
        self.timeout = timeout
        self.session = requests.Session()
        self.session.headers.update({'Content-Type': 'application/json'})

    def start_workflow(self, workflow_name: str, workflow_version: str):
        """
        The `start_workflow` function sends an HTTP POST request to start a workflow instance in the
        Sagawise app and returns the workflow instance ID if successful.
        
        :param workflow_name: The `workflow_name` parameter is a string that represents the name of the
        workflow that you want to start. It is used to identify the specific workflow that you want to
        initiate in the system
        :type workflow_name: str
        :param workflow_version: The `workflow_version` parameter in the `start_workflow` function is a
        string that represents the version of the workflow that you want to start. It is used to specify
        which version of the workflow should be initiated when calling the `start_workflow` method. Make
        sure to provide the correct version number when
        :type workflow_version: str
        :return: The `start_workflow` method returns the `workflow_instance_id` from the JSON response
        if the HTTP request is successful. If there is an error during the HTTP request (e.g., network
        issue, server error), it will catch the `requests.exceptions.RequestException`, print the error
        message, and return the error itself.
        """
        try:
            if not workflow_name or not workflow_version:
                raise ValueError('workflow_name and workflow_version are required.')

            # Send HTTP request to Sagawise app
            response = self.session.post(
                url=f'{self.base_url}/start_instance',
                params={
                    'workflow_name': workflow_name,
                    'workflow_version': workflow_version,
                },
                timeout=self.timeout
            )

            response.raise_for_status()
            return response.json().get('workflow_instance_id')

        except requests.exceptions.RequestException as error:
            print(f'Error: {error}')
            return error

    def publish_message(self, workflow_instance_id: str, workflow_version: str, event_name: str, is_retry=False, payload=None):
        """
        The function `publish_message` sends an HTTP POST request to update a workflow instance with
        specified parameters and payload.
        
        :param workflow_instance_id: The `workflow_instance_id` parameter is a unique identifier for a
        specific instance of a workflow. It helps identify and track the progress of a particular
        workflow instance within the system
        :type workflow_instance_id: str
        :param workflow_version: The `workflow_version` parameter in the `publish_message` method refers
        to the version of the workflow associated with the message being published. It is a string that
        identifies the specific version of the workflow that the message corresponds to. This version
        information is important for tracking and managing different iterations of workflows within the
        :type workflow_version: str
        :param event_name: The `event_name` parameter in the `publish_message` method refers to the name
        of the event being triggered or published. It is a string that identifies the specific event
        that is being processed within the workflow. This parameter helps in determining the action to
        be taken based on the event being published
        :type event_name: str
        :param is_retry: The `is_retry` parameter in the `publish_message` method is a boolean flag that
        indicates whether the message being published is a retry attempt. By default, its value is set
        to `False`, but it can be explicitly set to `True` when calling the method to indicate that the
        message is, defaults to False (optional)
        :param payload: The `payload` parameter in the `publish_message` method is used to send data
        along with the HTTP request to the Sagawise app. This data can be in the form of a JSON object
        and can contain any information that needs to be processed by the app in relation to the
        workflow instance identified
        :return: The code snippet provided returns the error message if an exception occurs during the
        HTTP request. If there is no error, it does not explicitly return anything, so the return value
        would be `None`.
        """
        try:
            if not workflow_instance_id or not workflow_version or not event_name or payload is None or payload == {}:
                raise ValueError('Required keys: workflow_instance_id, workflow_version, event_name, payload')

            # Send HTTP request to Sagawise app
            response = self.session.post(
                url=f'{self.base_url}/update_instance',
                params={
                    'workflow_instance_id': workflow_instance_id,
                    'workflow_version': workflow_version,
                    'event_name': event_name,
                    'action_type': 'publish',
                    'is_retry': is_retry
                },
                json=payload,
                timeout=self.timeout
            )

            response.raise_for_status()

        except requests.exceptions.RequestException as error:
            print(f'Error: {error}')
            return error

    def consume_message(self, workflow_instance_id: str, workflow_version: str, event_name: str, service_name: str, is_retry=False):
        """
        The function `consume_message` sends an HTTP POST request to update a workflow instance in a
        Sagawise app with specified parameters.
        
        :param workflow_instance_id: The `workflow_instance_id` parameter is a unique identifier for a
        specific instance of a workflow. It helps identify and track the progress of a particular
        workflow instance within the system
        :type workflow_instance_id: str
        :param workflow_version: The `workflow_version` parameter in the `consume_message` method
        represents the version of the workflow associated with the message being consumed. It is a
        string that helps identify the specific version of the workflow that the message is related to.
        This parameter is essential for tracking and managing different versions of workflows within the
        :type workflow_version: str
        :param event_name: The `event_name` parameter in the `consume_message` method represents the
        name of the event being processed or consumed by the workflow. It is a string that helps
        identify the specific event triggering the workflow action
        :type event_name: str
        :param service_name: The `service_name` parameter in the `consume_message` method refers to the
        name of the service that is consuming the message in the workflow. It is used to identify which
        service is processing the event within the workflow
        :type service_name: str
        :param is_retry: The `is_retry` parameter in the `consume_message` method is a boolean flag that
        indicates whether the message consumption is a retry attempt. By default, its value is set to
        `False`, but it can be explicitly set to `True` when calling the method to indicate that the
        message consumption is, defaults to False (optional)
        :return: The `consume_message` method returns the error message if an exception occurs during
        the HTTP request to the Sagawise app. If there is no error, it does not explicitly return
        anything, so it implicitly returns `None`.
        """
        try:
            if not workflow_instance_id or not workflow_version or not event_name or not service_name:
                raise ValueError('Required keys: workflow_instance_id, workflow_version, event_name, service_name')

            # Send HTTP request to Sagawise app
            response = self.session.post(
                url=f'{self.base_url}/update_instance',
                params={
                    'workflow_instance_id': workflow_instance_id,
                    'workflow_version': workflow_version,
                    'event_name': event_name,
                    'action_type': 'consume',
                    'service_name': service_name,
                    'is_retry': is_retry
                },
                timeout=self.timeout
            )

            response.raise_for_status()

        except requests.exceptions.RequestException as error:
            print(f'Error: {error}')
            return error

    def fail_message(self, workflow_instance_id: str, workflow_version: str, event_name: str, service_name: str, is_retry=False):
        """
        The `fail_message` function sends an HTTP request to update a workflow instance with failure
        information and handles any request exceptions.
        
        :param workflow_instance_id: The `workflow_instance_id` parameter is a unique identifier for a
        specific instance of a workflow. It helps identify and track the progress of a particular workflow
        instance within the system
        :type workflow_instance_id: str
        :param workflow_version: The `workflow_version` parameter in the `fail_message` method represents
        the version of the workflow associated with the `workflow_instance_id`. It is used to identify the
        specific version of the workflow that the instance belongs to. This information is crucial for
        tracking and managing different versions of workflows within the system
        :type workflow_version: str
        :param event_name: The `event_name` parameter in the `fail_message` method represents the name of
        the event associated with the workflow instance that has failed. It is a string value that helps
        identify the specific event that triggered the failure in the workflow
        :type event_name: str
        :param service_name: The `service_name` parameter in the `fail_message` method represents the name
        of the service that encountered a failure during the workflow execution. It is used to identify the
        specific service that failed when updating the workflow instance in the Sagawise app
        :type service_name: str
        :param is_retry: The `is_retry` parameter in the `fail_message` method is a boolean flag that
        indicates whether the current action is a retry attempt. It defaults to `False`, but can be set to
        `True` if the action being performed is a retry of a previous failed attempt, defaults to False
        (optional)
        :return: The function `fail_message` is returning the error message in case of a
        `requests.exceptions.RequestException` when sending the HTTP request to the Sagawise app.
        """
        try:
            if not workflow_instance_id or not workflow_version or not event_name or not service_name:
                raise ValueError('Required keys: workflow_instance_id, workflow_version, event_name, service_name')

            # Send HTTP request to Sagawise app
            response = self.session.post(
                url=f'{self.base_url}/update_instance',
                params={
                    'workflow_instance_id': workflow_instance_id,
                    'workflow_version': workflow_version,
                    'event_name': event_name,
                    'action_type': 'fail',
                    'service_name': service_name,
                    'is_retry': is_retry
                },
                timeout=self.timeout
            )

            response.raise_for_status()

        except requests.exceptions.RequestException as error:
            print(f'Error: {error}')
            return error
