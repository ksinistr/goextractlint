package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"goextractlint/internal/analysis"
)

func TestStore(t *testing.T) {
	for _, verdict := range []analysis.Verdict{analysis.Extract, analysis.Keep, analysis.InsufficientContext} {
		t.Run(string(verdict), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "cache")
			store := New(dir)
			key := strings.Repeat("a", 64)
			p := 0.5

			want := analysis.Decision{Verdict: verdict, ExtractProbability: &p}
			if err := store.Put(key, want); err != nil {
				t.Fatal(err)
			}

			got, hit, err := store.Get(key)
			if err != nil || !hit || !reflect.DeepEqual(got, want) {
				t.Fatalf("decision=%+v hit=%v err=%v", got, hit, err)
			}

			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != key+".json" {
				t.Fatalf("entries=%v err=%v", entries, err)
			}

			data, err := os.ReadFile(filepath.Join(dir, key+".json"))
			if err != nil {
				t.Fatal(err)
			}

			var fields map[string]json.RawMessage
			if json.Unmarshal(data, &fields) != nil || len(fields) != 2 || fields["version"] == nil ||
				fields["decision"] == nil {
				t.Fatalf("unexpected persisted data: %s", data)
			}

			for _, text := range []string{"source", "api_key", "raw_response", "Authorization"} {
				if strings.Contains(string(data), text) {
					t.Fatalf("persisted %s", text)
				}
			}

			fileInfo, err := os.Stat(filepath.Join(dir, key+".json"))
			if err != nil || fileInfo.Mode().Perm() != 0o600 {
				t.Fatalf("file permissions: %v %v", fileInfo, err)
			}

			dirInfo, err := os.Stat(dir)
			if err != nil || dirInfo.Mode().Perm() != 0o700 {
				t.Fatalf("directory permissions: %v %v", dirInfo, err)
			}
		})
	}
}

func TestInvalidEntries(t *testing.T) {
	cases := []struct {
		name, data string
		hit        bool
	}{
		{name: "absent"},
		{name: "corrupt", data: `{"version":`},
		{name: "unknown version", data: `{"version":2,"decision":{"verdict":"keep"}}`},
		{name: "bad verdict", data: `{"version":1,"decision":{"verdict":"unknown"}}`},
		{name: "bad probability", data: `{"version":1,"decision":{"verdict":"extract","extract_probability":2}}`},
		{name: "absent probability", data: `{"version":1,"decision":{"verdict":"extract"}}`, hit: true},
		{name: "unfinished temporary entry", data: `{"version":1,"decision":{"verdict":"keep"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store := New(dir)

			key := strings.Repeat("b", 64)
			if tc.data != "" {
				name := key + ".json"
				if tc.name == "unfinished temporary entry" {
					name = ".decision-temp"
				}

				if err := os.WriteFile(filepath.Join(dir, name), []byte(tc.data), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			got, hit, err := store.Get(key)
			if err != nil || hit != tc.hit || got.ExtractProbability != nil {
				t.Fatalf("decision=%+v hit=%v err=%v", got, hit, err)
			}
		})
	}
}
