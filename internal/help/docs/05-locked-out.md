# Locked out? Three ways back in

Operators get locked out — somebody set a strong password, then went on
vacation. Here's how to recover.

## 1. Rescue password (easiest)

The Pi accepts a rescue password in addition to whatever password you set.
It's controlled by the `JERRY_RESCUE_PASSWORD` environment variable.

In dev, the default is `jerry-rescue` (set in `scripts/reload.sh`). On a
production Pi, the value is whatever was baked into the systemd unit when
the `.img` was built — note it down somewhere safe before deployment.

Just log in as `admin` with that password. Every rescue login is logged
distinctly:

```
[jerry] login OK (RESCUE) for user="admin" from 192.168.1.50
```

Once in, set a new regular password from the admin UI.

## 2. Reset CLI

SSH into the Pi (or open a terminal) and run:

```
./little-jerrys --reset-password
```

This wipes the stored hash. The next login uses `admin` / `admin` and goes
through the forced-password-change flow.

## 3. Hand-edit the state file

In a real emergency:

```
sudo systemctl stop sienfeld
sudo nano /media/usb/.jerry/state.json
# delete the admin_password_hash field
# set password_is_default: true
sudo systemctl start sienfeld
```

The next login will use `admin` / `admin`.

## Why not just hardcode admin/admin forever?

The forced password change isn't there to be annoying — it's there because a
default-credential appliance on a restaurant WiFi network is a real risk.
The rescue password is the safety net.
