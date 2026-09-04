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
- **Find a time** (`f`) across several people's calendars and book it — see
  [Find a time](#find-a-time-f)
- **Overlay** several people's calendars into one agenda (`O`) — see
  [Overlaying calendars](#overlaying-calendars-o)
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

# Find-a-time (`f`) defaults
slot_duration      = 30    # meeting length to place (minutes)
slot_step          = 30    # candidate start grid (minutes)
slot_search_days   = 14    # how many days ahead to sweep
slot_day_start     = 7     # earliest bookable local hour (0-23)
slot_day_end       = 24    # latest bookable local hour (1-24; 24 = midnight)
slot_skip_weekends = false # drop Saturday/Sunday candidates
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

### The hint bar shows only what applies

The bottom bar is assembled from the keys that would actually do something in
the current state, not from every key the app has. `E`/`X`/`y` appear once an
event is selected; `A` and `L` only when that event has attendees or links;
`t` only in the list view; `u` only when there is something to undo.

`? help` and `q quit` are pinned to the right and are never dropped. When the
window is still too narrow, whole hints are removed lowest-priority first
rather than the line being cut mid-word — a hint reading `E ed` teaches
nothing.

```text
 (empty calendar)
 h/l day  j/k select  n now  N new  e calendar  f find-a-time  / search   |  ? help  q quit

 (event selected, with attendees and links)
 h/l day  j/k select  n now  ret open  E edit  X del  y copy  A attendees  L links  …  |  ? help  q quit

 (narrow window)
 h/l day  j/k select  n now  ret open   |  ? help  q quit
```

A staged time change takes the whole bar, and what it would write stays pinned
there — `s` sends it to other people's calendars, so the resulting time must not
be the part that scrolls off.

`?` remains the complete reference: keys the bar omits (`M`, `Z`, `R`, `D`/`W`,
`tab`) are all listed there.

### Global

- `q` — quit (refused while a time change is unsaved — see below)
- `?` — help
- `Z` — cycle timezone (list configured via `timezones`; `local` first)
- `n` — jump to now (today if loaded, else the nearest upcoming event)
- `a` — **active now**: everything in effect at this moment
- `f` — **find a time**: pick people, book a mutually open slot
- `O` — **overlay**: merge several calendars into one agenda
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

### Overlaying calendars (`O`)

"Who is in this slot?" and "when is the team actually free?" are questions about
several calendars at once, and reading them one at a time answers neither. `O`
loads a set of calendars into the **same** agenda:

```text
  📅 3 calendars  O:edit   LIST | 2026-09-04 | step:day   🕓 local
 Fri Sep 04                        5 events · peak 2  0   3   6   9  12  15  18  21
   09:00-09:30 ● gavin    Standup                     ██
   10:00-11:00 ● jace.son 1:1 with lead                  ████
 ▸ 10:00-11:30 ● yuna.kim Design review                   █████
   14:00-15:00 ● gavin    Retro                                    ████
   15:00-17:00 ● jace.son Deep work block                            ███████

  ● gavin 2  ● jace.son 2  ● yuna.kim 1
```

Every row gains a color dot and the owner's name, and **the 24h timeline bars
are tinted per person** — so the shared axis shows not just *that* two events
overlap but *whose* they are. That reuses the machinery already there rather
than adding a column-per-person view, which would cost `people × column width`
and stop fitting after three or four of them.

Because the events land in the same list, **every existing view works on the
merged set**: the list agenda, the week and month grids, `/` search, the `a`
active-now panel, and the `t` overlap timeline all gain multi-person data with
no separate mode to learn.

- `space` — toggle a calendar; type a full email/id with no match to add one
- `Enter` — apply. **Enter with nothing picked turns the overlay off.**
- `O` again — re-opens the picker with the current set still selected, so
  adding a fourth person does not mean re-picking the first three
- `e` — picking a single calendar also turns the overlay off; you asked for
  that one calendar

The legend under the agenda maps color → person and prints each one's event
count. A calendar that **could not be read** is shown hollow with the reason
(`○ yuna.kim (no access)`) and named in the status line, because an overlay that
silently drops a calendar looks exactly like that person having nothing
scheduled — the most dangerous way this could fail.

**Which calendar an action hits.** Editing (`E`), deleting (`X`), duplicating
(`D`/`W`) and the staged moves (`)( }{ ><` then `s`) all act on the **selected
row's own** calendar, and its owner is named in the confirm step — patching a
colleague's event against your own calendar id would 404, or hit a same-titled
event on the wrong calendar. Creating (`N`) has no row to follow, so a new event
lands on the calendar you have open; the form header names it.

Reminder toasts keep scanning only your own calendar while an overlay is on:
they are for events you have to attend, and toasting a colleague's 1:1 because
their calendar is on screen would be noise.

At most 8 calendars can be overlaid — past the palette, every extra person
reuses a color and the dots stop identifying anyone, so a larger set is refused
rather than rendered as a lie. On a pane too narrow to carry both the name and
the timeline, the **name** gives way first (down to the bare dot): overlap is
the reason to overlay at all, and the legend still carries the mapping.

### Find a time (`f`)

Answers the question a calendar viewer is worst at: **when can these people
actually meet?** Doing it by hand means opening five calendars in five tabs and
diffing them by eye — exactly the work a computer should do.

`f` opens a two-step flow.

**Step 1 — who is coming.** A fuzzy picker over everyone seen on the loaded
events, your recent calendars, and the calendar currently open. You are
pre-selected (a meeting you schedule is one you attend).

```text
  ┌ Find a time · who? ─────────────────────────┐
  │ › jace                                      │
  │  2 picked  gavin.jeong, jace.son            │
  │ * jace.son@company.com                      │
  │   jaewon.kim@company.com                    │
  │ space toggle · Enter find slots · ESC cancel│
  └─────────────────────────────────────────────┘
```

- `space` — toggle the highlighted person. Type a full address with no match and
  `space` adds it directly, so someone outside the loaded events is still
  invitable.
- `Enter` — read everyone's free/busy and rank the open slots.

**Step 2 — pick a slot.** Slots where **everyone** is free come first (green
`✓4/4`); partial ones stay in the list (amber `3/4`) with the blocked people
named underneath, because a 3/4 slot at a good hour often beats no slot at all.

```text
  ┌ Find a time · 1h for 4 ─────────────────────┐
  │  next 14d · 07:00-24:00 · weekends included │
  │ ▸ Thu 09-04 10:00-11:00  ✓4/4               │
  │   Thu 09-04 15:00-16:00  ✓4/4               │
  │   Fri 09-05 09:30-10:30   3/4  +1?          │
  │                                             │
  │  busy: jace.son                             │
  │  unknown (no free/busy access): contractor  │
  └─────────────────────────────────────────────┘
```

- `j` / `k`, `g` / `G` — move
- `d` — cycle the meeting length (15/30/45/60/90/120m)
- `w` — toggle weekends
- `H` — open the day up to a full 24h and back to the configured hours
- `R` — re-read free/busy
- `esc` — back to the participant picker (the usual fix for a disappointing list)
- `Enter` — **prefill the new-event form** with that slot and those attendees

`Enter` deliberately hands off to the normal create form rather than booking
outright: the form is where the title, location, and the "invitation emails will
be sent to N people" warning already live, and a mis-picked slot that has
already mailed five people is not something `u` makes comfortable to undo. Type
a title, press `Enter`, confirm — and only then does anything reach Google.

Details worth knowing:

- **Free/busy, not event bodies.** The lookup uses the Calendar free/busy API,
  so it works for colleagues whose event details you cannot read.
- **Unreadable calendars are their own category.** Someone whose free/busy is
  not accessible is counted as neither free nor busy — the badge reads `✓3/3`
  with a separate `+1?`, never a `3/4` that would claim a conflict nobody
  observed. When a search finds nothing, the unreadable calendars are named, so
  "no slot" and "no slot that we could see" stay distinguishable.
- **Back-to-back is free.** Busy intervals are half-open: a meeting ending at
  14:00 does not block a 14:00 start.
- **One entry per gap.** A wide-open afternoon offers a single 13:00 slot, not
  13:00/13:30/14:00/…; each contiguous run of identical availability collapses
  to its earliest start.
- **The small hours are excluded by default** (`slot_day_start = 7`) rather than
  office hours being enforced, because a cross-timezone team's only shared
  window is often somebody's evening. `H` lifts even that; see
  [Configuration](#configuration) to change the defaults.

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
- `>` / `<` — move to the next / previous **calendar** day (wall-clock time
  preserved, so a move across a DST boundary keeps a 10:00 meeting at 10:00
  rather than sliding it to 11:00)

While a change is staged:

- the event's row shows the **new** time with a `!+45m` marker in amber
- the detail pane spells out `saved → staged` in full
- the bottom bar turns amber and spells out the **resulting time**, not just
  the delta: `UNSAVED +1d: Sep 04 10:00-11:00 -> Sep 05 10:00-11:00 | s SAVE to
  Google | esc discard`. The delta alone does not say what the event ends up
  as, and a narrow window has no detail pane to check it in.
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
for your own calendar hygiene. When the event has attendees, the save status
says so explicitly (`2 attendees NOT notified`), because moving a meeting
without telling the people in it is the kind of thing worth knowing you just
did. Use `E` when a change should notify people.
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

### Catching mistakes before they reach Google

Both create and edit stop at a confirmation step. It is the only screen between
a typo and other people's calendars, so it shows what you are actually about to
do — not a restatement of the form, which the eye slides straight over.

**Creating** shows the resolved time, the location, and the things that are hard
to see from the shorthand still sitting in the fields:

```text
  ┌ Create event? ───────────────────────────────────────────┐
  │  회고                                                     │
  │ Sat Aug 29  10:00 → 20:00 (10h)  local                   │
  │ ↻ repeats weekly — forever (add "x4" to limit it)        │
  │ ⚠ starts in the PAST (5d4h ago)                          │
  │ ⚠ runs for 10h — is that right?                          │
  │                                                          │
  │ ✉ Google will email 2 people                             │
  │ y/Enter create  |  n/ESC back to form                    │
  └──────────────────────────────────────────────────────────┘
```

**Editing** shows only what *changes*, old → new. A rename is one line; fields
you did not touch are not mentioned, because they are not what a mis-edit gets
wrong. Changes that reach other people — time moves, attendee add/removes — are
marked `!`:

```text
  ┌ Save changes to event? ──────────────────────────────────┐
  │  Team sync                                               │
  │ 2 change(s):                                             │
  │ ! Time      Fri Sep 04 10:00-11:00                       │
  │           → Sat Sep 05 14:00-14:30                       │
  │ ! Attendees 2 → 1  (-b)                                  │
  │                                                          │
  │ ✉ Google will email 2 people                             │
  └──────────────────────────────────────────────────────────┘
```

What gets called out:

- **A start in the past.** `-3d` typed for `+3d`, or a month that has already
  gone by. Nothing rejects it, so it is easy to land an event in last week
  without noticing.
- **An endless repeat.** `weekly` with no count creates an event that never
  ends — the one field where a mistake keeps multiplying after you have
  forgotten about it. `weekly x4` is not flagged.
- **Outlier durations** (≥8h or <5m). `8` meaning 8 minutes and `8h` meaning 8
  hours both look plausible in the field.
- **An unchanged edit.** Submitting a form you did not actually change would
  mail everyone for nothing.
- **Who gets mail.** Editing notifies the event's whole attendee list plus
  anyone you removed (they get a cancellation) — not just the people added.

None of these are blocked: each has a legitimate use (logging a retro after the
fact, an all-day workshop, an open-ended standup). They are warnings, shown in
amber, and the past-start and duration ones also appear live in the form itself
while you type — a date that resolved to last week is easiest to fix in the
field it came from.

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
