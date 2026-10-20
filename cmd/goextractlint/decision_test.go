package main

import (
	"testing"
)

func TestDecode(t *testing.T) {
	cases := []struct {
		name, answer string
		probability  *float64
		bad          bool
	}{
		{name: missingPath, answer: `{}`, bad: true},
		{name: "unknown", answer: `{"type":"choice","choice":"other"}`, bad: true},
		{name: "absent", answer: `{"type":"choice","choice":"extract","confidence":0.9}`},
		{name: "null", answer: `{"type":"choice","choice":"keep","probabilities":{"extract":null}}`},
		{
			name:        "zero",
			answer:      `{"type":"choice","choice":"extract","probabilities":{"extract":0}}`,
			probability: floatPointer(0),
		},
		{
			name:        "one",
			answer:      `{"type":"choice","choice":"insufficient_context","probabilities":{"extract":1}}`,
			probability: floatPointer(1),
		},
		{
			name:   "negative",
			answer: `{"type":"choice","choice":"extract","probabilities":{"extract":-0.1}}`,
			bad:    true,
		},
		{
			name:   "above one",
			answer: `{"type":"choice","choice":"extract","probabilities":{"extract":1.1}}`,
			bad:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeDecision([]byte(`{"answers":{"verdict":` + tc.answer + `}}`))
			if (err != nil) != tc.bad {
				t.Fatalf("decision=%+v error=%v", got, err)
			}

			if !tc.bad &&
				((got.ExtractProbability == nil) != (tc.probability == nil) || tc.probability != nil && *tc.probability != *got.ExtractProbability) {
				t.Fatalf("probability=%v", got.ExtractProbability)
			}
		})
	}
}

func floatPointer(p float64) *float64 { return &p }
