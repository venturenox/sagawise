import os

import requests

# Requests to Sagawise time out after this many seconds (the unit `requests`
# uses). The previous default of 1000 was meant as milliseconds and made every
# hung call wait about 17 minutes.
DEFAULT_TIMEOUT_SECONDS = 1.0


class Sagawise:
    """Thin client over the Sagawise HTTP API.

    Every method sends exactly the request it describes and raises on any
    failure: ``ValueError`` for a missing argument, and
    ``requests.exceptions.RequestException`` (``HTTPError`` for a non-2xx
    answer, ``ConnectionError``/``Timeout`` for an unreachable server) for a
    failed request. Nothing is caught and returned as a value.

    :param timeout: per-request timeout in seconds (default 1.0)
    """

    def __init__(self, timeout=DEFAULT_TIMEOUT_SECONDS):
        self.base_url = os.getenv('SAGAWISE_URL')
        self.timeout = timeout
        self.session = requests.Session()
        self.session.headers.update({'Content-Type': 'application/json'})

    def _post(self, path, params, json=None):
        response = self.session.post(
            url=f'{self.base_url}{path}',
            params=params,
            json=json,
            timeout=self.timeout,
        )
        response.raise_for_status()
        return response

    def start_workflow(self, workflow_name: str, workflow_version: str):
        """Start a workflow instance and return its ``workflow_instance_id``.

        :param workflow_name: name of the workflow declared in the DSL
        :param workflow_version: version of that workflow
        :raises ValueError: if a required argument is empty
        :raises requests.exceptions.RequestException: if the request fails or Sagawise answers non-2xx
        """
        if not workflow_name or not workflow_version:
            raise ValueError('workflow_name and workflow_version are required.')

        response = self._post('/start_instance', {
            'workflow_name': workflow_name,
            'workflow_version': workflow_version,
        })
        return response.json().get('workflow_instance_id')

    def publish_message(self, workflow_instance_id: str, workflow_version: str, event_name: str, is_retry=False, payload=None):
        """Report that a message was published on ``event_name``.

        :param workflow_instance_id: the instance returned by :meth:`start_workflow`
        :param workflow_version: version of the workflow
        :param event_name: the topic the message was published on
        :param is_retry: True when re-sending a report that may already have been delivered
        :param payload: the message body (a non-empty dict); Sagawise sends it back in the failure webhook
        :raises ValueError: if a required argument is empty
        :raises requests.exceptions.RequestException: if the request fails or Sagawise answers non-2xx
        """
        if not workflow_instance_id or not workflow_version or not event_name or payload is None or payload == {}:
            raise ValueError('Required keys: workflow_instance_id, workflow_version, event_name, payload')

        self._post('/update_instance', {
            'workflow_instance_id': workflow_instance_id,
            'workflow_version': workflow_version,
            'event_name': event_name,
            'action_type': 'publish',
            'is_retry': 'true' if is_retry else 'false',
        }, json=payload)

    def consume_message(self, workflow_instance_id: str, workflow_version: str, event_name: str, service_name: str, is_retry=False):
        """Report that ``service_name`` consumed the message on ``event_name``.

        :raises ValueError: if a required argument is empty
        :raises requests.exceptions.RequestException: if the request fails or Sagawise answers non-2xx
        """
        if not workflow_instance_id or not workflow_version or not event_name or not service_name:
            raise ValueError('Required keys: workflow_instance_id, workflow_version, event_name, service_name')

        self._post('/update_instance', {
            'workflow_instance_id': workflow_instance_id,
            'workflow_version': workflow_version,
            'event_name': event_name,
            'action_type': 'consume',
            'service_name': service_name,
            'is_retry': 'true' if is_retry else 'false',
        })

    def fail_message(self, workflow_instance_id: str, workflow_version: str, event_name: str, service_name: str, is_retry=False):
        """Report that ``service_name`` failed to process the message on ``event_name``.

        :raises ValueError: if a required argument is empty
        :raises requests.exceptions.RequestException: if the request fails or Sagawise answers non-2xx
        """
        if not workflow_instance_id or not workflow_version or not event_name or not service_name:
            raise ValueError('Required keys: workflow_instance_id, workflow_version, event_name, service_name')

        self._post('/update_instance', {
            'workflow_instance_id': workflow_instance_id,
            'workflow_version': workflow_version,
            'event_name': event_name,
            'action_type': 'fail',
            'service_name': service_name,
            'is_retry': 'true' if is_retry else 'false',
        })
