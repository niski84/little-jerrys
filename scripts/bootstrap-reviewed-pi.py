#!/usr/bin/env python3
"""One-time reviewed legacy bootstrap; run as root on the identified Pi.

Arguments: staged binary, expected sha256, clean revision. Not an unattended
updater. Backs up the stopped appliance and restores it if health/smoke fails.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import time
import urllib.request


def run(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, timeout=60).strip()


def digest(path):
    with open(path, "rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest() if hasattr(hashlib, "file_digest") else hashlib.sha256(source.read()).hexdigest()


def active(unit):
    return subprocess.run(["systemctl", "is-active", "--quiet", unit]).returncode == 0


def main():
    candidate, expected, revision = sys.argv[1:]
    assert os.geteuid() == 0, "requires root"
    assert re.fullmatch(r"[a-f0-9]{64}", expected) and re.fullmatch(r"[a-f0-9]{40}", revision)
    assert Path('/sys/class/net/wlan0/address').read_text().strip() == 'b8:27:eb:cc:86:85', 'wrong device'
    assert digest(candidate) == expected, 'candidate digest mismatch'
    binary = Path('/opt/jerry/jerry')
    assert digest(binary) == '5f8b0927aff32e1f7aa39dad410628dd000ced99a83c8a2d5910f439b937b5ba', 'legacy baseline changed'
    assert active('jerry.service') and not active('jerry-maintainer.service') and not active('jerry-update.service')
    pid = run('systemctl', 'show', 'jerry.service', '-p', 'MainPID', '--value')
    env = dict(x.split('=', 1) for x in Path('/proc/'+pid+'/environ').read_bytes().decode().split('\0') if '=' in x)
    assert env.get('JERRY_STATE_PATH') == '/var/lib/jerry/state.json'
    assert env.get('JERRY_MEDIA_ROOT') == '/opt/jerry/media'
    dropin = Path('/etc/systemd/system/jerry.service.d/90-reviewed-migration.conf')
    envfile = Path('/etc/jerry/migration.env')
    assert not dropin.exists() and not envfile.exists(), 'migration configuration already exists'
    backup = Path('/var/backups/little-jerrys') / (time.strftime('%Y%m%dT%H%M%S')+'-'+revision[:12])
    backup.mkdir(parents=True, mode=0o700)
    os.chmod(backup.parent, 0o700)
    watchdog = active('jerry-watchdog.service')
    update_path = active('jerry-update.path')
    update_enabled = subprocess.run(['systemctl', 'is-enabled', '--quiet', 'jerry-update.path']).returncode == 0
    archive = backup / 'config-state.tar'
    paths = ['/var/lib/jerry', '/etc/jerry', '/etc/systemd/system/jerry.service',
             '/etc/systemd/system/jerry.service.d', '/etc/systemd/system/jerry-update.path',
             '/etc/systemd/system/jerry-update.service', '/etc/systemd/system/jerry-watchdog.service',
             '/opt/jerry/media/jerry.conf', '/opt/jerry/jerry-watchdog.sh',
             '/opt/jerry/update-apply.sh', '/home/jerry/.config/mpv']
    saved = False
    try:
        run('systemctl', 'stop', 'jerry-watchdog.service', 'jerry-update.path')
        run('systemctl', 'stop', 'jerry.service')
        shutil.copy2(binary, backup / 'jerry')
        with tarfile.open(archive, 'w') as tar:
            for path in paths:
                if Path(path).exists(): tar.add(path, arcname=path.lstrip('/'))
        saved = True
        (backup / 'manifest.json').write_text(json.dumps({'revision': revision, 'candidate_sha256': expected,
            'previous_sha256': digest(backup/'jerry'), 'watchdog_active': watchdog,
            'update_path_active': update_path, 'update_path_enabled': update_enabled}, indent=2))
        dropin.parent.mkdir(exist_ok=True)
        envfile.parent.mkdir(exist_ok=True)
        envfile.write_text('JERRY_AUTO_UPDATE=false\n')
        os.chmod(envfile, 0o600)
        dropin.write_text('[Service]\nEnvironmentFile=/etc/jerry/migration.env\n')
        run('systemctl', 'disable', 'jerry-update.path')
        staged = binary.with_name('jerry.reviewed-new')
        assert not staged.exists(), 'unexpected pending binary'
        shutil.copyfile(candidate, staged)
        os.chmod(staged, 0o755)
        assert digest(staged) == expected
        os.replace(staged, binary)
        run('systemctl', 'daemon-reload')
        run('systemctl', 'start', 'jerry.service')
        last_error = None
        for _ in range(45):
            try:
                with urllib.request.urlopen('http://127.0.0.1:8089/api/health', timeout=2) as response:
                    health = json.load(response)
                assert health.get('revision') == revision and health.get('modified') == 'false'
                assert health.get('service') == 'little-jerrys' and health.get('auto_update') == 'false'
                assert health.get('status') == 'ok' and health.get('device_id')
                break
            except Exception as error:
                last_error = error
                time.sleep(1)
        else: raise RuntimeError('candidate health failed: '+str(last_error))
        assert digest(binary) == expected
        pid = run('systemctl', 'show', 'jerry.service', '-p', 'MainPID', '--value')
        assert digest('/proc/'+pid+'/exe') == expected, 'running process differs'
        # Existing reviewed read-only smoke's Python payload, no SSH recursion.
        smoke = Path(__file__).with_name('verify-live-pi.sh').read_text().split("<<'PY'\n", 1)[1].rsplit('\nPY', 1)[0]
        result = subprocess.run(['python3', '-c', smoke], capture_output=True, text=True, timeout=30)
        assert result.returncode == 0, 'physical smoke failed: '+result.stderr[-1000:]
        if watchdog: run('systemctl', 'start', 'jerry-watchdog.service')
        print(json.dumps({'deployed': True, 'health': health, 'backup': str(backup), 'physical_smoke': json.loads(result.stdout)}))
    except Exception:
        if saved:
            run('systemctl', 'stop', 'jerry.service')
            shutil.copy2(backup/'jerry', binary.with_name('jerry.reviewed-rollback'))
            os.replace(binary.with_name('jerry.reviewed-rollback'), binary)
            for path in (dropin, envfile):
                if path.exists(): path.unlink()
            run('tar', '-xf', str(archive), '-C', '/')
            run('systemctl', 'daemon-reload')
        run('systemctl', 'start', 'jerry.service')
        if update_enabled: run('systemctl', 'enable', 'jerry-update.path')
        if update_path: run('systemctl', 'start', 'jerry-update.path')
        if watchdog: run('systemctl', 'start', 'jerry-watchdog.service')
        print('Bootstrap failed; legacy service restored. Recovery backup: '+str(backup), file=sys.stderr)
        raise


if __name__ == '__main__': main()
