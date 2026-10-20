package linerange

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Range struct {
	File  string
	Start int
	End   int
}

func Parse(spec string) (Range, error) {
	separator := strings.LastIndexByte(spec, ':')
	if separator <= 0 {
		return Range{}, fmt.Errorf("range %q must be path:line or path:start-end", spec)
	}

	lines, err := parseLines(spec[separator+1:])
	if err != nil {
		return Range{}, fmt.Errorf("range %q: %w", spec, err)
	}

	lines.File = spec[:separator]

	return lines, nil
}

func parseLines(text string) (Range, error) {
	first, last, found := strings.Cut(text, "-")

	start, err := strconv.Atoi(first)
	if err != nil || start < 1 {
		return Range{}, fmt.Errorf("start line must be a positive integer")
	}

	if !found {
		return Range{Start: start, End: start}, nil
	}

	end, err := strconv.Atoi(last)
	if err != nil || end < start {
		return Range{}, fmt.Errorf("end line must be an integer not less than the start line")
	}

	return Range{Start: start, End: end}, nil
}

func Read(reader io.Reader) ([]Range, error) {
	var ranges []Range

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parsed, err := Parse(line)
		if err != nil {
			return nil, err
		}

		ranges = append(ranges, parsed)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read ranges: %w", err)
	}

	return ranges, nil
}

type Selection struct {
	files  []string
	ranges map[string][]Range
}

func NewSelection(ranges []Range) Selection {
	selection := Selection{ranges: make(map[string][]Range)}

	for _, selected := range ranges {
		if _, ok := selection.ranges[selected.File]; !ok {
			selection.files = append(selection.files, selected.File)
		}

		selection.ranges[selected.File] = append(selection.ranges[selected.File], selected)
	}

	return selection
}

func (s Selection) Files() []string {
	return s.files
}

func (s Selection) Overlaps(file string, start, end int) bool {
	for _, selected := range s.ranges[file] {
		if selected.Start <= end && selected.End >= start {
			return true
		}
	}

	return false
}
