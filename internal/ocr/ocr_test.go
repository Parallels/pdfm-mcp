package ocr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestParseValid(t *testing.T) {
	doc := `{"width":800,"height":600,"lines":[
		{"text":"B","confidence":0.9,"box":[100,50,150,70]},
		{"text":"A","confidence":0.8,"box":[10,10,60,30]},
		{"text":"C","confidence":1,"box":[10,50,60,70]}]}` + "\n"
	rec, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Width != 800 || rec.Height != 600 || len(rec.Lines) != 3 {
		t.Fatalf("rec = %+v", rec)
	}
	var order []string
	for _, l := range rec.Lines {
		order = append(order, l.Text)
	}
	if !reflect.DeepEqual(order, []string{"A", "C", "B"}) {
		t.Fatalf("order = %v, want [A C B]", order)
	}
}

func TestParseRejects(t *testing.T) {
	line := func(text string, conf string, box string) string {
		return fmt.Sprintf(`{"width":100,"height":100,"lines":[{"text":%q,"confidence":%s,"box":%s}]}`, text, conf, box)
	}
	manyLines := `{"width":100,"height":100,"lines":[` + strings.Repeat(`{"text":"x","confidence":1,"box":[0,0,1,1]},`, MaxLines) + `{"text":"x","confidence":1,"box":[0,0,1,1]}]}`
	cases := map[string]string{
		"empty":          ``,
		"not json":       `hello`,
		"trailing":       `{"width":1,"height":1,"lines":[]} {}`,
		"unknown field":  `{"width":1,"height":1,"lines":[],"extra":1}`,
		"zero width":     `{"width":0,"height":1,"lines":[]}`,
		"huge width":     `{"width":1000000,"height":1,"lines":[]}`,
		"too many lines": manyLines,
		"long text":      line(strings.Repeat("a", MaxTextChars+1), "0.5", "[0,0,1,1]"),
		"missing conf":   `{"width":100,"height":100,"lines":[{"text":"a","box":[0,0,1,1]}]}`,
		"conf above 1":   line("a", "1.5", "[0,0,1,1]"),
		"conf negative":  line("a", "-0.1", "[0,0,1,1]"),
		"box short":      line("a", "0.5", "[0,0,1]"),
		"box long":       line("a", "0.5", "[0,0,1,1,1]"),
		"box outside":    line("a", "0.5", "[0,0,101,1]"),
		"box negative":   line("a", "0.5", "[-1,0,1,1]"),
		"box inverted":   line("a", "0.5", "[5,0,1,1]"),
		"text not a str": `{"width":100,"height":100,"lines":[{"text":5,"confidence":1,"box":[0,0,1,1]}]}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(doc)); err == nil {
				t.Fatal("want error")
			}
		})
	}
	// Exactly at the limits is accepted.
	atLimit := `{"width":100,"height":100,"lines":[` + strings.Repeat(`{"text":"x","confidence":1,"box":[0,0,1,1]},`, MaxLines-1) + `{"text":"` + strings.Repeat("é", MaxTextChars) + `","confidence":1,"box":[0,0,1,1]}]}`
	if _, err := Parse([]byte(atLimit)); err != nil {
		t.Fatalf("limits must be inclusive: %v", err)
	}
}

func TestParseErrorsDoNotQuoteText(t *testing.T) {
	_, err := Parse([]byte(`{"width":100,"height":100,"lines":[{"text":"SECRET-TOKEN","confidence":9,"box":[0,0,1,1]}]}`))
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseOutputCap(t *testing.T) {
	if _, err := Parse(make([]byte, MaxOutputBytes+1)); err == nil {
		t.Fatal("want error for oversized output")
	}
}

func sample() []Line {
	return []Line{
		{Text: "File", Confidence: 0.99, Box: Box{10, 10, 50, 30}},
		{Text: "Edit", Confidence: 0.98, Box: Box{60, 11, 100, 31}},
		{Text: "Save changes?", Confidence: 0.95, Box: Box{200, 200, 400, 230}},
		{Text: "Cancel", Confidence: 0.40, Box: Box{200, 300, 260, 320}},
		{Text: "OK", Confidence: 0.20, Box: Box{300, 300, 330, 320}},
		{Text: "Error 404", Confidence: 0.90, Box: Box{20, 500, 120, 520}},
		{Text: "Error 500", Confidence: 0.90, Box: Box{20, 540, 120, 560}},
	}
}

func texts(ls []Line) []string {
	out := []string{}
	for _, l := range ls {
		out = append(out, l.Text)
	}
	return out
}

func TestFindSubstringCaseInsensitive(t *testing.T) {
	got, err := Find(sample(), "SAVE", FindOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(texts(got), []string{"Save changes?"}) {
		t.Fatalf("got %v", texts(got))
	}
	// Substring metacharacters are literal.
	got, err = Find(sample(), "?", FindOptions{})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v err %v", texts(got), err)
	}
	none, err := Find(sample(), "zzz", FindOptions{})
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("no match must be an empty non-nil slice, got %#v err %v", none, err)
	}
}

func TestFindRegex(t *testing.T) {
	got, err := Find(sample(), `^Error \d+$`, FindOptions{Regex: true})
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v err %v", texts(got), err)
	}
	// Regex is case-sensitive unless (?i).
	got, _ = Find(sample(), `^error`, FindOptions{Regex: true})
	if len(got) != 0 {
		t.Fatalf("got %v", texts(got))
	}
	got, _ = Find(sample(), `(?i)^error`, FindOptions{Regex: true})
	if len(got) != 2 {
		t.Fatalf("got %v", texts(got))
	}
	if _, err := Find(sample(), `(`, FindOptions{Regex: true}); err == nil {
		t.Fatal("invalid regex must fail")
	}
	// Backreferences are not RE2.
	if _, err := Find(sample(), `(a)\1`, FindOptions{Regex: true}); err == nil {
		t.Fatal("backreference must be rejected")
	}
}

func TestFindQueryLimits(t *testing.T) {
	if _, err := Find(sample(), "", FindOptions{}); err == nil {
		t.Fatal("empty query must fail")
	}
	if _, err := Find(sample(), strings.Repeat("a", MaxQueryLen+1), FindOptions{}); err == nil {
		t.Fatal("long query must fail")
	}
	if _, err := Find(sample(), strings.Repeat("a", MaxQueryLen), FindOptions{Regex: true}); err != nil {
		t.Fatalf("query at the limit: %v", err)
	}
	if _, err := Find(sample(), "a", FindOptions{Nth: -1}); err == nil {
		t.Fatal("negative nth must fail")
	}
}

func TestFindMinConfidence(t *testing.T) {
	got, _ := Find(sample(), "o", FindOptions{MinConfidence: 0.5})
	for _, l := range got {
		if l.Confidence < 0.5 {
			t.Fatalf("low confidence line kept: %+v", l)
		}
	}
	got, _ = Find(sample(), "cancel", FindOptions{MinConfidence: 0.5})
	if len(got) != 0 {
		t.Fatalf("got %v", texts(got))
	}
	got, _ = Find(sample(), "cancel", FindOptions{MinConfidence: 0.4})
	if len(got) != 1 {
		t.Fatalf("threshold must be inclusive, got %v", texts(got))
	}
}

func TestFindRegion(t *testing.T) {
	r := Box{0, 0, 120, 40} // menu bar
	got, _ := Find(sample(), "e", FindOptions{Region: &r})
	if !reflect.DeepEqual(texts(got), []string{"File", "Edit"}) {
		t.Fatalf("got %v", texts(got))
	}
	// Touching edges do not intersect.
	touch := Box{50, 0, 60, 40}
	got, _ = Find(sample(), "e", FindOptions{Region: &touch})
	if len(got) != 0 {
		t.Fatalf("edge-touching must not match, got %v", texts(got))
	}
	// Partial overlap does.
	part := Box{90, 20, 95, 25}
	got, _ = Find(sample(), "edit", FindOptions{Region: &part})
	if len(got) != 1 {
		t.Fatalf("partial overlap must match, got %v", texts(got))
	}
}

func TestFindNth(t *testing.T) {
	got, _ := Find(sample(), "error", FindOptions{Nth: 2})
	if !reflect.DeepEqual(texts(got), []string{"Error 500"}) {
		t.Fatalf("got %v", texts(got))
	}
	got, _ = Find(sample(), "error", FindOptions{Nth: 3})
	if len(got) != 0 {
		t.Fatalf("got %v", texts(got))
	}
}

func TestCenter(t *testing.T) {
	x, y := Line{Box: Box{10, 20, 31, 40}}.Center()
	if x != 20 || y != 30 {
		t.Fatalf("center = %d,%d", x, y)
	}
}

func TestJoin(t *testing.T) {
	if Join(nil) != "" {
		t.Fatal("empty join")
	}
	lines := []Line{
		{Text: "Edit", Box: Box{60, 11, 100, 31}},
		{Text: "Save changes?", Box: Box{200, 200, 400, 230}},
		{Text: "File", Box: Box{10, 10, 50, 30}},
		{Text: "Cancel", Box: Box{200, 300, 260, 320}},
		{Text: "OK", Box: Box{300, 302, 330, 322}},
	}
	want := "File Edit\nSave changes?\nCancel OK"
	if got := Join(lines); got != want {
		t.Fatalf("Join = %q, want %q", got, want)
	}
	// Input must not be reordered in place.
	if lines[0].Text != "Edit" {
		t.Fatal("Join mutated its input")
	}
}

func TestNewBox(t *testing.T) {
	if _, err := NewBox([]int{0, 0, 10, 10}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]int{nil, {1, 2, 3}, {1, 2, 3, 4, 5}, {10, 0, 5, 5}, {0, 10, 5, 5}, {-1, 0, 5, 5}, {0, 0, 5, 1 << 30}, {0, 0, 0, 5}} {
		if _, err := NewBox(bad); err == nil {
			t.Fatalf("NewBox(%v) must fail", bad)
		}
	}
}

const okJSON = `{"width":100,"height":50,"lines":[{"text":"hi","confidence":0.9,"box":[1,2,30,20]}]}`

func fakeVisionRunner(out string, err error, got *struct {
	png   []byte
	fast  bool
	langs string
}) visionRun {
	return func(_ context.Context, png []byte, fast bool, langs string) ([]byte, error) {
		if got != nil {
			got.png, got.fast, got.langs = png, fast, langs
		}
		return []byte(out), err
	}
}

func TestVisionRecognize(t *testing.T) {
	var got struct {
		png   []byte
		fast  bool
		langs string
	}
	v := NewVision(WithVisionRunner(fakeVisionRunner(okJSON, nil, &got)), WithVisionLogger(quiet()),
		WithVisionFast(), WithVisionLanguages("en-US", "de-DE"))
	rec, err := v.Recognize(context.Background(), []byte("png-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Width != 100 || len(rec.Lines) != 1 || rec.Lines[0].Text != "hi" {
		t.Fatalf("rec = %+v", rec)
	}
	if string(got.png) != "png-bytes" {
		t.Fatalf("png = %q", got.png)
	}
	if !got.fast || got.langs != "en-US,de-DE" {
		t.Fatalf("fast=%v langs=%q", got.fast, got.langs)
	}
}

func TestVisionRecognizeErrors(t *testing.T) {
	ctx := context.Background()
	v := NewVision(WithVisionRunner(fakeVisionRunner("", errors.New("vision exploded"), nil)), WithVisionLogger(quiet()))
	_, err := v.Recognize(ctx, []byte("p"))
	if err == nil || !strings.Contains(err.Error(), "vision exploded") {
		t.Fatalf("err = %v", err)
	}

	v = NewVision(WithVisionRunner(fakeVisionRunner("not json", nil, nil)), WithVisionLogger(quiet()))
	if _, err = v.Recognize(ctx, []byte("p")); err == nil {
		t.Fatal("bad JSON must fail")
	}
	if _, err = v.Recognize(ctx, nil); err == nil {
		t.Fatal("empty image must fail")
	}
	if _, err = v.Recognize(ctx, make([]byte, MaxInputBytes+1)); err == nil {
		t.Fatal("oversized image must fail")
	}
}

func TestVisionTimeout(t *testing.T) {
	blocking := func(ctx context.Context, _ []byte, _ bool, _ string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	v := NewVision(WithVisionRunner(blocking), WithVisionTimeout(20*time.Millisecond), WithVisionLogger(quiet()))
	_, err := v.Recognize(context.Background(), []byte("p"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func TestVisionLanguageValidation(t *testing.T) {
	for _, bad := range []string{"en US", "e", "en-US;rm", strings.Repeat("a", 20)} {
		v := NewVision(WithVisionRunner(fakeVisionRunner(okJSON, nil, nil)), WithVisionLogger(quiet()),
			WithVisionLanguages(bad))
		if _, err := v.Recognize(context.Background(), []byte("p")); err == nil {
			t.Fatalf("language %q must be rejected", bad)
		}
	}
	tags := make([]string, maxLangs+1)
	for i := range tags {
		tags[i] = "en"
	}
	v := NewVision(WithVisionRunner(fakeVisionRunner(okJSON, nil, nil)), WithVisionLogger(quiet()),
		WithVisionLanguages(tags...))
	if _, err := v.Recognize(context.Background(), []byte("p")); err == nil {
		t.Fatal("too many languages must fail")
	}
	v = NewVision(WithVisionRunner(fakeVisionRunner(okJSON, nil, nil)), WithVisionLogger(quiet()),
		WithVisionLanguages("zh-Hans", "en-US", "pt"))
	if _, err := v.Recognize(context.Background(), []byte("p")); err != nil {
		t.Fatal(err)
	}
}
