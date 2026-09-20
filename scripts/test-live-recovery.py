#!/usr/bin/env python3
"""APPROVED DISRUPTIVE Pi tests: owned mpv SIGSTOP, pause, then near-EOF seek.
Run as root: script EXACT_REVISION OUTPUT_JSON. Never run as a periodic job.
"""
import hashlib
import hmac
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


def get(route):
    try:
        response=urllib.request.urlopen('http://127.0.0.1:8089'+route,timeout=3)
    except urllib.error.HTTPError as e:
        response=e
    with response: return response.code,json.load(response)


def ipc(name):
    with socket.socket(socket.AF_UNIX,socket.SOCK_STREAM) as sock:
        sock.settimeout(3);sock.connect('/tmp/mpv-jerry.sock')
        sock.sendall((json.dumps({'command':['get_property',name],'request_id':1})+'\n').encode())
        with sock.makefile('rb') as stream:
            while True:
                line=stream.readline()
                if not line: raise RuntimeError('mpv closed IPC')
                result=json.loads(line)
                if result.get('request_id')==1:
                    assert result.get('error')=='success',name+': '+str(result.get('error'))
                    return result.get('data')


def app_pid():
    return int(subprocess.check_output(['systemctl','show','jerry.service','-p','MainPID','--value'],text=True))


def mpv_pid():
    pids=subprocess.check_output(['pgrep','-P',str(app_pid()),'-x','mpv'],text=True).split()
    matches=[int(p) for p in pids if b'--input-ipc-server=/tmp/mpv-jerry.sock' in Path('/proc/'+p+'/cmdline').read_bytes().split(b'\0')]
    assert len(matches)==1,'expected one owned primary mpv'
    return matches[0]


def identity(pid):
    return Path('/proc/'+str(pid)+'/stat').read_text().rsplit(')',1)[1].split()[19]


def until_ready(seconds=30):
    end=time.monotonic()+seconds
    while time.monotonic()<end:
        try:
            code,result=get('/api/ready')
            if code==200 and result['playback']['state']=='playing':
                before=ipc('time-pos');time.sleep(1)
                if ipc('time-pos')>before:return result
        except (OSError,ValueError,AssertionError,RuntimeError): pass
        time.sleep(1)
    raise RuntimeError('playback did not become ready and advance')


def main():
    revision,output=sys.argv[1:]
    assert os.geteuid()==0
    assert Path('/sys/class/net/wlan0/address').read_text().strip()=='b8:27:eb:cc:86:85'
    _,health=get('/api/health')
    assert health['revision']==revision and health['modified']=='false'
    assert health['auto_update']=='false'
    until_ready()
    key=json.loads(Path('/var/lib/jerry/state.json').read_text())['session_key']
    def post(route,values=None):
        payload='admin:'+str(int(time.time())+60)
        signature=hmac.new(key.encode(),payload.encode(),hashlib.sha256).hexdigest()
        request=urllib.request.Request('http://127.0.0.1:8089'+route,
            data=urllib.parse.urlencode(values or {}).encode(),
            headers={'Cookie':'jerry_sid='+payload+'.'+signature})
        with urllib.request.urlopen(request,timeout=5) as response:assert response.status==204
    report={'revision':revision,'started_at':time.strftime('%Y-%m-%dT%H:%M:%S%z'),'passed':False}
    stopped=None
    paused=False
    try:
        primary=app_pid(); victim=mpv_pid();stamp=identity(victim)
        path=ipc('path');position=ipc('time-pos')
        stopped=(victim,stamp)
        os.kill(victim,signal.SIGSTOP)
        started=time.monotonic();observed_unready=False
        deadline=started+90
        while time.monotonic()<deadline:
            code,result=get('/api/ready')
            observed_unready |= code==503
            try:
                replacement=mpv_pid()
            except (subprocess.CalledProcessError,FileNotFoundError,AssertionError):
                replacement=None  # Expected briefly while the owned player respawns.
            if replacement and replacement!=victim and code==200 and result['playback']['state']=='playing':break
            time.sleep(2)
        else:raise RuntimeError('no autonomous frozen-process recovery within 90s')
        assert observed_unready,'freeze was never exposed by readiness'
        assert app_pid()==primary,'external service restart masked player recovery'
        assert ipc('path')==path,'recovery skipped the current track'
        recovered=until_ready()
        assert abs(ipc('time-pos')-position)<30,'recovery lost the saved position'
        report['frozen_process']={'passed':True,'recovery_seconds':round(time.monotonic()-started,2),'readiness_503_observed':True,'app_pid_unchanged':True,'same_track':True}
        print(json.dumps(report['frozen_process']),flush=True)
        stopped=None

        baseline=recovered['playback']['recovery_count'];pid=mpv_pid()
        post('/api/pause');paused=True;position=ipc('time-pos')
        time.sleep(40)
        code,result=get('/api/ready')
        assert code==200 and result['playback']['state']=='paused'
        assert ipc('pause') is True and abs(ipc('time-pos')-position)<0.5
        assert mpv_pid()==pid and result['playback']['recovery_count']==baseline
        post('/api/play');paused=False;until_ready()
        report['intentional_pause']={'passed':True,'seconds':40,'no_forced_resume_or_restart':True}
        print(json.dumps(report['intentional_pause']),flush=True)

        path=ipc('path');duration=ipc('duration')
        assert duration>10
        post('/api/seek',{'secs':duration-3})
        deadline=time.monotonic()+40
        while time.monotonic()<deadline:
            try: current=ipc('path')
            except (OSError,AssertionError,RuntimeError):
                time.sleep(1)
                continue
            if current!=path and not current.endswith(('.png','.jpg','.jpeg')):break
            time.sleep(1)
        else:raise RuntimeError('no automatic next episode after EOF')
        until_ready()
        assert app_pid()==primary
        report['episode_boundary']={'passed':True,'next_track_loaded':True,'app_pid_unchanged':True}
        report['passed']=True
    except Exception as error:
        report['error']=str(error)
        raise
    finally:
        if stopped:
            try:
                if identity(stopped[0])==stopped[1]:os.kill(stopped[0],signal.SIGCONT)
            except FileNotFoundError:pass
        if paused:post('/api/play')
        report['finished_at']=time.strftime('%Y-%m-%dT%H:%M:%S%z')
        fd=os.open(output,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
        with os.fdopen(fd,'w') as out:json.dump(report,out,indent=2)
        print(json.dumps(report),flush=True)


if __name__=='__main__':
    def interrupted(*_):raise RuntimeError('test interrupted; restoring controls')
    signal.signal(signal.SIGTERM,interrupted)
    signal.signal(signal.SIGINT,interrupted)
    main()
