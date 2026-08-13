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
- **24-hour overlap timeline** in the list view (`t`) — see below
- **Week grid** view
- **Month grid** view
- Keyboard navigation for day/week movement
- Focus follows selection in list view
- Grid view keeps the calendar window stable while moving the cursor

### Overlap timeline (`t`)

A flat agenda tells you what exists but not what runs *at the same time* — the
question a maintenance-window calendar is read for. Every list row gets a mini
00:00–24:00 bar on one shared axis, so overlapping events line up vertically:

```
 * Thu Aug 13  (today)
                        20 events · peak 9  0   3   6   9  12  15  18  21   <- density ruler
   02:30-07:30 [ap3]       ███████
   03:30-10:30 [dream11]     ██████████
   03:30-08:00 [nexon]       ██████
 ▸ 04:30-10:30 [ap5]          █████████                                     <- selected
   15:30-23:00 [doordash]                        ███████████│               <- │ = now
   21:00-04:30 [ap9]                                      ████>             <- past midnight
```

- The ruler under each date header shades **how many events run at once** in
  each column (cool → hot), scaled against the busiest column across every
  loaded day so the same color means the same load on every day on screen. The
  left gutter prints that day's event count and peak concurrency.
- `│` marks the current moment; `<` / `>` mark a window continuing past
  midnight — a 21:00–04:30 window is counted in the next morning's density too.
- Bars follow row state: blue while an event is active now, amber while a time
  change is staged but unsaved.
- Resolution adapts to pane width (1, 2, or 4 columns per hour). Panes too
  narrow to carry a bar without crushing the title simply don't get one.
- `t` toggles it; `timeline = false` in the config turns it off at startup.
  Bars are drawn with background-colored spaces, never block glyphs, so
  terminals that render box-drawing characters double-width stay aligned.

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
- Copy event summaries, Calendar URLs, timestamps, and descriptions with `y` combinations
- Create a new event (`N`)
- Edit an event (`E`)
- Delete an event (`X`)
- Staged time adjustments (`)( }{ ><`) previewed in the view and written only on
  `s` — see [Adjusting time](#adjusting-time-staged-then-saved)
- "Active now" digest (`a`) of every event in effect at this moment, including
  multi-day windows — see [Active now](#active-now-a)
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
timeline         = true              # list-view 24h overlap bars + density ruler (t toggles)
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

- `q` — quit (refused while a time change is unsaved — see below)
- `?` — help
- `Z` — cycle timezone (list configured via `timezones`; `local` first)
- `n` — jump to now (today if loaded, else the nearest upcoming event)
- `a` — **active now**: everything in effect at this moment
- `e` — calendar picker
- `/` — fuzzy search

### Active now (`a`)

Answers "what is in effect right now?" — the question a maintenance-window,
on-call, or PTO calendar gets asked constantly and which the agenda answers
badly. A window that started three days ago and ends tomorrow sits far above the
`-- now --` divider, scrolled out of sight and visually identical to something
long finished.

`a` **toggles a docked panel** above the schedule listing every event whose span
covers this moment, **ordered by what lapses first** so the one you may need to
extend is at the top:

```text
  📅 me  e:switch   LIST | 2026-07-27 | step:day   🕓 local  🔴 2 active a
  ◉ Active now (2) · 11:10   tab to focus
▸ ends in 40m  ·  [ap3] istiod upgrade to 1.29
    Jul 27 10:50-11:50  ·  ops-apne2
  ends in 1d12h49m  ·  apne2 maintenance window
    Jul 25 -> Jul 28 all-day

 Sat Jul 25
  all-day     apne2 maintenance window ◉now
 * Mon Jul 27  (today)
  10:50-11:50 [ap3] istiod upgrade to 1.29 ◉now 📍
 -- now 11:10 -----------------------------------
▸ 14:10-14:40 Standup
```

**It is a toggle, not a mode.** `a` does not move your cursor and does not take
focus: the schedule below keeps every key it had, so you glance at what is
running and carry on exactly where you were. `j`/`k`, `g`, `/`, `N`, the quick
actions — all unchanged with the panel open.

When you do want to work inside it, `tab` moves focus in:

- `tab` — cycle focus: schedule → day detail (grid views) → active panel → back.
  Panes that aren't on screen are skipped.
- `j` / `k` — move the panel's cursor (the schedule selection follows along)
- `Enter` — jump the schedule to that event and hand focus back
- `E` / `X` / `L` / `A` / `o` — act on the highlighted window. The panel doesn't
  intercept these: it keeps the schedule selection in sync, so they simply work.
- `esc` — leave the panel, **keeping it open**. `a` is what closes it.

Details:

- multi-day and all-day spans are handled properly (the exclusive API end date
  is not counted as active, so a 25→28 window stops being active on the 29th)
- the header carries a live `🔴 n active` count, so you see it without asking
- active events are also marked `◉now` in the agenda, grid, and detail pane
- the panel is capped at a third of the body height and scrolls if more windows
  are active than fit
- the count and countdowns refresh on their own (30s tick) — no keypress needed

### List view

- `h` / `l` — move backward/forward by current step
- `d` / `w` / `m` — set movement step to day/week/month
- `j` / `k` — move event selection
- `t` — toggle the 24h overlap timeline
- `g` — switch to week grid
- `M` — switch to month grid

### Grid view

- `h` / `l` — move focus by day
- `j` / `k` — move focus by week
- `g` / `v` — return to list view
- `M` — month grid
- `tab` — cycle focus (day detail pane, and the active panel when open)

### Event actions

- `Enter` / `o` — open the Google Calendar event page
- `L` — open the link picker (Zoom/docs/etc.)
- `A` — open attendee picker and jump to that person's calendar
- `N` — create a new event
- `E` — edit selected/current event
- `X` — delete selected/current event

### Copying event data (`y` combinations)

Press `y`, then a second key to copy data from the selected/current event:

- `yy` — a shareable summary (title, time, location, description, Calendar URL)
- `yu` — Google Calendar event URL
- `ys` — start timestamp
- `ye` — end timestamp
- `yd` — description
- `y` then `Esc` — cancel

Timed events use RFC3339 in the timezone currently selected with `Z`. All-day
events copy dates instead; their end date is the last covered day, not Google
Calendar's exclusive API boundary. If a time adjustment is staged, `yy`, `ys`,
and `ye` copy the time currently previewed in the TUI and mark the summary as
`UNSAVED`. Clipboard transfer uses OSC 52, so it works without invoking a
platform-specific clipboard process and can pass through SSH/tmux when the
terminal permits OSC 52 clipboard access.

### Adjusting time (staged, then saved)

Reschedule without opening the form. These keys **do not write to Google** — they
only change what the view shows, so you can compose an adjustment, look at it,
and then decide:

- `)` / `(` — move the event 15 minutes later / earlier (duration preserved)
- `}` / `{` — lengthen / shorten by 15 minutes (start preserved)
- `>` / `<` — move to the next / previous day (time of day preserved)

While a change is staged:

- the event's row shows the **new** time with a `!+45m` marker in amber
- the detail pane spells out `saved → staged` in full
- the bottom bar turns amber and reads
  `UNSAVED +45m on "Standup" | s SAVE to Google | esc discard`
- `q` refuses to quit, so an adjustment is never lost by accident

Then:

- `s` — save the staged change to Google (one patch, however many nudges)
- `esc` — discard it

Repeated nudges compose: `)))` is one `+45m` save, not three round trips.
Nudging back to the original time clears the staged change by itself. A failed
save keeps the change staged so `s` can retry. Only one event can carry a staged
change at a time; nudging a different one is refused rather than silently
dropping the first.

### Immediate actions

- `D` — duplicate the event in place (title gets a `(copy)` suffix)
- `W` — copy the event into the same slot next week
- `u` — undo the last create / edit / delete / save

These apply right away — there is no meaningful half-applied state to render for
a copy — and each is undoable with `u`.

Saving a staged change, duplicating, and undo never email attendees — they are
for your own calendar hygiene. Use `E` when a change should notify people.
All-day events cannot be nudged (they have no time to move), but they can be
duplicated.

## Create/edit workflow

Event create/edit uses a single modal form rather than a step-by-step wizard.

Fields: **Title**, **Date**, **Start**, **Duration**, **Location**, **Repeat**,
**Notes**, **Attendees**.

You can:

- navigate fields with `j` / `k` or `Tab` / `⇧Tab` (field-navigation mode)
- enter a field with `Enter` or `i`
- while editing a field, `j` / `k` type normally; use `Tab` / `⇧Tab` (or arrows) to move fields
- leave field-edit mode with `Esc`
- toggle attendees with `Space`
- in the Location field, cycle suggestions with `ctrl+n` / `ctrl+p` and accept with `ctrl+y`
  (suggestions come from locations and meeting rooms on already-loaded events)
- submit with `Enter` from any field

The form shows a live preview of the resolved day, start → end time, and
duration, so shorthand input is confirmed before you submit.

### Flexible date/time input

Date, Start, and Duration accept shorthand as well as the canonical forms:

| Field | Accepted |
|-------|----------|
| Date | `2026-07-20`, `07-20`, `7/20`, `0720`, `20` (day of this month), `today`/`tod`, `tomorrow`/`tmr`/`tom`, `yesterday`/`yst`, `+3d`, `-1d`, `2w`, `+1m`, `+1y`, `mon`…`sun`, `next fri`, and the Korean `오늘`/`내일`/`어제` |
| Start | `15:00`, `9:30`, `1530`, `930`, `15`, `3pm`, `3:30pm`, `11am`, `12am` (midnight), `12pm` (noon), `15시`, `9시30분` |
| Duration | `30`, `45m`, `90m`, `1h`, `1.5h`, `1h30m`, `1h30`, `1일`, `30분`, `1시간30분` |
| Repeat | empty/`none`, `daily`, `weekly`, `biweekly`, `monthly`, `yearly`, `weekdays`, Korean `매일`/`매주`/`격주`/`매월`/`평일`, plus an occurrence count: `weekly x4` |

Whatever you type is normalized into the canonical form on submit, so the field
text and the created event always agree. Weekday names resolve to the **next**
occurrence (typing `wed` on a Wednesday means next Wednesday).

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
- **Destructive-adjacent edits are staged, not immediate.** The nudge/resize/
  day-move keys used to patch Google on every keypress, which turned one mental
  adjustment into four round trips and put a mis-key on other people's calendars
  before you could see the result. They now change only the rendered time until
  `s`. Create/delete/duplicate stay immediate: there is no coherent
  half-applied state to render for those, and `u` already covers them.
- **"Active now" is a first-class view, not a search.** A span covering the
  present moment is what an operations calendar is read for, and the agenda is
  structurally bad at surfacing it (long windows scroll above the `now`
  divider). The count lives in the header so it needs no query.
- **The active panel is docked and non-modal.** It started as an overlay that
  grabbed every key, which was wrong: checking what is running is a *glance*,
  not a task you stop to do. Docking it above the schedule means toggling it
  costs nothing — no lost cursor position, no keys to re-learn — and `tab` is
  there for the rarer case where you want to work through the list. Panel
  navigation keeps the schedule selection in sync rather than intercepting the
  action keys, so `E`/`X`/`L` need no panel-specific code path.

- **The timeline transposes the calendar rather than reproducing it.** Google
  Calendar shows overlap with vertical lanes, which cost `concurrent events ×
  lane width` columns — 25 simultaneous maintenance windows fit in no terminal.
  Rotating the time axis 90° keeps one row per event (so titles stay readable
  and the existing agenda keys still work) while making overlap visible as
  vertical alignment. The density ruler carries the number the bars can't: bars
  show *when* things overlap, the ruler shows *how much*.
- **The density ramp is relative, not absolute.** Scaling to the busiest column
  across all loaded days keeps colors comparable between days on screen; the
  per-day gutter prints the actual peak so the number is never lost. A ramp
  normalized per day would make a quiet day look as busy as a stacked one.
- **Nothing is drawn with block-drawing glyphs.** U+2580–259F are
  East-Asian-ambiguous: some terminals render them two cells wide, which
  desyncs the whole frame. Background-colored spaces are always exactly one
  cell, so the bars stay aligned everywhere (this is the same constraint that
  keeps `detailStyle` borderless).

## Limitations / future ideas

- better explicit organizer/owner filters beyond `mine`
- background prefetch with safer coalescing once the fetch queue is stabilized
- richer week/month grid packing
- attendee/room rendering tweaks for very crowded recurring schedules
- deeper tmux integration for persistent notifications
