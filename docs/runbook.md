# Bokkie — Runbook

**For the owner and whoever is standing at the station PC.** This assumes no
programming knowledge. If something here does not match what you see on screen,
stop and call the developer rather than improvising — nothing in this system
loses data on its own, but a hurried fix can.

Two things worth knowing before anything else.

**Bokkie runs only while its window is open on the host computer.** After a
power cut, a restart, or a Windows update, **somebody has to open it again** —
it does not come back on its own, by design. Until they do, the counter
computers will say they cannot reach the page, and that is not a fault.

**Every payment and advance ever recorded is in one file**, `bokkie.db`, inside
the data folder. Backups are copies of that file. Restoring means putting a
copy back.

---

## Where things live

| What | Windows | macOS | Linux |
|---|---|---|---|
| Data folder | `%LOCALAPPDATA%\Bokkie` (or `C:\ProgramData\Bokkie`, kept from an older version) | `/Library/Application Support/Bokkie` | `/var/lib/bokkie` |
| The database | `<data folder>\bokkie.db` | `<data folder>/bokkie.db` | `<data folder>/bokkie.db` |
| Backups | `<data folder>\backups` | `<data folder>/backups` | `<data folder>/backups` |
| Logs | `<data folder>\logs` | `<data folder>/logs` | `<data folder>/logs` |
| Settings file | `<data folder>\config.yaml` | `<data folder>/config.yaml` | `<data folder>/config.yaml` |
| The program itself | `C:\Program Files\Bokkie\bokkie.exe` | `/usr/local/bin/bokkie` | `/usr/local/bin/bokkie` |

Run `bokkie backup -list` to have the program print the backup folder it is
actually using, rather than trusting the table above.

---

## Everyday

### Opening and closing the station

Bokkie runs in a window on the host computer, and only while that window is
open.

- **To open it:** click **Bokkie** in the Start menu, or the Bokkie icon on
  the desktop. A small Bokkie window appears. It says **Bokkie is running** in
  green once the counter computers can use it.
- **To close it:** close that window, or press **Stop Bokkie** on it. Either
  way a backup is taken on the way out, so closing it is itself a save. The
  window says *Stopping Bokkie…* while it does that and closes by itself.

The window shows whether Bokkie is running, the address for this computer and
the address the counter computers use, when the last backup was taken, and
where the records are. It has three buttons:

- **Open Bokkie in browser** — opens the system on this computer.
- **Run a check** — the self-check described under *When nobody can reach
  Bokkie*, in a window you can copy from.
- **Stop Bokkie** — stops it for every computer at the station and closes.

If the window says **Bokkie is not answering** in red, it is open but its own
address refuses to connect. Press **Run a check** and send the developer what
it says. If it says **Bokkie could not start**, the line under it says why.

Opening Bokkie a second time does not start a second copy; it brings the open
window to the front.

**Nothing opens it for you.** Not a restart, not a Windows update, not logging
in. That is deliberate — Bokkie is open when a person has opened it and at no
other time.

### Starting and stopping the service (macOS and Linux hosts only)

On a Mac or Linux host the service starts with the computer.

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
- **Manager** — an encoder, plus: managing users, changing settings, running and
  listing backups, reading the audit log, reading the four reports, and voiding
  a posted advance slip, payment or settlement.

Settling a farmer — charging the interest up to a cut-off date and applying the
palay or cash they brought in — is counter work, and either role can do it.

Under **Administration** in the sidebar, a manager will find **Users**,
**Audit log** and **Settings**.

### Managing accounts

On the **Users** screen, the ⋮ at the end of a row offers:

- **Make manager** / **Make encoder** — takes effect on that person's next
  click. They do not need to sign out.
- **Reset password…** — for somebody who has forgotten theirs. You do not need
  their old password. They are signed out of every machine and will need the new
  password to get back in. Tell them in person; do not write it on a note.
- **Deactivate** — the nearest thing to removing an account. They are signed out
  within a click and cannot sign back in. Nothing they did is removed, and the
  audit log still shows their name against it. **Accounts are never deleted**,
  so use this when somebody leaves.

Anyone can change their own password from the key icon at the bottom of the
sidebar, next to the lock and sign-out buttons. That one asks for the current
password first, and leaves you signed in where you are.

**Two things the app will refuse, on purpose:**

- You cannot deactivate or demote **yourself**. That click would take away both
  the screen and the way back, so somebody else has to do it.
- You cannot deactivate or demote the **last manager**. If you try, the app says
  so and tells you to make somebody else a manager first.

Between them, those two mean it should not be possible to lock the station out
of its own administration. If it somehow happens anyway — every manager account
deactivated, or none left — nobody inside the app can fix it and you will need
to call the developer.

### Letting the other computers in, or keeping them out

On the dashboard, under **This server**, a manager can switch **Network access**
on or off.

- **On** (normal) — the counter PCs and the phones on the station WiFi can reach
  the app, at `http://bokkie.local:8080`.
- **Off** — only the host computer can. Every other machine gets a page saying
  the station is set to the host machine only, and `bokkie.local` stops
  resolving until it is switched back on.

Three things worth knowing:

- **The switch only works at the host computer.** That is deliberate: switching
  it off from a counter PC would take away the app and the switch in the same
  click, and nobody would be able to put it back except at the host.
- **It takes effect immediately.** Nothing needs restarting, in either
  direction. Anyone mid-screen on another PC will find their next click refused.
- **It is not a lock.** It decides who is served, not who can reach the machine
  — the port stays open. It is the right tool for "the counter PCs should not be
  in here today", not for keeping out somebody who should not be on the station
  network in the first place. That is what the WiFi password is for.

### If someone is locked out

After five wrong passwords, that username is refused for fifteen minutes even if
the password is then typed correctly. Either wait it out, or restart the service
(table above) — restarting clears all lockouts immediately.

---

## Backups

A backup runs automatically every night at **21:00** station time, and keeps the
**14 most recent** copies. A manager can change both under **Settings →
Backup**; the change takes effect the same evening, with no restart.

The same screen lists what is in the backup folder, with the size of each file
and the time it was taken. The time shown is station time. **The timestamp
inside the file name is UTC** — eight hours behind — because the names have to
sort correctly in Windows Explorer and on any machine you plug the stick into.
So `bokkie-20260918-022238.db` is the backup the screen shows as 10:22 AM on 18
September. They are the same moment.

Each backup is checked the moment it is written — the program reopens the new
file and counts its rows against the live database. A backup that fails that
check is deleted rather than left looking like a good one.

### Take a backup right now

From the app, as a manager: **Settings → Backups → Back up now**.

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

### Windows — the installer

1. On the computer that will be the host, open
   **https://dannjann.github.io/bokkie-lms/** and click
   **Download Bokkie for Windows**. That is one file, `BokkieSetup.exe`.
2. Double-click it. Windows will say *"Windows protected your PC"* — **this is
   expected**, because the program is not signed by a certificate we pay a
   yearly fee for. Click **More info**, then **Run anyway**.
3. Approve the administrator prompt.
4. The Bokkie Setup wizard opens. Click **Next** through its steps:
   - **Choose components:** leave it on **Station computer**.
   - **Install location:** leave it as `C:\Program Files\Bokkie`.
   - **Install**, then **Finish**. Leave **Open Bokkie now** ticked.

The installer puts Bokkie in the Start menu and on the desktop, lets the
counter computers through Windows Firewall, and appears in Add/Remove Programs.
If an older Bokkie had been installed as a service, the installer removes that
service — Bokkie no longer starts by itself — and keeps its records.

It does **not** make Bokkie start with the computer. Somebody opens it each
morning.

### The counter computers

Run **the same file** on each counter PC. On the **Choose components** step,
pick **Counter PC** from the list. It then asks for the station's address —
type the one the Bokkie window on the station shows under *Other computers*.
It puts a **Bokkie** icon on the desktop, which opens the system in its own
window with no address bar and no tabs. Nothing else is installed.

If `bokkie.local` does not work on that PC, run the installer again and type
the number address instead, for example `http://192.168.1.50:8080`.

### macOS or Linux host

Download the release folder from
[the releases page](https://github.com/Dannjann/bokkie-lms/releases/latest),
copy the whole folder onto the computer, and double-click **Install
Bokkie.command**. Type the computer's password when asked.

### If the station has no internet

Download `BokkieSetup.exe` on a computer that has a connection, copy it onto a
USB stick, and run it on the host from there. It needs nothing else; the
program is inside it. (For macOS and Linux, copy the whole release folder
instead and use the `.command` files in it.)

### Removing it

Bokkie appears in **Add/Remove Programs** like any other program. Close the
Bokkie window first. Removing it takes the program, the Start menu entry, the
desktop icon and the firewall rule off the computer, and **leaves the data
folder and every backup in it exactly where they are**. Nothing is deleted;
installing again opens the same records.

---

## When nobody can reach Bokkie

**Run the check before anything else.** It changes nothing, needs no password,
and prints the whole picture at once:

- **The quick way:** press **Run a check** in the Bokkie window.
- **At a command prompt:** `bokkie doctor`

It tells you, in order: whether Bokkie is running on this computer, whether the
other computers are allowed in, whether the name `bokkie.local` points at this
machine, and whether it actually answers there. Anything it marks `FAIL` comes
with what to do about it. Send the developer the whole screen.

### The two addresses

Both of these reach the same system:

```
http://bokkie.local
http://bokkie.local:8080
```

The short one works because Bokkie also answers on the ordinary web port. If
something else on the host computer is already using that port, only the longer
one works — the check above says so plainly when that happens.

### "Refused to connect"

That message means the computer found the host but nothing answered. Almost
always one of:

- **Bokkie is stopped.** The check says so, and how to start it.
- **The address was typed without `:8080`** on a host where port 80 is taken.
- **Another machine on the network answers to `bokkie.local`.** The check
  compares what the name resolves to against this computer's own address and
  says when they differ.

If the name will not behave, every screen is still reachable by address —
`http://192.168.1.x:8080`. `bokkie doctor` prints this computer's address.

### "Another machine on this network is answering to that name"

Two computers on the station network are both calling themselves `bokkie.local`,
and the counter PCs are being sent to the wrong one. The check names both
addresses — the one the name resolves to, and the one this computer actually
has — so you can tell them apart.

This almost always means a second copy of Bokkie is running somewhere: a laptop
it was once installed on for testing, or an old host that was replaced and never
switched off. **Find that machine and stop Bokkie on it.** Until then the
station is still reachable by address, `http://192.168.1.x:8080`, which
`bokkie doctor` prints.

Bokkie also writes this into its own log within a few seconds of starting, so
the host says so without anybody asking.

### "Bokkie found its records but cannot write there"

This happens once, on a station upgraded from an older version. That version
ran as a Windows service and its records folder belongs to the service's
account rather than to you.

**Bokkie fixes this itself.** The window offers **Let Bokkie fix this** — choose
it, allow the permission prompt, and open Bokkie again the normal way. It does
not need permission after that, and nothing inside the folder is changed: every
advance, payment and backup stays exactly where it is.

If the prompt is refused, nothing has been changed and the same choice is there
next time.

### "Another program is already using port 8080"

Some other program on the host computer got there first. Bokkie refuses to
install over it rather than registering a service that cannot start, and
nothing on the computer has been changed.

Two ways out:

1. **Stop the other program**, then run the installer again.
2. **Give Bokkie a different port.** Open `C:\ProgramData\Bokkie\config.yaml`
   in Notepad, change the `port:` line to something else — `8081` is a fine
   choice — save it, and install again. Every address then uses that number
   instead: `http://bokkie.local:8081`, and the shortcut on each counter PC
   has to be made again so it points at the new one.

`bokkie doctor` says which of the two you are looking at: it tells a port that
nothing is on apart from a port that something else answers.

### Stopping and starting it

Close the Bokkie window to stop it; open Bokkie from the Start menu to start it
again. Stopping does not remove anything and does not touch the database.

---

## Updating to a new version

**Windows, the simple way:** close the Bokkie window (that takes a backup),
download the new `BokkieSetup.exe` from
**https://dannjann.github.io/bokkie-lms/**, and run it on the host computer
exactly as for a first install. It replaces the program and keeps the records.

**The careful way, on any host,** is `bokkie update` from a release folder, as
described below — it checks the signature itself and puts the old version back
if the new one will not start.

**What it does, in this order:** checks the folder really is a genuine bokkie
release and has not been altered; takes a full backup of the database and
verifies it; stops the service; puts the new version in place; starts it again;
and confirms the new version is answering. **If the new version does not come
up, it puts the old one back and starts that** — the station keeps working and
the update can wait for the developer.

It also checks **the file you downloaded** against the release, before it
touches anything. If it cannot — no internet, or a file that does not match —
**it refuses and changes nothing**, and tells you to download it again. A
working station is never replaced by a file that could not be checked.

If one of the numbers it shows you does not look right, or it says the file is
an older version than the one installed, stop and call the developer.

**From a USB stick instead,** when the station has no internet:

1. Copy the new folder onto the host computer.
2. **Windows:** close the Bokkie window, then open a Command Prompt **as
   administrator** and run
   `"C:\Program Files\Bokkie\bokkie.exe" update "D:\bokkie-v0.3.0"`, naming
   the folder you copied. (Or simply run the `BokkieSetup.exe` inside it.)
   **macOS:** double-click **Update Bokkie.command**.
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
did it, when, and from which computer. Managers can read it under
**Administration → Audit log**.

Narrow it by person, by what happened, by which record, or by a range of dates —
the dropdowns only offer things that have actually happened, so an empty list
means an empty list and not a typo. The arrow at the end of a row opens it to
show what the value was before and what it became, with the request number and
the computer's address underneath. **Load older** goes further back; the log
holds everything since the day the system was installed.

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
