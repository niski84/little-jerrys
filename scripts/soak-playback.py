#!/usr/bin/env python3
"""Read-only local playback soak monitor for Little Jerry's."""

import argparse
import http.client
import ipaddress
import json
import os
import signal
import socket
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timedelta, timezone
from pathlib import Path


DEFAULT_URL = "http://127.0.0.1:8089"
DEFAULT_SOCKET = "/tmp/mpv-jerry.sock"
MAX_SAMPLES = 100_000
MAX_LOG_BYTES = 64 * 1024 * 1024
MAX_RECORD_BYTES = 16 * 1024
READINESS_FAILURE_SECONDS = 60.0
FROZEN_SECONDS = 45.0
EXCLUDED_STATES = {"paused", "static", "slideshow", "eof", "buffering", "seeking", "idle"}
MPV_PROPERTIES = (
    "path", "time-pos", "pause", "eof-reached", "paused-for-cache",
    "seeking", "idle-active", "video-params", "audio-params",
    "demuxer-cache-state",
)


def utc_now():
    return datetime.now(timezone.utc).isoformat()


def validate_loopback_url(raw):
    parsed = urllib.parse.urlsplit(raw)
    if parsed.scheme != "http" or not parsed.hostname or parsed.username or parsed.password:
        raise ValueError("URL must be plain HTTP on a loopback host")
    if parsed.path not in ("", "/") or parsed.query or parsed.fragment:
        raise ValueError("URL must contain only a loopback origin")
    host = parsed.hostname.lower()
    try:
        loopback = ipaddress.ip_address(host).is_loopback
    except ValueError:
        loopback = host == "localhost"
    if not loopback:
        raise ValueError("refusing non-loopback URL")
    return parsed.geturl().rstrip("/")


def playback_state(health, ready):
    playback = ready.get("playback") if isinstance(ready, dict) else None
    if isinstance(playback, dict) and playback.get("state"):
        return str(playback["state"]).lower()
    return str((health or {}).get("playback_state", "unknown")).lower()


def readiness_ok(health_status, health, ready_status, ready):
    return bool(
        health_status == 200
        and isinstance(health, dict)
        and health.get("liveness", "ok") == "ok"
        and str(health.get("playback_ready", "true")).lower() == "true"
        and ready_status == 200
        and isinstance(ready, dict)
        and ready.get("ready") is True
    )


def recovery_count(health, ready):
    playback = ready.get("playback") if isinstance(ready, dict) else None
    value = playback.get("recovery_count") if isinstance(playback, dict) else None
    if value is None and isinstance(health, dict):
        value = health.get("recovery_count")
    try:
        return max(0, int(value))
    except (TypeError, ValueError):
        return None


def is_playing(sample):
    props = sample.get("mpv") or {}
    state = str(sample.get("playback_state", "unknown")).lower()
    if state in EXCLUDED_STATES or state != "playing":
        return False
    if not props.get("path") or props.get("pause") is True:
        return False
    if props.get("eof-reached") is True or props.get("paused-for-cache") is True:
        return False
    if props.get("seeking") is True or props.get("idle-active") is True:
        return False
    return isinstance(props.get("time-pos"), (int, float))


def output_observation(sample):
    if not is_playing(sample):
        return None
    props = sample.get("mpv") or {}
    video = props.get("video-params")
    audio = props.get("audio-params")
    return {
        "video_params_present": isinstance(video, dict) and bool(video),
        "audio_params_present": isinstance(audio, dict) and bool(audio),
    }


class PlaybackMonitor:
    """Pure sample classifier; time is injected to keep threshold tests deterministic."""

    def __init__(self, started_monotonic):
        self.started = started_monotonic
        self.baseline_recovery = None
        self.last_recovery = None
        self.recovery_delta = 0
        self.last_path = None
        self.track_changes = 0
        self.ready_since = None
        self.ready_failure_reported = False
        self.frozen_since = None
        self.frozen_path = None
        self.frozen_position = None
        self.stall_reported = False
        self.failures = []

    def _failure(self, kind, at, detail=None):
        self.failures.append({"kind": kind, "at": at, **({"detail": detail} if detail else {})})

    def observe(self, sample, now):
        at = sample.get("timestamp", utc_now())
        props = sample.get("mpv") or {}
        path = props.get("path")
        if path and not str(path).lower().endswith(('.png', '.jpg', '.jpeg', '.webp')):
            path = str(path)[:512]
            if self.last_path is not None and path != self.last_path:
                self.track_changes += 1
            self.last_path = path

        count = sample.get("recovery_count")
        if count is not None:
            try:
                count = max(0, int(count))
            except (TypeError, ValueError):
                count = None
        if count is not None:
            if self.baseline_recovery is None:
                self.baseline_recovery = count
                self.last_recovery = count
            elif count > self.last_recovery:
                self.recovery_delta += count - self.last_recovery
                self._failure("unexpected_recovery", at, {"delta": self.recovery_delta})
                self.last_recovery = count
            elif count < self.last_recovery:
                self.last_recovery = count

        state = str(sample.get("playback_state", "unknown")).lower()
        # Short EOF/IPC transitions get the same bounded grace as other gaps.
        # Never turn an indefinitely stuck EOF or an unreadable player into a pass.
        observable = not sample.get("observation_errors") and bool(props)
        if state == "playing":
            observable = observable and is_playing(sample)
        if sample.get("ready", False) and observable:
            self.ready_since = None
            self.ready_failure_reported = False
        else:
            if self.ready_since is None:
                self.ready_since = now
            elif (now - self.ready_since >= READINESS_FAILURE_SECONDS
                  and not self.ready_failure_reported):
                self._failure("readiness_failure", at)
                self.ready_failure_reported = True

        if not is_playing(sample):
            self.frozen_since = None
            self.frozen_path = None
            self.frozen_position = None
            self.stall_reported = False
            return

        position = float(props["time-pos"])
        if self.frozen_path != path:
            self.frozen_path = path
            self.frozen_position = position
            self.frozen_since = now
            self.stall_reported = False
            return
        if self.frozen_position is None or abs(position - self.frozen_position) > 0.25:
            self.frozen_position = position
            self.frozen_since = now
            self.stall_reported = False
            return
        if (self.frozen_since is not None and now - self.frozen_since >= FROZEN_SECONDS
                and not self.stall_reported):
            self._failure("persistent_stall", at, {"path": path})
            self.stall_reported = True


def http_json(url, path, timeout):
    request = urllib.request.Request(url + path, headers={"Accept": "application/json"}, method="GET")
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            status = response.status
            payload = response.read(64 * 1024)
    except urllib.error.HTTPError as exc:
        status = exc.code
        payload = exc.read(64 * 1024)
    return status, json.loads(payload.decode("utf-8"))


def mpv_properties(socket_path, timeout):
    values = {}
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
        client.settimeout(timeout)
        client.connect(socket_path)
        stream = client.makefile("rb")
        wanted = {i + 1: name for i, name in enumerate(MPV_PROPERTIES)}
        for request_id, name in wanted.items():
            request = {"command": ["get_property", name], "request_id": request_id}
            client.sendall((json.dumps(request, separators=(",", ":")) + "\n").encode())
        deadline = time.monotonic() + timeout
        while wanted:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            client.settimeout(remaining)
            line = stream.readline(MAX_RECORD_BYTES)
            if not line:
                break
            try:
                reply = json.loads(line)
            except (UnicodeDecodeError, json.JSONDecodeError):
                continue
            request_id = reply.get("request_id")
            if request_id in wanted:
                name = wanted.pop(request_id)
                if reply.get("error") == "success":
                    values[name] = reply.get("data")
        return values


def secure_output(directory):
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    if directory.is_symlink() or not directory.is_dir():
        raise ValueError("output must be a real directory")
    os.chmod(directory, 0o700)
    log_path = directory / "samples.jsonl"
    flags = os.O_WRONLY | os.O_APPEND | os.O_CREAT
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    fd = os.open(log_path, flags, 0o600)
    os.fchmod(fd, 0o600)
    if os.fstat(fd).st_size > MAX_LOG_BYTES:
        os.close(fd)
        raise ValueError("samples.jsonl already exceeds the bounded log size")
    return fd, log_path


def atomic_summary(directory, summary):
    fd, temp_path = tempfile.mkstemp(prefix=".summary-", dir=directory)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as output:
            json.dump(summary, output, sort_keys=True, indent=2)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.replace(temp_path, directory / "summary.json")
        dirfd = os.open(directory, os.O_RDONLY)
        try:
            os.fsync(dirfd)
        finally:
            os.close(dirfd)
    finally:
        try:
            os.unlink(temp_path)
        except FileNotFoundError:
            pass


def run(args):
    base_url = validate_loopback_url(args.url)
    if args.duration <= 0 or args.interval <= 0:
        raise ValueError("duration and interval must be positive")
    expected_samples = int(args.duration / args.interval) + 2
    if expected_samples > MAX_SAMPLES:
        raise ValueError(f"requested run exceeds {MAX_SAMPLES} sample bound")
    output_dir = Path(args.output).expanduser().absolute()
    log_fd, log_path = secure_output(output_dir)
    log_size = os.fstat(log_fd).st_size
    started_at = utc_now()
    expected_end_at = (datetime.now(timezone.utc) + timedelta(seconds=args.duration)).isoformat()
    started_mono = time.monotonic()
    expected_end = started_mono + args.duration
    stop_requested = {"value": False}

    def request_stop(_signum, _frame):
        stop_requested["value"] = True

    old_handlers = {}
    for sig in (signal.SIGTERM, signal.SIGINT):
        old_handlers[sig] = signal.signal(sig, request_stop)

    monitor = PlaybackMonitor(started_mono)
    samples = 0
    complete = False
    fatal_error = None
    try:
        while time.monotonic() < expected_end and not stop_requested["value"]:
            sample_started = time.monotonic()
            timestamp = utc_now()
            health_status = ready_status = None
            health = ready = None
            errors = []
            try:
                health_status, health = http_json(base_url, "/api/health", min(4, args.interval))
                ready_status, ready = http_json(base_url, "/api/ready", min(4, args.interval))
            except (OSError, ValueError, http.client.HTTPException) as exc:
                errors.append(type(exc).__name__)
            state = playback_state(health or {}, ready or {})
            try:
                props = mpv_properties(args.socket, min(4, args.interval))
            except (OSError, ValueError, json.JSONDecodeError) as exc:
                props = {}
                errors.append(type(exc).__name__)
            ready_ok = readiness_ok(health_status, health, ready_status, ready)
            count = recovery_count(health or {}, ready or {})
            sample = {
                "timestamp": timestamp,
                "elapsed_seconds": round(sample_started - started_mono, 3),
                "http": {"health": health_status, "ready": ready_status},
                "ready": ready_ok,
                "playback_state": state,
                "recovery_count": count,
                "mpv": props,
                "outputs_while_playing": output_observation({"playback_state": state, "mpv": props}),
            }
            if errors:
                sample["observation_errors"] = errors
            monitor.observe(sample, sample_started)
            encoded = (json.dumps(sample, separators=(",", ":"), ensure_ascii=True) + "\n").encode()
            if len(encoded) > MAX_RECORD_BYTES:
                fatal_error = "sample exceeds record-size bound"
                break
            if samples >= MAX_SAMPLES or log_size + len(encoded) > MAX_LOG_BYTES:
                fatal_error = "sample log reached configured size bound"
                break
            view = memoryview(encoded)
            while view:
                written = os.write(log_fd, view)
                view = view[written:]
            log_size += len(encoded)
            samples += 1
            atomic_summary(output_dir, {
                "started": started_at, "expectedend": expected_end_at,
                "last_sample": timestamp, "duration_seconds": args.duration,
                "actual_elapsed_seconds": round(time.monotonic() - started_mono, 3),
                "complete": False, "passed": False, "samples": samples,
                "failures": monitor.failures, "actual_track_changes": monitor.track_changes,
                "recovery_count_baseline": monitor.baseline_recovery,
                "recovery_count_delta": monitor.recovery_delta,
                "sample_file": str(log_path),
            })
            delay = args.interval - (time.monotonic() - sample_started)
            if delay > 0 and not stop_requested["value"]:
                time.sleep(min(delay, max(0, expected_end - time.monotonic())))
        actual_elapsed = time.monotonic() - started_mono
        complete = actual_elapsed >= args.duration
    except Exception as exc:
        fatal_error = type(exc).__name__
    finally:
        os.close(log_fd)
        for sig, old_handler in old_handlers.items():
            signal.signal(sig, old_handler)
        ended_at = utc_now()
        actual_elapsed = time.monotonic() - started_mono
        summary = {
            "started": started_at,
            "end": ended_at,
            "expectedend": expected_end_at,
            "duration_seconds": args.duration,
            "actual_elapsed_seconds": round(actual_elapsed, 3),
            "complete": bool(complete and actual_elapsed >= args.duration),
            "passed": bool(complete and actual_elapsed >= args.duration and not monitor.failures and not fatal_error),
            "samples": samples,
            "failures": monitor.failures,
            "actual_track_changes": monitor.track_changes,
            "recovery_count_baseline": monitor.baseline_recovery,
            "recovery_count_delta": monitor.recovery_delta,
            "mpv_pid_changes": None,
            "fatal_error": fatal_error,
            "sample_file": str(log_path),
        }
        atomic_summary(output_dir, summary)
    return 0 if summary["passed"] else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, help="private output directory")
    parser.add_argument("--duration", type=float, default=43200, help="monitor duration in seconds")
    parser.add_argument("--interval", type=float, default=15, help="sample interval in seconds")
    parser.add_argument("--url", default=DEFAULT_URL, help="loopback HTTP origin only")
    parser.add_argument("--socket", default=DEFAULT_SOCKET, help="read-only mpv IPC socket path")
    args = parser.parse_args()
    try:
        return run(args)
    except (OSError, ValueError) as exc:
        parser.error(str(exc))


if __name__ == "__main__":
    raise SystemExit(main())
