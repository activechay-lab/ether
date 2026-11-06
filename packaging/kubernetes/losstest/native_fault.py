"""Bind an observed eligible native execution to an armed, guarded fault request.

The native supervisor remains the sole authority for the fresh retained-handle
check and the actual kill. Queuing an observation avoids a second network-read
race; it never authorizes killing a process that the supervisor deems ineligible.
"""
from datetime import datetime


def eligible(state, run_id, session, now):
    if state.get('runID') != run_id or state.get('session') != session:
        raise RuntimeError('Run/session binding mismatch')
    age = now - datetime.fromisoformat(state['atUTC'].replace('Z', '+00:00')).timestamp()
    boundaries = state.get('nativeRestartBoundaries', [])
    timeline = state.get('authoredTimeline', {})
    return bool(len(set(boundaries)) >= 2 and
                state.get('lastNativeProducedIndex', -1) > max(boundaries) and
                -1 <= age < 5 and timeline.get('valid') is True and
                timeline.get('underHalf') is True and state.get('killEligible') is True and
                state.get('liveNVENC') is True and state.get('restartCount', -1) >= 2 and
                state.get('nodeId') and state.get('boot'))


def arm(observation, startup, run_id, session, now, retain, enqueue):
    """Retain every accepted/rejected observation; queue once, with no reread."""
    proof = {'runID': run_id, 'session': session, 'observation': observation,
             'startup': startup, 'checkedAtUnix': now, 'accepted': False}
    try:
        if not eligible(observation, run_id, session, now):
            raise RuntimeError('Observed native encoder state is ineligible or stale')
        if (startup.get('runID') != run_id or startup.get('session') != session or
                startup.get('nodeId') != observation['nodeId'] or
                startup.get('boot') != observation['boot'] or
                startup.get('hardware') != 'nvenc' or not startup.get('nvencSmokeVerified')):
            raise RuntimeError('Native startup identity/probe mismatch')
        request = {'runID': run_id, 'session': session, 'nodeId': observation['nodeId'],
                   'boot': observation['boot'],
                   'timelineSHA256': observation['authoredTimeline']['timelineSHA256']}
        proof['accepted'] = True
        retain(proof)
        enqueue(request)
        return request
    except Exception as error:
        proof.update(accepted=False, errorType=type(error).__name__, error=str(error))
        retain(proof)
        raise
