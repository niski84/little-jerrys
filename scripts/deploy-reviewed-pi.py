#!/usr/bin/env python3
"""Operator-approved canonical-to-canonical deployment, never a remote actuator.

Arguments: candidate, candidate SHA256, revision, installed SHA256, revision.
Requires exact identities, disabled legacy updates, backups, and live checks.
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
    with open(path, 'rb') as f:
        return hashlib.sha256(f.read()).hexdigest()


def active(unit):
    return subprocess.run(['systemctl', 'is-active', '--quiet', unit]).returncode == 0


def health():
    with urllib.request.urlopen('http://127.0.0.1:8089/api/health', timeout=3) as response:
        return json.load(response)


def main():
    candidate, expected, revision, previous_hash, previous_revision = sys.argv[1:]
    assert os.geteuid() == 0
    assert all(re.fullmatch('[a-f0-9]{64}', x) for x in [expected, previous_hash])
    assert all(re.fullmatch('[a-f0-9]{40}', x) for x in [revision, previous_revision])
    assert Path('/sys/class/net/wlan0/address').read_text().strip() == 'b8:27:eb:cc:86:85'
    binary = Path('/opt/jerry/jerry')
    assert digest(candidate) == expected and digest(binary) == previous_hash
    old = health()
    assert old['revision'] == previous_revision and old['modified'] == 'false'
    assert old['auto_update'] == 'false' and old['service'] == 'little-jerrys'
    assert active('jerry.service') and not active('jerry-maintainer.service')
    assert not active('jerry-update.path') and not active('jerry-update.service')
    assert run('systemctl', 'show', 'jerry.service', '-p', 'ExecStart', '--value').find('/opt/jerry/jerry') >= 0
    backup = Path('/var/backups/little-jerrys') / (time.strftime('%Y%m%dT%H%M%S')+'-'+revision[:12])
    backup.mkdir(mode=0o700)
    archive = backup/'config-state.tar'
    watchdog = active('jerry-watchdog.service')
    staged = binary.with_name('jerry.reviewed-new')
    assert not staged.exists()
    saved = False
    try:
        run('systemctl', 'stop', 'jerry-watchdog.service', 'jerry.service')
        shutil.copy2(binary, backup/'jerry')
        with tarfile.open(archive, 'w') as tar:
            for path in ['/var/lib/jerry', '/etc/jerry', '/etc/systemd/system/jerry.service',
                         '/etc/systemd/system/jerry.service.d', '/opt/jerry/media/jerry.conf']:
                if Path(path).exists(): tar.add(path, arcname=path.lstrip('/'))
        saved = True
        (backup/'manifest.json').write_text(json.dumps({'revision':revision, 'sha256':expected,
            'previous_revision':previous_revision, 'previous_sha256':previous_hash,
            'watchdog_active':watchdog}, indent=2))
        os.sync()
        shutil.copyfile(candidate, staged)
        os.chmod(staged, 0o755)
        assert digest(staged) == expected
        os.replace(staged, binary)
        os.sync()
        run('systemctl', 'start', 'jerry.service')
        deadline = time.monotonic()+90
        while time.monotonic()<deadline:
            try:
                new = health()
                assert new['revision']==revision and new['modified']=='false'
                assert new['auto_update']=='false' and new['device_id']==old['device_id']
                assert new['status']=='ok' and new.get('playback_state')=='playing'
                break
            except Exception:
                time.sleep(1)
        else: raise RuntimeError('candidate did not reach verified playback readiness')
        pid = run('systemctl','show','jerry.service','-p','MainPID','--value')
        assert digest(binary)==expected and digest('/proc/'+pid+'/exe')==expected
        smoke = Path(__file__).with_name('verify-live-pi.sh').read_text().split("<<'PY'\n",1)[1].rsplit('\nPY',1)[0]
        result = subprocess.run(['python3','-c',smoke],capture_output=True,text=True,timeout=30)
        assert result.returncode==0, result.stderr[-1000:]
        if watchdog: run('systemctl','start','jerry-watchdog.service')
        print(json.dumps({'deployed':True,'health':new,'backup':str(backup),'physical_smoke':json.loads(result.stdout)}))
    except Exception:
        if saved:
            run('systemctl','stop','jerry.service')
            shutil.copy2(backup/'jerry', staged)
            os.replace(staged,binary)
            run('tar','-xf',str(archive),'-C','/')
            run('systemctl','daemon-reload')
        run('systemctl','start','jerry.service')
        if watchdog: run('systemctl','start','jerry-watchdog.service')
        print('Deployment failed; restored previous app. Backup: '+str(backup),file=sys.stderr)
        raise


if __name__=='__main__': main()
