# gcl

A terminal-first Google Calendar viewer/editor for people who live in tmux.

`gcl` is a Go TUI that reads Google Calendar directly via the Calendar API,
reusing the OAuth credentials already configured for `gcalcli`.

It is designed for fast keyboard-driven navigation of personal, shared, and
team calendars, with direct event link opening, attendee-aware search, and
in-terminal event creation/editing.

## Why this exists

`gcalcli` is great for quick agenda output, but it becomes limiting when you
want all of the following at once:

- keyboard-native navigation in tmux
- direct link opening from events
- attendee-aware filtering and switching to another person's calendar
- week/month calendar-style views
- editing and creating events without leaving the terminal
- access to calendars that are readable via Google Calendar API even when they
  are not explicitly subscribed inside `gcalcli`

`gcl` keeps the good part of the existing setup — the already-authorized
OAuth credentials from `gcalcli` — and builds a richer interactive interface on
 top of the Google Calendar API.

## Features

### Calendar viewing

- Personal calendar viewing
- Shared/public/team calendar viewing
- Calendar alias support (user-managed dotfile — see [Configuration](#configuration))
- Direct calendar email / calendar ID lookup
- Recent calendar history for fast switching

### Navigation

- **List view** for dense agenda browsing
- **Week grid** view
- **Month grid** view
- Keyboard navigation for day/week movement
- Focus follows selection in list view
- Grid view keeps the calendar window stable while moving the cursor

### Event details

- Right/bottom split detail pane depending on pane geometry
- Event description display
- Location display
- Link list display
- Attendee list display
- Attendee response markers (`[y]` accepted, `[?]` tentative, `[n]` declined)
- Meeting-room / resource display (shown separately from the free-text location)

### Search and filtering

- Fuzzy event search with `/`
- Search across:
  - date
  - title
  - location
  - description
  - attendee emails
  - links
- `mine`, `@me`, `owner:me` style filters for focusing on your own events
- Fuzzy calendar picker
- Fuzzy attendee picker

### Actions

- Open the Google Calendar event page
- Open secondary links (`L`) such as Zoom/docs
- Jump to an attendee's calendar (`A`)
- Create a new event (`N`)
- Edit an event (`E`)
- Delete an event (`X`)
- Non-focus-stealing reminder toasts for upcoming events (in-app watcher or
  `--notify`), honoring each event's own reminder settings

## Authentication model

This app **does not** ask you to do a separate login flow.

Instead, it reuses the OAuth credentials stored by `gcalcli` at:

```text
~/Library/Application Support/gcalcli/oauth
```

At runtime it:

1. reads the stored OAuth credential pickle
2. extracts `client_id`, `client_secret`, and `refresh_token`
3. refreshes an access token against Google OAuth
4. calls the Google Calendar v3 API directly

This means:

- you keep using your existing `gcalcli` login setup
- calendars readable via API but not subscribed in `gcalcli` can still work
- attendees and event metadata are available more richly than the plain `gcalcli`
  agenda output

## Configuration

Calendar aliases are managed in a dotfile so you can add/remove your own
calendars without touching the code. Resolution order for the path:

1. `$GCL_CONFIG` (explicit override)
2. `$XDG_CONFIG_HOME/gcl/config`
3. `~/.config/gcl/config` (default)

On first run the file is auto-created with sensible defaults. Edit it freely —
it's a minimal INI:

```ini
# gcl config
[aliases]
me           = primary
team         = My Team Calendar
holidays     = xxxxx#holiday@group.v.calendar.google.com
oncall       = c_xxxxxxxxxxxxxxxxxxxxxxxxxxxx@group.calendar.google.com

[settings]
email            = you@example.com   # optional; auto-detected from primary calendar if blank
default_calendar = me                # opened at startup (overridden by --calendar)
default_step     = day               # list-view h/l step: day | week | month
event_time       = 10:00             # new-event default start time
event_duration   = 30                # new-event default duration (minutes)
timezones        = local, KST=Asia/Seoul, UTC   # cycled with the Z key
notify           = false             # in-app reminder watcher
notify_window    = 15                # fallback minutes for events with no reminder
notify_interval  = 30                # watcher scan interval (seconds)
```

Each alias value may be:

- `primary` (your account's default calendar) or an **email**
  (`someone@company.com`)
- a **raw calendar id** (`…@group.calendar.google.com`)
- a **calendarList display name** (e.g. `My Team Calendar`), which is resolved
  to the underlying id automatically at query time

`me` is special: its value is treated as your own calendar/identity for the
`mine` search filter and self display. Setting `email` in `[settings]`
overrides that identity explicitly.

The `[settings]` keys have safe built-in defaults, so every one is optional.
`timezones` is a comma-separated Z-cycle list; each item is an IANA zone,
optionally `Label=Zone`, and the system `local` zone is always kept first.

Aliases show up in the `e` calendar picker and can be passed to `--calendar` /
`-c`. Comments (`#`, `;`) and a leading `[aliases]` section header are supported;
keys are case-insensitive.

### Notifications

Reminders for upcoming events are delivered as **toasts that do not steal
focus** — a `tmux display-message` on the status line (inside tmux) and a macOS
desktop notification banner (`osascript`, when available). tmux `display-popup`
is intentionally *not* used because it is modal and grabs the keyboard.

Each event fires according to **its own Google Calendar reminder settings**
(`reminders.overrides`, or the calendar's default reminders when the event uses
`reminders.useDefault`). So an event set to remind 30 minutes before fires at
T-30, one set to 10 minutes fires at T-10, and multiple reminders on one event
each fire independently. `notify_window` is only a *fallback* for events that
carry no reminder at all.

Two ways to run them:

- **In-app watcher** (no cron needed): while the TUI is open it periodically
  scans the current calendar and toasts each event at its configured reminder
  time. Enable it in the `[settings]` section of the config:

  ```ini
  [settings]
  notify          = true   # turn the in-app watcher on
  notify_window   = 15     # FALLBACK minutes-before-start for events with no reminder set
  notify_interval = 30     # seconds between scans
  ```

- **One-shot** (cron/launchd friendly): `gcl --notify --calendar me`
  performs a single scan-and-toast and exits, honoring each event's reminders
  (`--notify-window` sets the fallback). Already-fired reminders are tracked in
  `~/.cache/gcl-notify-state` so the same reminder is not repeated.


## Requirements

- Go 1.25+
- `gcalcli` already initialized
- a valid `gcalcli` OAuth file at:

```text
~/Library/Application Support/gcalcli/oauth
```

If you don't have that yet, initialize your existing flow first, e.g. via:

```bash
gcal.sh init
```

or directly:

```bash
gcalcli init
```

## Getting started

### 1. Build

```bash
make build
```

### 2. Install

```bash
make install
```

This installs the binary to:

```text
~/.local/bin/gcl
```

### 3. Run

```bash
gcl
```

By default it opens your personal calendar (`me`).

### 4. Try a specific calendar

```bash
gcl --calendar me
gcl --calendar team
gcl --calendar someone@example.com
```

### 5. Debug with dump mode

```bash
gcl --dump --calendar me --date 2026-07-01
```

### 6. Upcoming event notifications in tmux

```bash
gcl --notify --calendar me --notify-window 15
```

If you are inside tmux, this sends `tmux display-message` notifications for
upcoming events in the given window.

## Keyboard guide

### Global

- `q` — quit
- `?` — help
- `Z` — cycle timezone (list configured via `timezones`; `local` first)
- `n` — jump to now (today if loaded, else the nearest upcoming event)
- `e` — calendar picker
- `/` — fuzzy search

### List view

- `h` / `l` — move backward/forward by current step
- `d` / `w` / `m` — set movement step to day/week/month
- `j` / `k` — move event selection
- `g` — switch to week grid
- `M` — switch to month grid

### Grid view

- `h` / `l` — move focus by day
- `j` / `k` — move focus by week
- `g` / `v` — return to list view
- `M` — month grid
- `tab` — move focus into the detail pane

### Event actions

- `Enter` / `o` — open the Google Calendar event page
- `L` — open the link picker (Zoom/docs/etc.)
- `A` — open attendee picker and jump to that person's calendar
- `N` — create a new event
- `E` — edit selected/current event
- `X` — delete selected/current event

## Create/edit workflow

Event create/edit uses a single modal form rather than a step-by-step wizard.

You can:

- navigate fields with `j` / `k` or `Tab` / `⇧Tab` (field-navigation mode)
- enter a field with `Enter` or `i`
- while editing a field, `j` / `k` type normally; use `Tab` / `⇧Tab` (or arrows) to move fields
- leave field-edit mode with `Esc`
- toggle attendees with `Space`
- submit with `Enter`

When attendees are present, event creation/update uses Google Calendar
`sendUpdates=all`, which sends invitation/update emails.

## Make targets

```bash
make build      # build ./gcl
make install    # install to ~/.local/bin/gcl
make run        # go run .
make dump       # dump today's events from me calendar
make fmt        # gofmt -w .
make test       # go test ./...
make lint       # go vet ./...
make check      # fmt + test + lint + build
make deps       # go mod tidy
make clean      # remove local build artifact
```

## Design notes

- The app prefers direct Google Calendar API access over shelling out to
  `gcalcli agenda` because API access is more complete and handles calendars not
  subscribed in `gcalcli` itself.
- `gcalcli` remains useful as the bootstrap OAuth source and as a fallback path.
- Layout tries to adapt to pane shape:
  - wide pane → detail split on the right
  - tall/narrow pane → detail split on the bottom
- The interface is optimized for tmux use, keyboard navigation, and dense event
  browsing.

## Limitations / future ideas

- better explicit organizer/owner filters beyond `mine`
- background prefetch with safer coalescing once the fetch queue is stabilized
- richer week/month grid packing
- attendee/room rendering tweaks for very crowded recurring schedules
- deeper tmux integration for persistent notifications
