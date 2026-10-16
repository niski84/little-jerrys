# little-jerrys API Endpoints

Base URL: `http://localhost:8089`

## GET /api/health
Service liveness check.

```json
{"status": "ok", "service": "little-jerrys"}
```

## SD library and automatic USB import

These routes require an authenticated session. The retry action also requires
completion of the first-login password change.

- `GET /api/library/status`: snapshot of phase, current file/bytes, free space,
  180-entry episode checklist, and per-file results/errors. Returns 404 if
  `JERRY_IMPORT_ROOT` is not configured.
- `GET /settings/library`: live HTML status section used by Settings.
- `POST /settings/library/rescan`: queue a serialized scan/retry, then redirect
  to Settings. Repeated requests are coalesced; existing SD episodes are preserved.

Fresh release images import from `/media/usb` to `/opt/jerry/media`. The source
must be separate from the destination. See `docs/PI-RELEASE.md`.
