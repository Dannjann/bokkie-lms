# Bokkie — Runbook

**For the owner and whoever is standing at the station PC.** This assumes no
programming knowledge. If something here does not match what you see on screen,
stop and call the developer rather than improvising — nothing in this system
loses data on its own, but a hurried fix can.

The one thing worth knowing before anything else: **every payment and advance
ever recorded is in one file**, `bokkie.db`, inside the data folder. Backups are
copies of that file. Restoring means putting a copy back.

---

## Where things live

| What | Windows | macOS | Linux |
|---|---|---|---|
| Data folder | `C:\ProgramData\Bokkie` | `/Library/Application Support/Bokkie` | `/var/lib/bokkie` |
| The database | `<data folder>\bokkie.db` | `<data folder>/bokkie.db` | `<data folder>/bokkie.db` |
| Backups | `<data folder>\backups` | `<data folder>/backups` | `<data folder>/backups` |
| Logs | `<data folder>\logs` | `<data folder>/logs` | `<data folder>/logs` |
| Settings file | `<data folder>\config.yaml` | `<data folder>/config.yaml` | `<data folder>/config.yaml` |
| The program itself | `C:\Program Files\Bokkie\bokkie.exe` | `/usr/local/bin/bokkie` | `/usr/local/bin/bokkie` |

Run `bokkie backup -list` to have the program print the backup folder it is
actually using, rather than trusting the table above.

---

## Everyday

### Starting and stopping the service

The service starts with the computer. You should not need to touch it.

| | Windows | macOS | Linux |
|---|---|---|---|
| Stop | `sc stop bokkie` | `sudo launchctl bootout system/bokkie` | `sudo systemctl stop bokkie` |
| Start | `sc start bokkie` | `sudo launchctl bootstrap system /Library/LaunchDaemons/bokkie.plist` | `sudo systemctl start bokkie` |
| Check | `sc query bokkie` | `sudo launchctl print system/bokkie` | `sudo systemctl status bokkie` |

The Windows and Linux commands above follow from the service name `bokkie` but
have not been run on a real host yet — no Windows or Linux machine has been
available. The macOS ones have been exercised. Treat the other two as very
likely right rather than proven.

To check it is answering, open `http://bokkie.local:8080/healthz` in a browser.
A line containing `"status":"ok"` means the server and its database are fine.

### Getting in

- Open `http://bokkie.local:8080` on any computer or phone on the station's
  network. If the name does not work, use the host computer's IP address
  instead: `http://192.168.1.x:8080`.
- **The very first time only**, the app shows a "Set up Bokkie" page. Create the
  manager account there. That page never appears again.
- **Lock** (top right) is for stepping away from the counter. It signs you out
  but keeps your username on the login screen, so coming back is just your
  password. Use it rather than leaving the screen open.
- **Sign out** is for handing the PC to someone else.

### Two kinds of user

- **Encoder** — the everyday counter role.
- **Manager** — an encoder, plus: adding users, changing settings, running and
  listing backups, reading the audit log, and (from a later version) voiding a
  posted voucher.

Only a manager can create users. If you are locked out of every manager account,
call the developer; there is no password reset built into the app.

### If someone is locked out

After five wrong passwords, that username is refused for fifteen minutes even if
the password is then typed correctly. Either wait it out, or restart the service
(table above) — restarting clears all lockouts immediately.

---

## Backups

A backup runs automatically every night at **21:00** station time, and keeps the
**14 most recent** copies. A manager can change both from the app's settings;
the change takes effect the same evening, with no restart.

Each backup is checked the moment it is written — the program reopens the new
file and counts its rows against the live database. A backup that fails that
check is deleted rather than left looking like a good one.

### Take a backup right now

From the app, as a manager: **Backup now**.

From the command line on the host:

```
bokkie backup
```

This is safe to run while people are using the system.

### Copy backups off the machine

A backup on the same disk as the original protects against a mistake, not
against the disk failing. Point the backup folder at a USB stick:

1. Plug the stick in and note its path (`E:\bokkie-backups`, `/Volumes/USB/bokkie-backups`).
2. On the host, run:

   ```
   bokkie backup -set-folder "E:\bokkie-backups"
   ```

   It writes a test file to that folder and reads it back before saving
   anything, so a mistyped path or an unplugged drive is refused there and then
   rather than discovered at 21:00 by nobody.

3. Restart the service (table above). The folder is read when the service
   starts, so until you do, backups still go to the old place.
4. Run `bokkie backup` and confirm the file lands on the stick.

Leave the stick plugged in. If it is missing at 21:00 the backup fails and says
so in the log; the system keeps running normally.

### Check a backup is good

```
bokkie backup -verify "E:\bokkie-backups\bokkie-20260915-210000.db"
```

It prints the row counts, or explains what is wrong with the file.

---

## Installing on a new computer

The developer hands over a folder — on a USB stick, or copied onto the machine.
Copy the whole folder onto the computer first; do not run it from the stick.

- **Windows:** double-click **Install Bokkie.cmd**. Windows will say *"Windows
  protected your PC"* — this is expected, because the program is not signed by
  a certificate we pay a yearly fee for. Click **More info**, then **Run
  anyway**. Then approve the administrator prompt.
- **macOS:** double-click **Install Bokkie.command** and type the computer's
  password when asked.

It prints the address to open when it finishes. If it does not finish, nothing
has been removed — send the developer everything on the screen.

On each counter PC, double-click **Create Bokkie Shortcut.cmd** once. That puts
a **Bokkie** icon on the desktop which opens the system in its own window, with
no address bar and no tabs.

---

## Updating to a new version

The developer hands over a new folder the same way. Updating is one
double-click, and the program does the careful part itself.

**What it does, in this order:** checks the folder really is a genuine bokkie
release and has not been altered; takes a full backup of the database and
verifies it; stops the service; puts the new version in place; starts it again;
and confirms the new version is answering. **If the new version does not come
up, it puts the old one back and starts that** — the station keeps working and
the update can wait for the developer.

1. Copy the new folder onto the host computer.
2. **Windows:** double-click **Update Bokkie.cmd**. **macOS:** double-click
   **Update Bokkie.command**.
3. It shows you which version is in the folder and asks you to confirm. Type
   `y` and press Enter.
4. When it finishes, open `http://bokkie.local:8080` and check the version on
   the home page matches what it just installed.

To look at a folder without changing anything — worth doing on a phone call
before you commit to it:

```
bokkie update -check "D:\bokkie-v0.3.0"
```

That says whether the folder is genuine and what version is in it, and touches
nothing.

**If an update fails**, read the message. If it says the previous version was
put back, the station is running normally on the old version — take a note and
call the developer, there is no hurry. If it says anything else, call the
developer before touching the machine again.

---

## Restoring after a disaster

Use this when the database is lost or damaged — the app will not start, or
`/healthz` says the database is not `ok`.

**Everything entered since the backup you restore will be gone.** Pick the
newest backup that verifies.

1. **Find the newest backup and check it.**

   ```
   bokkie backup -list
   bokkie backup -verify "<the last path it printed>"
   ```

   If it does not verify, try the one before it.

2. **Stop the service.** (Table above.) Nothing else may be writing to the
   database during a restore.

3. **Move the damaged database aside — do not delete it.** In the data folder,
   rename `bokkie.db` to `bokkie.db.broken`, and do the same for
   `bokkie.db-wal` and `bokkie.db-shm` if they are there. A developer may be
   able to recover something from them later.

4. **Copy the backup into place.** Copy the verified backup file into the data
   folder and rename the copy to exactly `bokkie.db`. Copy it — do not move it —
   so the backup folder still has its copy.

5. **Start the service.**

6. **Check it.** Open `http://bokkie.local:8080/healthz` and confirm
   `"database":"ok"`. Sign in and confirm the most recent transactions you
   expect to see are there.

7. **Take a fresh backup immediately**, so there are two good copies again:

   ```
   bokkie backup
   ```

Everyone stays signed in through a restore — sessions are stored in the database
too, so a restored copy brings them back.

---

## The audit log

Every change, every refused action, and every failed login is recorded with who
did it, when, and from which computer. Managers can read it in the app.

Nothing is ever removed from it. It is not possible to edit or delete an entry —
not from the app, and not by anyone typing SQL at the database; the database
itself refuses. That is deliberate: its whole value is on the day someone
disputes something from two seasons ago.

---

## When to call the developer

- The app will not start, or `/healthz` does not say `ok` after a restore.
- The newest backups do not verify.
- A balance looks wrong. Do not try to correct it by editing anything — the
  audit log and the ledger are the evidence of what happened, and changing them
  destroys the only record of the mistake.
- An update failed with anything other than "the previous version was put back".

Have ready: what you were doing, the **request ID** if an error appeared on
screen, and the day's log file from the `logs` folder.
