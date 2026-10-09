package servers

import (
	"strings"
	"testing"
)

func TestValidateSlug(t *testing.T) {
	good := []string{"a", "survival", "my-world-2", "x1", strings.Repeat("a", 32)}
	for _, s := range good {
		if err := ValidateSlug(s); err != nil {
			t.Errorf("%q rejected: %v", s, err)
		}
	}
	bad := []string{"", "-a", "a-", "My", "a_b", "a.b", strings.Repeat("a", 33), "www", "api", "admin", "mc-router", "ünï"}
	for _, s := range bad {
		if ValidateSlug(s) == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestValidateVersion(t *testing.T) {
	for _, v := range []string{"LATEST", "1.21.4", "1.20", "26.1", "1.21-pre1", "1.21.5-rc2", "24w14a"} {
		if err := ValidateVersion(v); err != nil {
			t.Errorf("%q rejected", v)
		}
	}
	for _, v := range []string{"", "latest", "1", "1.21.4.1", "abc", "1.21; rm -rf", "1.21\n"} {
		if ValidateVersion(v) == nil {
			t.Errorf("%q accepted", v)
		}
	}
}

func TestValidateType(t *testing.T) {
	if ValidateType("PAPER") != nil || ValidateType("paper") == nil || ValidateType("SPIGOT") == nil {
		t.Fatal("type validation wrong")
	}
}

func TestValidateDisplayName(t *testing.T) {
	if ValidateDisplayName("My World") != nil {
		t.Error("valid name rejected")
	}
	for _, n := range []string{"", "   ", strings.Repeat("x", 65), "a\x00b"} {
		if ValidateDisplayName(n) == nil {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestParseSettingsDefaults(t *testing.T) {
	s, err := ParseSettings(nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Difficulty != "normal" || s.MaxPlayers != 20 || !s.OnlineMode {
		t.Fatalf("bad defaults: %+v", s)
	}
	s, err = ParseSettings([]byte(`{"difficulty":"hard"}`))
	if err != nil || s.Difficulty != "hard" || s.Gamemode != "survival" {
		t.Fatalf("partial settings should keep defaults: %+v %v", s, err)
	}
}

func TestParseSettingsRejects(t *testing.T) {
	cases := []string{
		`{"java_opts":"-Xmx99G"}`,
		`{"difficulty":"insane"}`,
		`{"gamemode":"god"}`,
		`{"max_players":0}`,
		`{"max_players":1000}`,
		`{"view_distance":64}`,
		`{"motd":"line1\nline2"}`,
		`{"seed":"` + strings.Repeat("1", 65) + `"}`,
		`{"level_type":"custom"}`,
		`{"whitelist":["ok_name","bad name"]}`,
		`{"ops":["x"]}`,
		`not json`,
	}
	for _, c := range cases {
		_, err := ParseSettings([]byte(c))
		if err == nil {
			t.Errorf("%s accepted", c)
		} else if !IsValidation(err) {
			t.Errorf("%s: expected validation error, got %T", c, err)
		}
	}
}

func TestPlayersNormalized(t *testing.T) {
	s, err := ParseSettings([]byte(`{"whitelist":[" Steve ","steve","Alex"],"ops":["Notch"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(s.Whitelist, ",") != "Steve,Alex" {
		t.Fatalf("whitelist not deduplicated: %v", s.Whitelist)
	}
}

func TestSettingsEnv(t *testing.T) {
	s := DefaultSettings()
	s.WhitelistEnabled = true
	s.Whitelist = []string{"Steve", "Alex"}
	s.LevelType = "flat"
	env := s.Env()
	want := map[string]string{
		"DIFFICULTY":        "normal",
		"MODE":              "survival",
		"ENABLE_WHITELIST":  "true",
		"ENFORCE_WHITELIST": "true",
		"WHITELIST":         "Steve,Alex",
		"LEVEL_TYPE":        "minecraft:flat",
		"MAX_PLAYERS":       "20",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	if _, ok := env["SEED"]; ok {
		t.Error("empty seed must not be set")
	}
}

func TestLiveCommands(t *testing.T) {
	s := DefaultSettings()
	s.Difficulty, s.Gamemode, s.WhitelistEnabled, s.PVP = "hard", "creative", true, false
	got := strings.Join(s.LiveCommands(), ";")
	want := "difficulty hard;defaultgamemode creative;whitelist on;gamerule pvp false"
	if got != want {
		t.Fatalf("got %s", got)
	}
}
