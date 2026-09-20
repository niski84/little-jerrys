"""Pure classification tests for soak-playback.py."""

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch


SCRIPT = Path(__file__).with_name("soak-playback.py")
SPEC = importlib.util.spec_from_file_location("soak_playback", SCRIPT)
soak = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(soak)


def sample(at, *, state="playing", path="/media/episode.mp4", pos=12.0,
           ready=True, paused=False, eof=False, buffering=False,
           seeking=False, recovery=0):
    return {
        "timestamp": str(at),
        "ready": ready,
        "playback_state": state,
        "recovery_count": recovery,
        "mpv": {
            "path": path,
            "time-pos": pos,
            "pause": paused,
            "eof-reached": eof,
            "paused-for-cache": buffering,
            "seeking": seeking,
            "idle-active": False,
            "video-params": {"w": 1920, "h": 1080},
            "audio-params": {"samplerate": 48000},
        },
    }


class PlaybackClassificationTests(unittest.TestCase):
    def test_url_must_be_loopback_and_plain_http(self):
        self.assertEqual(soak.validate_loopback_url("http://127.0.0.1:8089/"), "http://127.0.0.1:8089")
        for url in ("http://example.com", "https://127.0.0.1", "http://user@localhost"):
            with self.subTest(url=url), self.assertRaises(ValueError):
                soak.validate_loopback_url(url)

    def test_track_changes_are_counted_and_position_progress_resets_stall(self):
        monitor = soak.PlaybackMonitor(0)
        monitor.observe(sample(0, pos=1), 0)
        monitor.observe(sample(20, pos=2), 20)
        self.assertEqual(monitor.failures, [])
        monitor.observe(sample(21, path="/media/next.mp4", pos=0), 21)
        self.assertEqual(monitor.track_changes, 1)

    def test_frozen_clock_fails_at_45_seconds_of_playing(self):
        monitor = soak.PlaybackMonitor(0)
        monitor.observe(sample(0, pos=10), 0)
        monitor.observe(sample(44.9, pos=10), 44.9)
        self.assertEqual(monitor.failures, [])
        monitor.observe(sample(45, pos=10), 45)
        self.assertEqual([f["kind"] for f in monitor.failures], ["persistent_stall"])

    def test_pause_static_slideshow_eof_buffering_and_seeking_are_excluded(self):
        for options in (
            {"paused": True}, {"state": "static"}, {"state": "slideshow"},
            {"state": "eof", "eof": True}, {"buffering": True}, {"seeking": True},
        ):
            with self.subTest(options=options):
                self.assertFalse(soak.is_playing(sample(0, **options)))
                self.assertIsNone(soak.output_observation(sample(0, **options)))

    def test_readiness_failure_requires_60s_including_stuck_eof(self):
        monitor = soak.PlaybackMonitor(0)
        monitor.observe(sample(0, state="recovering", ready=False), 0)
        monitor.observe(sample(59.9, state="recovering", ready=False), 59.9)
        self.assertEqual(monitor.failures, [])
        monitor.observe(sample(60, state="recovering", ready=False), 60)
        self.assertEqual([f["kind"] for f in monitor.failures], ["readiness_failure"])

        transient = soak.PlaybackMonitor(0)
        transient.observe(sample(0, state="recovering", ready=False), 0)
        transient.observe(sample(59, state="eof", eof=True, ready=False), 59)
        transient.observe(sample(59.5, pos=13), 59.5)
        self.assertEqual(transient.failures, [])
        stuck = soak.PlaybackMonitor(0)
        stuck.observe(sample(0, state="eof", eof=True, ready=False), 0)
        stuck.observe(sample(60, state="eof", eof=True, ready=False), 60)
        self.assertEqual(stuck.failures[0]["kind"], "readiness_failure")

    def test_unobservable_player_cannot_pass_from_http_readiness_alone(self):
        monitor = soak.PlaybackMonitor(0)
        missing = sample(0)
        missing["mpv"] = {}
        monitor.observe(missing, 0)
        monitor.observe(missing, 60)
        self.assertEqual(monitor.failures[0]["kind"], "readiness_failure")

    def test_recovery_after_rolling_counter_expires_is_detected(self):
        monitor = soak.PlaybackMonitor(0)
        monitor.observe(sample(0, recovery=2), 0)
        monitor.observe(sample(15, recovery=0, pos=20), 15)
        monitor.observe(sample(30, recovery=1, pos=35), 30)
        self.assertEqual(monitor.recovery_delta, 1)
        self.assertEqual(monitor.failures[0]["kind"], "unexpected_recovery")

    def test_recovery_baseline_ignores_prior_count_but_new_recovery_fails(self):
        monitor = soak.PlaybackMonitor(0)
        monitor.observe(sample(0, recovery=2), 0)
        monitor.observe(sample(15, recovery=2), 15)
        self.assertEqual(monitor.recovery_delta, 0)
        monitor.observe(sample(30, recovery=3), 30)
        self.assertEqual(monitor.recovery_delta, 1)
        self.assertEqual([f["kind"] for f in monitor.failures], ["unexpected_recovery"])

    def test_output_parameters_are_only_classified_while_playing(self):
        observation = soak.output_observation(sample(0))
        self.assertEqual(observation, {"video_params_present": True, "audio_params_present": True})
        self.assertIsNone(soak.output_observation(sample(0, paused=True)))

    def test_short_complete_run_and_interrupted_run_write_honest_summaries(self):
        def response(_url, route, _timeout):
            if route == "/api/health":
                return 200, {"liveness": "ok", "playback_ready": "true"}
            return 200, {"ready": True, "playback": {"state": "playing", "recovery_count": 0}}
        for interrupted in (False, True):
            with self.subTest(interrupted=interrupted), tempfile.TemporaryDirectory() as directory:
                args = SimpleNamespace(url=soak.DEFAULT_URL, output=directory,
                    duration=0.05, interval=0.025, socket=soak.DEFAULT_SOCKET)
                effect = RuntimeError("interrupted") if interrupted else response
                with patch.object(soak, "http_json", side_effect=effect), patch.object(
                        soak, "mpv_properties", return_value=sample(0)["mpv"]):
                    result = soak.run(args)
                summary = json.loads((Path(directory) / "summary.json").read_text())
                self.assertEqual(summary["complete"], not interrupted)
                self.assertEqual(summary["passed"], not interrupted)
                self.assertEqual(result, 1 if interrupted else 0)


if __name__ == "__main__":
    unittest.main()
