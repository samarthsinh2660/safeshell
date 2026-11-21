package config

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
)

// Unknown lists the settings in the file at path that SafeShell does not know,
// one plain sentence each naming the full key, with the closest known key when
// one is within two typos. It is for showing to the user, never an error: the
// rest of the file still applies. A missing or unreadable file has none (Load
// reports those).
func Unknown(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	return unknownIn("", raw, reflect.TypeOf(Config{}))
}

// unknownIn compares the keys of one JSON object with the json tags of the
// struct type t, and recurses into nested structs. Maps (hosts) take any key.
func unknownIn(prefix string, obj map[string]any, t reflect.Type) []string {
	known := map[string]reflect.Type{}
	names := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		n := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		known[n] = t.Field(i).Type
		names = append(names, n)
	}
	sort.Strings(names)
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		ft, ok := known[k]
		switch {
		case !ok:
			msg := "unknown setting " + prefix + k + " is ignored"
			if s := closest(k, names); s != "" {
				msg += ": did you mean " + prefix + s + "?"
			}
			out = append(out, msg)
		case ft.Kind() == reflect.Struct:
			if sub, ok := obj[k].(map[string]any); ok {
				out = append(out, unknownIn(prefix+k+".", sub, ft)...)
			}
		}
	}
	return out
}

// closest is the name within two edits of k, the nearest first, or "".
func closest(k string, names []string) string {
	best, bestD := "", 3
	for _, n := range names {
		if d := distance(k, n); d < bestD {
			best, bestD = n, d
		}
	}
	return best
}

// distance is the Levenshtein edit distance between a and b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
