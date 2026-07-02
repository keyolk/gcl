package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// defaultAliases seeds a fresh config file so users start from a working example
// and can edit/extend it. The value may be an email, a raw calendar id, or a
// calendarList display name (resolved to an id at query time). "me" is special:
// its value is treated as the user's own identity (see appSettings.identity).
var defaultAliases = map[string]string{
	"me": "primary",
}

// appSettings holds tunables from the config's [settings] section.
type appSettings struct {
	notify         bool   // in-app watcher: toast upcoming events while the TUI runs
	notifyWindow   int    // fallback minutes-before-start for events with NO reminder set
	notifyInterval int    // seconds between in-app watcher scans
	identity       string // the user's own email, for "mine" filters and self display

	defaultCalendar string     // calendar opened at startup (alias/email/id)
	defaultStep     string     // list-view h/l step: "day" | "week" | "month"
	eventTime       string     // default start time for new events (HH:MM)
	eventDuration   int        // default duration for new events (minutes)
	timezones       []tzOption // timezones cycled with the Z shortcut
}

// settings is populated by loadConfig; defaults are conservative (watcher off).
var settings = appSettings{
	notify:          false,
	notifyWindow:    15,
	notifyInterval:  30,
	defaultCalendar: "me",
	defaultStep:     "day",
	eventTime:       "10:00",
	eventDuration:   30,
	timezones:       defaultTimezones(),
}

// defaultTimezones is the built-in Z-cycle list. "local" (system tz) is always
// first; the rest are common fallbacks users can override via config.
func defaultTimezones() []tzOption {
	return []tzOption{
		{label: "local", zone: ""},
		{label: "UTC", zone: "UTC"},
	}
}

// appName is the program's canonical name, used for the binary, the config
// directory (~/.config/<appName>/config), cache file names, and the
// <APPNAME>_CONFIG env override. Change it here to rebrand everything.
const appName = "gcl"

// legacyAppName is the previous name; its config/cache files are migrated to the
// current appName's locations on first run so upgrades are seamless.
const legacyAppName = "gcal-tui"

// configEnvVar is the environment override for the config path, derived from the
// app name (e.g. GCL_CONFIG).
func configEnvVar() string {
	return strings.ToUpper(appName) + "_CONFIG"
}

// configPath resolves the dotfile location, honoring <APPNAME>_CONFIG and the
// XDG base-directory spec, defaulting to ~/.config/<appName>/config.
func configPath() string {
	if v := strings.TrimSpace(os.Getenv(configEnvVar())); v != "" {
		return v
	}
	base := xdgConfigHome()
	return filepath.Join(base, appName, "config")
}

// xdgConfigHome returns $XDG_CONFIG_HOME or ~/.config.
func xdgConfigHome() string {
	if v := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

// cacheDir returns the base cache directory ($XDG_CACHE_HOME or ~/.cache).
func cacheDir() string {
	if v := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache")
}

// cachePath returns a per-app cache file path, e.g. ~/.cache/gcl-<name>.
func cachePath(name string) string {
	return filepath.Join(cacheDir(), appName+"-"+name)
}

// legacyConfigPath is where the previous name kept its config, used for one-time
// migration. Empty when the env override is in effect (nothing to migrate).
func legacyConfigPath() string {
	if strings.TrimSpace(os.Getenv(configEnvVar())) != "" {
		return ""
	}
	return filepath.Join(xdgConfigHome(), legacyAppName, "config")
}

// loadConfig populates calendarAliases from the dotfile. When the file is
// missing it seeds one with the defaults so the user has an editable starting
// point; if seeding fails (e.g. read-only home) it falls back to the defaults
// in memory. Returns the path that was used and any non-fatal error.
func loadConfig() (string, error) {
	path := configPath()
	data, err := os.ReadFile(path)
	if err != nil && os.IsNotExist(err) {
		// New path missing: try migrating a previous-name config into place.
		if migrated, mErr := migrateLegacyConfig(path); mErr == nil && migrated {
			data, err = os.ReadFile(path)
		}
	}
	if err != nil {
		if !os.IsNotExist(err) {
			// Unreadable for some other reason — fall back to defaults but report.
			calendarAliases = cloneAliases(defaultAliases)
			return path, fmt.Errorf("read config %s: %w", path, err)
		}
		// Still missing: seed a default file (best effort) and use the defaults.
		calendarAliases = cloneAliases(defaultAliases)
		if seedErr := writeDefaultConfig(path); seedErr != nil {
			return path, fmt.Errorf("seed config %s: %w", path, seedErr)
		}
		return path, nil
	}
	parsed, set := parseConfig(data)
	settings = set
	if len(parsed) == 0 {
		// Empty/aliasless file: keep defaults rather than losing every alias.
		calendarAliases = cloneAliases(defaultAliases)
		return path, nil
	}
	calendarAliases = parsed
	return path, nil
}

// migrateLegacyConfig copies a previous-name config file to the new path when
// the new one doesn't exist yet. Returns true if a file was migrated.
func migrateLegacyConfig(newPath string) (bool, error) {
	legacy := legacyConfigPath()
	if legacy == "" || legacy == newPath {
		return false, nil
	}
	data, err := os.ReadFile(legacy)
	if err != nil {
		return false, err // missing legacy file is fine (caller ignores err)
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(newPath, data, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// parseConfig reads a minimal INI: `#`/`;` comments, `[section]` headers, and
// `key = value` pairs. Keys under the top level or `[aliases]` become calendar
// aliases; keys under `[settings]` tune the watcher. Unknown sections are
// ignored so the format can grow later.
func parseConfig(data []byte) (map[string]string, appSettings) {
	aliases := map[string]string{}
	set := settings // start from defaults
	section := ""
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:eq]))
		val := strings.TrimSpace(line[eq+1:])
		// A quoted value is taken verbatim (may contain '#', spaces). An unquoted
		// value has a trailing inline comment stripped, but only when the comment
		// marker is preceded by whitespace — so calendar ids that legitimately
		// contain '#' (e.g. "…#holiday@group.v.calendar.google.com") survive.
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		} else if i := inlineCommentIndex(val); i >= 0 {
			val = strings.TrimSpace(val[:i])
		}
		if key == "" || val == "" {
			continue
		}
		switch section {
		case "settings":
			applySetting(&set, key, val)
		default: // "" or "aliases"
			aliases[key] = val
		}
	}
	return aliases, set
}

// inlineCommentIndex returns the index of a trailing inline comment marker
// (# or ;) that is preceded by whitespace, or -1 if none. Requiring a leading
// space means '#'/';' inside a value (e.g. a calendar id) is not treated as a
// comment.
func inlineCommentIndex(val string) int {
	for i := 1; i < len(val); i++ {
		if (val[i] == '#' || val[i] == ';') && (val[i-1] == ' ' || val[i-1] == '\t') {
			return i
		}
	}
	return -1
}

// applySetting maps one [settings] key/value onto the settings struct.
func applySetting(s *appSettings, key, val string) {
	switch key {
	case "notify":
		s.notify = parseBool(val, s.notify)
	case "notify_window", "notify_window_minutes":
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			s.notifyWindow = n
		}
	case "notify_interval", "notify_interval_seconds":
		if n, err := strconv.Atoi(val); err == nil && n >= 5 {
			s.notifyInterval = n
		}
	case "email", "identity":
		s.identity = strings.ToLower(strings.TrimSpace(val))
	case "default_calendar", "calendar":
		s.defaultCalendar = val
	case "default_step", "step":
		if v := strings.ToLower(val); v == "day" || v == "week" || v == "month" {
			s.defaultStep = v
		}
	case "default_event_time", "event_time":
		if isHHMM(val) {
			s.eventTime = val
		}
	case "default_event_duration", "event_duration":
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			s.eventDuration = n
		}
	case "timezones", "timezone", "tz":
		if tzs := parseTimezones(val); len(tzs) > 0 {
			s.timezones = tzs
		}
	}
}

// isHHMM reports whether s looks like "HH:MM".
func isHHMM(s string) bool {
	if len(s) != 5 || s[2] != ':' {
		return false
	}
	h, err1 := strconv.Atoi(s[:2])
	m, err2 := strconv.Atoi(s[3:])
	return err1 == nil && err2 == nil && h >= 0 && h < 24 && m >= 0 && m < 60
}

// parseTimezones builds the Z-cycle list from a comma-separated spec. Each item
// is an IANA zone (e.g. "Asia/Seoul") optionally labelled as "Label=Zone"
// (e.g. "KST=Asia/Seoul"); the special value "local" means the system tz. A
// "local" entry is always ensured at the front so the cycle can return home.
func parseTimezones(spec string) []tzOption {
	var out []tzOption
	haveLocal := false
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		label, zone := part, part
		if eq := strings.IndexByte(part, '='); eq >= 0 {
			label = strings.TrimSpace(part[:eq])
			zone = strings.TrimSpace(part[eq+1:])
		}
		if strings.EqualFold(zone, "local") || strings.EqualFold(part, "local") {
			haveLocal = true
			out = append(out, tzOption{label: "local", zone: ""})
			continue
		}
		// Derive a short label from the zone's last path segment if unlabelled.
		if label == zone {
			if i := strings.LastIndexByte(zone, '/'); i >= 0 {
				label = zone[i+1:]
			}
		}
		out = append(out, tzOption{label: label, zone: zone})
	}
	if len(out) > 0 && !haveLocal {
		out = append([]tzOption{{label: "local", zone: ""}}, out...)
	}
	return out
}

func parseBool(val string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

// writeDefaultConfig creates the config directory and a commented starter file.
func writeDefaultConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultConfigText()), 0o644)
}

// defaultConfigText renders a self-documenting starter config from defaultAliases.
func defaultConfigText() string {
	var b strings.Builder
	b.WriteString("# " + appName + " config\n")
	b.WriteString("#\n")
	b.WriteString("# Calendar aliases picked in the `e` picker and via --calendar/-c.\n")
	b.WriteString("# Each value may be an email, a raw calendar id\n")
	b.WriteString("# (…@group.calendar.google.com), or a calendarList display name\n")
	b.WriteString("# (resolved to an id automatically at query time).\n")
	b.WriteString("#\n")
	b.WriteString("# Edit freely — this file was auto-generated on first run.\n")
	b.WriteString("\n[aliases]\n")
	b.WriteString("# 'me' = your primary calendar. Add your own, e.g.:\n")
	b.WriteString("#   team     = Team Calendar Name\n")
	b.WriteString("#   holidays = xxxxx#holiday@group.v.calendar.google.com\n")

	keys := make([]string, 0, len(defaultAliases))
	for k := range defaultAliases {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	width := 0
	for _, k := range keys {
		if len(k) > width {
			width = len(k)
		}
	}
	for _, k := range keys {
		fmt.Fprintf(&b, "%-*s = %s\n", width, k, defaultAliases[k])
	}

	b.WriteString("\n[settings]\n")
	b.WriteString("# email: your address, used for the \"mine\" search filter and self\n")
	b.WriteString("# display. Leave blank to auto-detect from your primary calendar.\n")
	b.WriteString("email            =\n")
	b.WriteString("\n")
	b.WriteString("# default_calendar: alias/email opened at startup (overridden by --calendar).\n")
	fmt.Fprintf(&b, "default_calendar = %s\n", settings.defaultCalendar)
	b.WriteString("# default_step: list-view h/l movement — day | week | month.\n")
	fmt.Fprintf(&b, "default_step     = %s\n", settings.defaultStep)
	b.WriteString("\n")
	b.WriteString("# New-event defaults.\n")
	fmt.Fprintf(&b, "event_time       = %s   # default start time (HH:MM)\n", settings.eventTime)
	fmt.Fprintf(&b, "event_duration   = %d     # default duration (minutes)\n", settings.eventDuration)
	b.WriteString("\n")
	b.WriteString("# timezones: comma-separated list cycled with the Z key. Each item is an\n")
	b.WriteString("# IANA zone, optionally 'Label=Zone'. 'local' (system tz) is always kept\n")
	b.WriteString("# first. Example: timezones = KST=Asia/Seoul, PST=America/Los_Angeles, UTC\n")
	fmt.Fprintf(&b, "timezones        = %s\n", formatTimezones(settings.timezones))
	b.WriteString("\n")
	b.WriteString("# In-app reminder watcher: while the TUI is open, upcoming events in the\n")
	b.WriteString("# current calendar are announced as non-focus-stealing toasts (tmux\n")
	b.WriteString("# status message + macOS desktop notification). Each event fires per its\n")
	b.WriteString("# own Google Calendar reminder settings (e.g. 10m/30m/1d before).\n")
	fmt.Fprintf(&b, "notify           = %t\n", settings.notify)
	fmt.Fprintf(&b, "notify_window    = %d   # fallback minutes-before-start for events with NO reminder set\n", settings.notifyWindow)
	fmt.Fprintf(&b, "notify_interval  = %d   # seconds between scans\n", settings.notifyInterval)
	return b.String()
}

// formatTimezones renders a timezones list back to the config spec form.
func formatTimezones(tzs []tzOption) string {
	parts := make([]string, 0, len(tzs))
	for _, t := range tzs {
		switch {
		case t.zone == "":
			parts = append(parts, "local")
		case t.label != "" && t.label != t.zone:
			parts = append(parts, t.label+"="+t.zone)
		default:
			parts = append(parts, t.zone)
		}
	}
	return strings.Join(parts, ", ")
}

func cloneAliases(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
