"""Observation filters for selecting a live ingress; never mutate lab resources."""
def ready_ingresses(pods, prefix):
    return [pod for pod in pods
            if pod['metadata']['name'].startswith(prefix)
            and not pod['metadata'].get('deletionTimestamp')
            and pod.get('status', {}).get('phase') == 'Running'
            and any(condition.get('type') == 'Ready' and condition.get('status') == 'True'
                    for condition in pod.get('status', {}).get('conditions', []))]
