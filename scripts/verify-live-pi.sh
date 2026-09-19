#!/usr/bin/env bash
# Read-only physical-appliance smoke. No restart, login POST or playback controls.
# PI_HOST=192.168.4.40 bash scripts/verify-live-pi.sh
set -euo pipefail
: "${PI_HOST:?Set PI_HOST after confirming the device in AGENT-HANDOFF.md}"
ssh -i "${PI_SSH_KEY:-$HOME/.ssh/id_ed25519}" \
  -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes \
  -o ConnectTimeout=8 -o ServerAliveInterval=5 -o ServerAliveCountMax=2 \
  "${PI_USER:-jerry}@${PI_HOST}" 'timeout 25s python3 -' <<'PY'
import glob
import json
import os
import platform
import socket
import subprocess
import time
import urllib.request

def command(*args):
    return subprocess.check_output(args, text=True, timeout=5).strip()

assert command('systemctl', 'is-active', 'jerry.service') == 'active', 'jerry.service is not active'
with urllib.request.urlopen('http://127.0.0.1:8089/api/health', timeout=4) as response:
    health = json.load(response)
assert health.get('status') == 'ok', 'unhealthy app'
assert health.get('service') in ('little-jerrys', 'sienfeld_smash_deck'), 'unexpected app identity'
hdmi = []
for path in glob.glob('/sys/class/drm/card*-HDMI-A-*/status'):
    with open(path) as source:
        if source.read().strip() == 'connected':
            hdmi.append(os.path.basename(os.path.dirname(path)))
assert hdmi, 'no connected HDMI output'

with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
    client.settimeout(4)
    client.connect('/tmp/mpv-jerry.sock')
    stream = client.makefile('rb')
    def get_property(name, request_id):
        client.sendall((json.dumps({'command': ['get_property', name], 'request_id': request_id}) + '\n').encode())
        deadline = time.monotonic() + 4
        while time.monotonic() < deadline:
            line = stream.readline()
            assert line, 'mpv socket closed'
            reply = json.loads(line)
            if reply.get('request_id') == request_id:
                assert reply.get('error') == 'success', 'mpv property unavailable: ' + name
                return reply.get('data')
        raise AssertionError('mpv property timed out: ' + name)

    names = ['duration', 'video-out-params', 'vo-configured', 'audio-device', 'audio-params', 'idle-active', 'pause', 'time-pos']
    values = {name: get_property(name, i) for i, name in enumerate(names, 1)}
    assert (values['duration'] or 0) > 0, 'no media duration'
    assert (values['video-out-params'] or {}).get('w', 0) > 0, 'video not configured'
    assert values['vo-configured'] is True, 'video output inactive'
    assert values['audio-device'] and 'null' not in str(values['audio-device']).lower(), 'null audio output'
    assert values['audio-params'], 'audio parameters missing'
    assert values['idle-active'] is False, 'player idle'
    assert values['pause'] is False, 'player paused'
    time.sleep(1)
    next_position = get_property('time-pos', 100)
    assert next_position > values['time-pos'], 'playback did not advance (retry if an episode just ended)'

print(json.dumps({'passed': True, 'service': health['service'], 'version': health.get('version'),
    'architecture': platform.machine(), 'hdmi': hdmi, 'video': True, 'audio': True,
    'playback_advancing': True, 'modified_device': False}))
PY
