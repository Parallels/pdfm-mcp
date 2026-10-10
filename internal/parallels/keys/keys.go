// Package keys holds the Parallels PRL_KEY code table (from the Parallels
// Desktop CLI reference) and pure mappings from key names and plain text
// to keyboard event sequences. It has no knowledge of prlctl or MCP.
package keys

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

// leftShiftCode is PRL_KEY_LEFT_SHIFT.
const leftShiftCode = 50

// codes maps the lowercased short name (PRL_KEY_ prefix removed) to the
// Parallels virtual keycode. The table is fixed upstream; gaps in the
// numbering (8, 101, 111, 114) are upstream gaps, not omissions.
var codes = map[string]int{
	"wildcard_any":        1,
	"wildcard_keyboard":   2,
	"wildcard_mouse":      3,
	"wildcard_shift":      4,
	"wildcard_ctrl":       5,
	"wildcard_alt":        6,
	"wildcard_win":        7,
	"escape":              9,
	"1":                   10,
	"2":                   11,
	"3":                   12,
	"4":                   13,
	"5":                   14,
	"6":                   15,
	"7":                   16,
	"8":                   17,
	"9":                   18,
	"0":                   19,
	"minus":               20,
	"equal":               21,
	"backspace":           22,
	"tab":                 23,
	"q":                   24,
	"w":                   25,
	"e":                   26,
	"r":                   27,
	"t":                   28,
	"y":                   29,
	"u":                   30,
	"i":                   31,
	"o":                   32,
	"p":                   33,
	"left_bracket":        34,
	"right_bracket":       35,
	"enter":               36,
	"left_control":        37,
	"a":                   38,
	"s":                   39,
	"d":                   40,
	"f":                   41,
	"g":                   42,
	"h":                   43,
	"j":                   44,
	"k":                   45,
	"l":                   46,
	"semicolon":           47,
	"quote":               48,
	"tilde":               49,
	"left_shift":          50,
	"backslash":           51,
	"z":                   52,
	"x":                   53,
	"c":                   54,
	"v":                   55,
	"b":                   56,
	"n":                   57,
	"m":                   58,
	"comma":               59,
	"dot":                 60,
	"slash":               61,
	"right_shift":         62,
	"np_star":             63,
	"left_alt":            64,
	"space":               65,
	"caps_lock":           66,
	"f1":                  67,
	"f2":                  68,
	"f3":                  69,
	"f4":                  70,
	"f5":                  71,
	"f6":                  72,
	"f7":                  73,
	"f8":                  74,
	"f9":                  75,
	"f10":                 76,
	"num_lock":            77,
	"scroll_lock":         78,
	"np_7":                79,
	"np_8":                80,
	"np_9":                81,
	"np_minus":            82,
	"np_4":                83,
	"np_5":                84,
	"np_6":                85,
	"np_plus":             86,
	"np_1":                87,
	"np_2":                88,
	"np_3":                89,
	"np_0":                90,
	"np_delete":           91,
	"print":               92,
	"europe_1":            93,
	"europe_2":            94,
	"f11":                 95,
	"f12":                 96,
	"home":                97,
	"up":                  98,
	"page_up":             99,
	"left":                100,
	"right":               102,
	"end":                 103,
	"down":                104,
	"page_down":           105,
	"insert":              106,
	"delete":              107,
	"np_enter":            108,
	"right_control":       109,
	"pause":               110,
	"np_slash":            112,
	"right_alt":           113,
	"left_win":            115,
	"right_win":           116,
	"menu":                117,
	"media_next_track":    118,
	"media_prev_track":    119,
	"media_stop":          120,
	"media_play_pause":    121,
	"mute":                122,
	"volume_up":           123,
	"volume_down":         124,
	"media_select":        125,
	"app_mail":            126,
	"app_calculator":      127,
	"app_my_computer":     128,
	"www_search":          129,
	"www_home":            130,
	"www_back":            131,
	"www_forward":         132,
	"www_stop":            133,
	"www_refresh":         134,
	"www_favorites":       135,
	"eject":               136,
	"system_power":        137,
	"system_sleep":        138,
	"system_wake":         139,
	"brazilian_keypad":    140,
	"ro":                  141,
	"hiragana_katakana":   142,
	"yen":                 143,
	"henkan":              144,
	"muhenkan":            145,
	"pc9800_keypad":       146,
	"hanguel":             147, // spelled HANGUEL in the upstream table
	"hanja":               148,
	"katakana":            149,
	"hiragana":            150,
	"zenkaku_hankaku":     151,
	"f13":                 152,
	"f14":                 153,
	"f15":                 154,
	"f16":                 155,
	"f17":                 156,
	"f18":                 157,
	"f19":                 158,
	"f20":                 159,
	"f21":                 160,
	"f22":                 161,
	"f23":                 162,
	"f24":                 163,
	"np_equal":            164,
	"break":               165,
	"print_with_modifier": 166,
	"sysrq":               167,
	"fn":                  168,
	"euro":                169,
	"dollar":              170,
	"mon_brightness_down": 171,
	"mon_brightness_up":   172,
	"app_expose":          173,
	"app_dashboard":       174,
	"kbd_brightness_down": 175,
	"kbd_brightness_up":   176,
	"mac129":              177,
	"left_button":         178,
	"middle_button":       179,
	"right_button":        180,
	"move_up_left":        181,
	"move_up":             182,
	"move_up_right":       183,
	"move_left":           184,
	"move_right":          185,
	"move_down_left":      186,
	"move_down":           187,
	"move_down_right":     188,
	"wheel_up":            189,
	"wheel_down":          190,
	"wheel_left":          191,
	"wheel_right":         192,
}

// CodeFor resolves a key name to its Parallels keycode. It accepts the
// bare short name or the PRL_KEY_ prefixed form, case-insensitively, and
// numeric strings (the code itself).
func CodeFor(name string) (int, error) {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.TrimPrefix(s, "prl_key_")
	if s == "" {
		return 0, fmt.Errorf("unknown key name %q", name)
	}
	if code, ok := codes[s]; ok {
		return code, nil
	}
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n, nil
	}
	return 0, fmt.Errorf("unknown key name %q", name)
}

type textKey struct {
	code  int
	shift bool
}

// typeMap is the QWERTY character → key mapping used by EventsForText.
// Letters are by physical position (A=38, S=39, …), not alphabetically.
var typeMap = map[rune]textKey{
	'0': {19, false}, '1': {10, false}, '2': {11, false}, '3': {12, false},
	'4': {13, false}, '5': {14, false}, '6': {15, false}, '7': {16, false},
	'8': {17, false}, '9': {18, false},
	' ': {65, false}, '\n': {36, false}, '\r': {36, false}, '\t': {23, false},
	'q': {24, false}, 'w': {25, false}, 'e': {26, false}, 'r': {27, false},
	't': {28, false}, 'y': {29, false}, 'u': {30, false}, 'i': {31, false},
	'o': {32, false}, 'p': {33, false},
	'a': {38, false}, 's': {39, false}, 'd': {40, false}, 'f': {41, false},
	'g': {42, false}, 'h': {43, false}, 'j': {44, false}, 'k': {45, false},
	'l': {46, false},
	'z': {52, false}, 'x': {53, false}, 'c': {54, false}, 'v': {55, false},
	'b': {56, false}, 'n': {57, false}, 'm': {58, false},
	'-': {20, false}, '=': {21, false}, '[': {34, false}, ']': {35, false},
	'\\': {51, false}, ';': {47, false}, '\'': {48, false}, '`': {49, false},
	',': {59, false}, '.': {60, false}, '/': {61, false},
	'!': {10, true}, '@': {11, true}, '#': {12, true}, '$': {13, true},
	'%': {14, true}, '^': {15, true}, '&': {16, true}, '*': {17, true},
	'(': {18, true}, ')': {19, true},
	'Q': {24, true}, 'W': {25, true}, 'E': {26, true}, 'R': {27, true},
	'T': {28, true}, 'Y': {29, true}, 'U': {30, true}, 'I': {31, true},
	'O': {32, true}, 'P': {33, true},
	'A': {38, true}, 'S': {39, true}, 'D': {40, true}, 'F': {41, true},
	'G': {42, true}, 'H': {43, true}, 'J': {44, true}, 'K': {45, true},
	'L': {46, true},
	'Z': {52, true}, 'X': {53, true}, 'C': {54, true}, 'V': {55, true},
	'B': {56, true}, 'N': {57, true}, 'M': {58, true},
	'_': {20, true}, '+': {21, true}, '{': {34, true}, '}': {35, true},
	'|': {51, true}, ':': {47, true}, '"': {48, true}, '~': {49, true},
	'<': {59, true}, '>': {60, true}, '?': {61, true},
}

// expandEscapes interprets the backslash escapes of the type text:
// \n → newline (Enter), \t → tab, \\ → one literal backslash. A backslash
// before any other character (or at the end of the text) is kept literal,
// so "C:\Users" types as written; only the three escapes above are
// rewritten. Real control characters pass through unchanged.
func expandEscapes(text string) []rune {
	out := make([]rune, 0, len(text))
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == '\\' && i+size < len(text) {
			if next, nsize := utf8.DecodeRuneInString(text[i+size:]); next == 'n' || next == 't' || next == '\\' {
				switch next {
				case 'n':
					out = append(out, '\n')
				case 't':
					out = append(out, '\t')
				case '\\':
					out = append(out, '\\')
				}
				i += size + nsize
				continue
			}
		}
		out = append(out, r)
		i += size
	}
	return out
}

// EventsForText maps ASCII text to a keyboard event sequence with Shift
// handled automatically: consecutive shifted characters share one
// left_shift press/release pair. Backslash escapes \n, \t and \\ are
// interpreted (see expandEscapes); real newlines/tabs also map to Enter
// and Tab. delayMS (0 = none) is applied to every generated event.
// Unmappable runes are rejected.
func EventsForText(text string, delayMS int) ([]interfaces.KeyEvent, error) {
	if text == "" {
		return nil, errors.New("text must not be empty")
	}
	var (
		events   []interfaces.KeyEvent
		inShift  bool
		delayPtr *int
	)
	if delayMS > 0 {
		delayPtr = &delayMS
	}
	endShift := func() {
		if inShift {
			events = append(events, ev(leftShiftCode, "release", delayPtr))
			inShift = false
		}
	}
	for i, r := range expandEscapes(text) {
		m, ok := typeMap[r]
		if !ok {
			return nil, fmt.Errorf("cannot type U+%04X %q at position %d: no key mapping", r, r, i)
		}
		if m.shift && !inShift {
			events = append(events, ev(leftShiftCode, "press", delayPtr))
			inShift = true
		}
		if !m.shift {
			endShift()
		}
		events = append(events, ev(m.code, "", delayPtr))
	}
	endShift()
	return events, nil
}

func ev(code int, event string, delay *int) interfaces.KeyEvent {
	c := code
	return interfaces.KeyEvent{Key: &c, Event: event, Delay: delay}
}
