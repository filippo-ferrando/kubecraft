// Package servers holds server validation, lifecycle actions, and quota-checked changes.
package servers

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidationError is returned for bad user input; its message is safe to show.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

var slugRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,30}[a-z0-9])?$`)

// ReservedSlugs cannot be used as server names.
var ReservedSlugs = []string{
	"www", "api", "admin", "mc-router", "router", "mine", "mail", "smtp", "ftp", "ns1", "ns2",
	"dashboard", "manager", "kubecraft", "status", "static", "assets", "login", "localhost",
}

func ValidateSlug(slug string) error {
	if !slugRe.MatchString(slug) {
		return invalid("name must be 1-32 characters of lowercase letters, digits, or '-', and cannot start or end with '-'")
	}
	if slices.Contains(ReservedSlugs, slug) {
		return invalid("name %q is reserved", slug)
	}
	return nil
}

// Server types supported by the itzg image, keyed by the TYPE env value.
var Types = []string{"VANILLA", "PAPER", "PURPUR", "FABRIC", "FORGE", "NEOFORGE"}

// SupportsContent reports whether a server type can load mods or plugins.
func SupportsContent(t string) bool { return t != "VANILLA" }

func ValidateType(t string) error {
	if !slices.Contains(Types, t) {
		return invalid("unknown server type %q", t)
	}
	return nil
}

// Releases (1.21.4, 26.1), pre-releases, release candidates, and snapshots (24w14a).
var versionRe = regexp.MustCompile(`^(\d{1,2}\.\d{1,2}(\.\d{1,2})?(-(pre|rc)\d{1,2})?|\d{2}w\d{2}[a-z])$`)

func ValidateVersion(v string) error {
	if v == "LATEST" || versionRe.MatchString(v) {
		return nil
	}
	return invalid("version must be LATEST or a Minecraft version like 1.21.4")
}

func ValidateDisplayName(n string) error {
	n = strings.TrimSpace(n)
	if n == "" || utf8.RuneCountInString(n) > 64 {
		return invalid("display name must be 1-64 characters")
	}
	for _, r := range n {
		if unicode.IsControl(r) {
			return invalid("display name cannot contain control characters")
		}
	}
	return nil
}

// Settings are the only server options users can change.
type Settings struct {
	MOTD             string   `json:"motd"`
	Difficulty       string   `json:"difficulty"`
	Gamemode         string   `json:"gamemode"`
	MaxPlayers       int      `json:"max_players"`
	ViewDistance     int      `json:"view_distance"`
	WhitelistEnabled bool     `json:"whitelist_enabled"`
	Whitelist        []string `json:"whitelist"`
	Ops              []string `json:"ops"`
	PVP              bool     `json:"pvp"`
	Seed             string   `json:"seed"`
	LevelType        string   `json:"level_type"`
	OnlineMode       bool     `json:"online_mode"`
	// AutoSleep stops the server when idle and starts it when a player joins.
	AutoSleep bool `json:"auto_sleep"`
	// Modrinth project slugs (mods or plugins), downloaded by the server on start.
	ModrinthProjects []string `json:"modrinth_projects"`
}

func DefaultSettings() Settings {
	return Settings{
		MOTD:             "A kubecraft server",
		Difficulty:       "normal",
		Gamemode:         "survival",
		MaxPlayers:       20,
		ViewDistance:     10,
		Whitelist:        []string{},
		Ops:              []string{},
		ModrinthProjects: []string{},
		PVP:              true,
		LevelType:        "normal",
		OnlineMode:       true,
	}
}

var (
	Difficulties = []string{"peaceful", "easy", "normal", "hard"}
	Gamemodes    = []string{"survival", "creative", "adventure", "spectator"}
	// Level types mapped to the server.properties value.
	LevelTypes = map[string]string{
		"normal":       "minecraft:normal",
		"flat":         "minecraft:flat",
		"large_biomes": "minecraft:large_biomes",
		"amplified":    "minecraft:amplified",
	}
	playerRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)
)

const maxPlayersInList = 200

// ParseSettings decodes JSON settings, rejecting unknown keys, then validates them.
func ParseSettings(raw []byte) (Settings, error) {
	s := DefaultSettings()
	if len(raw) == 0 {
		return s, nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Settings{}, invalid("invalid settings: %s", strings.TrimPrefix(err.Error(), "json: "))
	}
	return s, s.Validate()
}

// Validate checks all fields and normalizes player lists.
func (s *Settings) Validate() error {
	if utf8.RuneCountInString(s.MOTD) > 120 || strings.ContainsAny(s.MOTD, "\r\n") {
		return invalid("MOTD must be at most 120 characters on one line")
	}
	if !slices.Contains(Difficulties, s.Difficulty) {
		return invalid("difficulty must be one of %s", strings.Join(Difficulties, ", "))
	}
	if !slices.Contains(Gamemodes, s.Gamemode) {
		return invalid("gamemode must be one of %s", strings.Join(Gamemodes, ", "))
	}
	if s.MaxPlayers < 1 || s.MaxPlayers > 200 {
		return invalid("max players must be between 1 and 200")
	}
	if s.ViewDistance < 2 || s.ViewDistance > 32 {
		return invalid("view distance must be between 2 and 32")
	}
	if len(s.Seed) > 64 || strings.ContainsAny(s.Seed, "\r\n") {
		return invalid("seed must be at most 64 characters on one line")
	}
	if _, ok := LevelTypes[s.LevelType]; !ok {
		return invalid("unknown level type %q", s.LevelType)
	}
	var err error
	if s.Whitelist, err = normalizePlayers("whitelist", s.Whitelist); err != nil {
		return err
	}
	if s.Ops, err = normalizePlayers("ops", s.Ops); err != nil {
		return err
	}
	if len(s.ModrinthProjects) > maxProjects {
		return invalid("at most %d mods or plugins", maxProjects)
	}
	refs := make([]string, 0, len(s.ModrinthProjects))
	for _, r := range s.ModrinthProjects {
		r = strings.TrimSpace(r)
		if !projectRe.MatchString(r) {
			return invalid("%q is not a valid Modrinth project", r)
		}
		if !slices.Contains(refs, r) {
			refs = append(refs, r)
		}
	}
	s.ModrinthProjects = refs
	return nil
}

const maxProjects = 100

var projectRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{2,64}$`)

func normalizePlayers(field string, in []string) ([]string, error) {
	if len(in) > maxPlayersInList {
		return nil, invalid("%s can have at most %d players", field, maxPlayersInList)
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		if !playerRe.MatchString(p) {
			return nil, invalid("%s: %q is not a valid Minecraft username", field, p)
		}
		if k := strings.ToLower(p); !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// ValidPlayerName reports whether n is a valid Minecraft Java username.
func ValidPlayerName(n string) bool { return playerRe.MatchString(n) }

// Env returns the itzg env vars for these settings.
func (s Settings) Env() map[string]string {
	env := map[string]string{
		"MOTD":                    s.MOTD,
		"DIFFICULTY":              s.Difficulty,
		"MODE":                    s.Gamemode,
		"MAX_PLAYERS":             strconv.Itoa(s.MaxPlayers),
		"VIEW_DISTANCE":           strconv.Itoa(s.ViewDistance),
		"PVP":                     strconv.FormatBool(s.PVP),
		"LEVEL_TYPE":              LevelTypes[s.LevelType],
		"ONLINE_MODE":             strconv.FormatBool(s.OnlineMode),
		"ENABLE_WHITELIST":        strconv.FormatBool(s.WhitelistEnabled),
		"ENFORCE_WHITELIST":       strconv.FormatBool(s.WhitelistEnabled),
		"WHITELIST":               strings.Join(s.Whitelist, ","),
		"OPS":                     strings.Join(s.Ops, ","),
		"EXISTING_WHITELIST_FILE": "SYNCHRONIZE",
		"EXISTING_OPS_FILE":       "SYNCHRONIZE",
		// The image downloads these (and their required dependencies) on start.
		"MODRINTH_PROJECTS":              strings.Join(s.ModrinthProjects, ","),
		"MODRINTH_DOWNLOAD_DEPENDENCIES": "required",
	}
	if s.Seed != "" {
		env["SEED"] = s.Seed
	}
	return env
}

// LiveCommands are RCON commands that apply settings stored in the world (not only in
// server.properties). They are sent after each start and after every settings change.
func (s Settings) LiveCommands() []string {
	wl := "off"
	if s.WhitelistEnabled {
		wl = "on"
	}
	return []string{
		"difficulty " + s.Difficulty,
		"defaultgamemode " + s.Gamemode,
		"whitelist " + wl,
		// PvP is a gamerule on newer versions; older ones reject it and use server.properties.
		"gamerule pvp " + strconv.FormatBool(s.PVP),
	}
}

// IsValidation reports whether err is a user input error.
func IsValidation(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}
