"""Home Assistant sidecar using the Ether Python SDK."""
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from ether_plugin import CredentialError, PluginHttpError, plugin, serve

manifest = json.loads((HERE / 'manifest.json').read_text())
runtime_manifest = json.loads(json.dumps(manifest))
runtime_manifest['ether']['settings'] = json.loads((HERE / 'settings.schema.json').read_text())
SERVICE = re.compile(r'^[a-z0-9_]+\.[a-z0-9_]+$')
ENTITY = re.compile(r'^[a-z0-9_]+\.[a-z0-9_]+$')

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

def call(settings, path, data=None):
    base = str(settings.get('url', '')).rstrip('/')
    token = str(settings.get('token', ''))
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme not in ('http', 'https') or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment or not token:
        raise PluginHttpError(400, 'not_configured', 'Home Assistant settings are incomplete.')
    request = urllib.request.Request(base + path, data=None if data is None else json.dumps(data).encode(), headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json', 'Accept': 'application/json'}, method='GET' if data is None else 'POST')
    try:
        with urllib.request.build_opener(NoRedirect).open(request, timeout=8) as response:
            raw = response.read(1024 * 1024 + 1)
            if len(raw) > 1024 * 1024:
                raise ValueError('response too large')
            return json.loads(raw)
    except (OSError, ValueError):
        raise PluginHttpError(502, 'home_assistant_error', 'Home Assistant request failed.') from None

def run_service(settings, service, entity):
    if not SERVICE.fullmatch(service) or not ENTITY.fullmatch(entity):
        raise PluginHttpError(400, 'invalid_service', 'Enter a valid service and entity.')
    domain, action = service.split('.')
    call(settings, '/api/services/' + domain + '/' + action, {'entity_id': entity})

def events(items, ctx):
    ack = []
    for event in items:
        name = event.get('name', '').removeprefix('playback.')
        if name not in ('started', 'paused', 'stopped'):
            continue
        target = str(ctx.settings.get(name, '')).strip()
        if target:
            try:
                service, entity = target.split()
                run_service(ctx.settings, service, entity)
            except (ValueError, PluginHttpError):
                continue
        ack.append(event.get('id'))
    return {'ack': ack}

def tool(req, ctx):
    if req['name'] == 'ha_states':
        states = call(ctx.settings, '/api/states')
        if not isinstance(states, list):
            raise PluginHttpError(502, 'home_assistant_error', 'Home Assistant returned invalid states.')
        return {'states': [{'entity_id': s.get('entity_id'), 'state': s.get('state')} for s in states[:100] if isinstance(s, dict)]}
    if req['name'] == 'ha_run_scene':
        entity = str(req['input'].get('entity_id', ''))
        if not entity.startswith('scene.'):
            raise PluginHttpError(400, 'invalid_scene', 'Enter a scene entity.')
        run_service(ctx.settings, 'scene.turn_on', entity)
        return {'ok': True}
    raise PluginHttpError(404, 'not_found', 'Tool not found.')

def test(ctx):
    try:
        call(ctx.settings, '/api/')
        return {'ok': True, 'message': 'Connected to Home Assistant.'}
    except PluginHttpError:
        return {'ok': False, 'message': 'Could not connect to Home Assistant.'}

home_assistant = plugin(manifest=runtime_manifest, routes={'event': events, 'tool': tool, 'test': test})

if __name__ == '__main__':
    # The SDK pairs from ETHER_URL and ETHER_PAIRING_CODE on first run, saves
    # the credential under ETHER_PLUGIN_STATE_DIR, and reuses it afterwards.
    # It returns when the tunnel disconnects; keep reconnecting with a bounded
    # delay. A missing or rejected credential is fatal, so it is not retried.
    delay = 1
    while True:
        try:
            serve(home_assistant, mode='tunnel', background=False)
        except CredentialError as exc:
            sys.exit(str(exc))
        except (OSError, ConnectionError):
            pass
        time.sleep(delay)
        delay = min(delay * 2, 60)

