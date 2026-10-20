package linerange

import (
	"reflect"
	"strings"
	"testing"
)

const (
	firstFile  = "a.go"
	secondFile = "b.go"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name, spec string
		want       Range
		bad        bool
	}{
		{name: "single line", spec: "a.go:7", want: Range{File: firstFile, Start: 7, End: 7}},
		{name: "span", spec: "dir/a.go:3-9", want: Range{File: "dir/a.go", Start: 3, End: 9}},
		{name: "colon in path", spec: "a:b.go:4", want: Range{File: "a:b.go", Start: 4, End: 4}},
		{name: "equal bounds", spec: "a.go:5-5", want: Range{File: firstFile, Start: 5, End: 5}},
		{name: "missing lines", spec: "a.go", bad: true},
		{name: "missing path", spec: ":3", bad: true},
		{name: "empty lines", spec: "a.go:", bad: true},
		{name: "zero line", spec: "a.go:0", bad: true},
		{name: "open end", spec: "a.go:3-", bad: true},
		{name: "reversed", spec: "a.go:9-3", bad: true},
		{name: "not a number", spec: "a.go:x", bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.spec)
			if (err != nil) != tc.bad || got != tc.want {
				t.Fatalf("got=%+v err=%v", got, err)
			}
		})
	}
}

func TestRead(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        []Range
		bad         bool
	}{
		{
			name:  "lines and blanks",
			input: "a.go:1-2\n\n  b.go:3  \nc.go:4",
			want:  []Range{{File: firstFile, Start: 1, End: 2}, {File: secondFile, Start: 3, End: 3}, {File: "c.go", Start: 4, End: 4}},
		},
		{name: "empty"},
		{name: "malformed", input: "a.go:1\nb.go\n", bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Read(strings.NewReader(tc.input))
			if (err != nil) != tc.bad || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got=%+v err=%v", got, err)
			}
		})
	}
}

func TestSelection(t *testing.T) {
	selection := NewSelection([]Range{
		{File: firstFile, Start: 10, End: 12},
		{File: secondFile, Start: 5, End: 5},
		{File: firstFile, Start: 30, End: 30},
	})

	if files := selection.Files(); !reflect.DeepEqual(files, []string{firstFile, secondFile}) {
		t.Fatalf("files=%v", files)
	}

	for _, tc := range []struct {
		name       string
		file       string
		start, end int
		want       bool
	}{
		{name: "contains range", file: firstFile, start: 8, end: 20, want: true},
		{name: "touches start", file: firstFile, start: 1, end: 10, want: true},
		{name: "touches end", file: firstFile, start: 12, end: 15, want: true},
		{name: "second range", file: firstFile, start: 25, end: 35, want: true},
		{name: "between ranges", file: firstFile, start: 13, end: 29},
		{name: "single line", file: secondFile, start: 5, end: 5, want: true},
		{name: "other file", file: "c.go", start: 1, end: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := selection.Overlaps(tc.file, tc.start, tc.end); got != tc.want {
				t.Fatalf("got=%v", got)
			}
		})
	}
}
