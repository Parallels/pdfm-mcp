package keys

import (
	"slices"
	"strings"
	"testing"
)

func TestCodeForNames(t *testing.T) {
	cases := map[string]int{
		"enter":         36,
		"ENTER":         36,
		"Enter":         36,
		"PRL_KEY_ENTER": 36,
		"prl_key_enter": 36,
		// Whitespace-padded name (CodeFor trims); built by concatenation so
		// the key literal stays clean for the linter.
		" " + "PRL_KEY_ENTER" + " ": 36,
		"f5":                        71,
		"F5":                        71,
		"PRL_KEY_F5":                71,
		"a":                         38,
		"z":                         52,
		"q":                         24,
		"left_shift":                50,
		"left_win":                  115,
		"np_star":                   63,
		"www_home":                  130,
		"mon_brightness_down":       171,
		"mac129":                    177,
		"wheel_right":               192,
		"escape":                    9,
		"space":                     65,
		"np_7":                      79,
		"hanguel":                   147,
		"36":                        36,
		"92":                        92,
	}
	for name, want := range cases {
		got, err := CodeFor(name)
		if err != nil {
			t.Fatalf("CodeFor(%q) error: %v", name, err)
		}
		if got != want {
			t.Fatalf("CodeFor(%q) = %d, want %d", name, got, want)
		}
	}
}

// The doc example: typing "test" + Enter is T=28, E=26, S=39, T=28,
// Enter=36.
func TestCodeForDocExample(t *testing.T) {
	for name, want := range map[string]int{"t": 28, "e": 26, "s": 39} {
		got, err := CodeFor(name)
		if err != nil || got != want {
			t.Fatalf("CodeFor(%q) = %d, %v; want %d", name, got, err, want)
		}
	}
}

func TestCodeForUnknown(t *testing.T) {
	for _, name := range []string{"", "   ", "nope", "PRL_KEY_NOPE", "36abc", "-1", "prl_key_"} {
		if _, err := CodeFor(name); err == nil {
			t.Fatalf("CodeFor(%q): want error, got none", name)
		}
	}
}

func TestTableConsistency(t *testing.T) {
	// No duplicate codes with different names (every code has one name).
	seen := map[int]string{}
	for name, code := range codes {
		if prev, dup := seen[code]; dup {
			t.Fatalf("code %d claimed by %q and %q", code, prev, name)
		}
		seen[code] = name
		if code < 1 || code > 192 {
			t.Fatalf("%s: code %d outside 1-192", name, code)
		}
	}
}

func TestEventsForTextPlain(t *testing.T) {
	evs, err := EventsForText("ab\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	// a=38 tap, b=56 tap, enter=36 tap — no shift.
	if len(evs) != 3 {
		t.Fatalf("got %d events: %+v", len(evs), evs)
	}
	if *evs[0].Key != 38 || evs[0].Event != "" || evs[0].Delay != nil {
		t.Fatalf("ev0 = %+v", evs[0])
	}
	if *evs[1].Key != 56 {
		t.Fatalf("ev1 = %+v", evs[1])
	}
	if *evs[2].Key != 36 {
		t.Fatalf("ev2 = %+v", evs[2])
	}
}

func TestEventsForTextShiftRun(t *testing.T) {
	// "aBBb": a tap, shift press, B tap, B tap, shift release, b tap —
	// one press/release pair for the whole shifted run.
	evs, err := EventsForText("aBBb", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 6 {
		t.Fatalf("got %d events, want 6: %+v", len(evs), evs)
	}
	want := []struct {
		code  int
		event string
	}{
		{38, ""}, {50, "press"}, {56, ""}, {56, ""}, {50, "release"}, {56, ""},
	}
	for i, w := range want {
		if evs[i].Key == nil || *evs[i].Key != w.code || evs[i].Event != w.event {
			t.Fatalf("ev%d = %+v, want code %d event %q", i, evs[i], w.code, w.event)
		}
	}
}

func TestEventsForTextPunctuation(t *testing.T) {
	// ':' is shifted semicolon (47), '!' is shifted '1' (10).
	evs, err := EventsForText(":a!", 0)
	if err != nil {
		t.Fatal(err)
	}
	// shift press, ';', release, a, shift press, '1', release = 7 events
	if len(evs) != 7 {
		t.Fatalf("got %d events, want 7: %+v", len(evs), evs)
	}
	if *evs[1].Key != 47 || *evs[5].Key != 10 {
		t.Fatalf("codes: %+v", evs)
	}
}

func TestEventsForTextDelayApplied(t *testing.T) {
	evs, err := EventsForText("aB", 25)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range evs {
		if e.Delay == nil || *e.Delay != 25 {
			t.Fatalf("ev %d: delay = %v, want 25", i, e.Delay)
		}
	}
}

func TestEventsForTextRejected(t *testing.T) {
	for _, text := range []string{"", "café", "hi\u2028there"} {
		_, err := EventsForText(text, 0)
		if err == nil {
			t.Fatalf("EventsForText(%q): want error", text)
		}
	}
	_, err := EventsForText("a☃b", 0)
	if err == nil || !strings.Contains(err.Error(), "U+2603") {
		t.Fatalf("err = %v, want the rune named", err)
	}
}

func TestEventsForTextTabsAndNewlines(t *testing.T) {
	evs, err := EventsForText("a\tb\nc\r", 0)
	if err != nil {
		t.Fatal(err)
	}
	var codes []int
	for _, e := range evs {
		if e.Key == nil {
			t.Fatalf("expected key events, got %+v", e)
		}
		codes = append(codes, *e.Key)
	}
	want := []int{38, 23, 56, 36, 54, 36} // a, tab, b, enter, c, enter
	if len(codes) != len(want) {
		t.Fatalf("codes = %v, want %v", codes, want)
	}
	for i := range codes {
		if codes[i] != want[i] {
			t.Fatalf("codes = %v, want %v", codes, want)
		}
	}
}

func TestEventsForTextBackslashEscapes(t *testing.T) {
	codesOf := func(text string) []int {
		evs, err := EventsForText(text, 0)
		if err != nil {
			t.Fatalf("EventsForText(%q): %v", text, err)
		}
		var codes []int
		for _, e := range evs {
			codes = append(codes, *e.Key)
		}
		return codes
	}

	cases := []struct {
		name string
		text string
		want []int
	}{
		// Form-style input: a literal backslash before n/t becomes
		// Enter/Tab, not a typed backslash.
		{"backslash-n is enter", "a\\nb", []int{38, 36, 56}},
		{"backslash-t is tab", "a\\tb", []int{38, 23, 56}},
		{"double backslash is one literal backslash", "a\\\\b", []int{38, 51, 56}},
		// A backslash before any other character (or at the end) stays
		// literal, so paths like C:\Users type as written.
		// c:\users = c, shift, semicolon(:), unshift, backslash, u,s,e,r,s
		{"backslash-u stays literal", "c:\\users", []int{54, 50, 47, 50, 51, 30, 39, 26, 27, 39}},
		{"trailing backslash stays literal", "ab\\", []int{38, 56, 51}},
		{"double backslash then n", "a\\\\nb", []int{38, 51, 57, 56}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := codesOf(tc.text); !slices.Equal(got, tc.want) {
				t.Fatalf("codes = %v, want %v", got, tc.want)
			}
		})
	}
}
