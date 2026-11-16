package main

import "strings"

// jsonObject is the JSON object in a model's answer: the answer itself,
// or what lies between its first { and its last }, out of a code fence.
func jsonObject(s string) string {
	s = strings.TrimSpace(s)
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j < i {
		return s
	}
	return s[i : j+1]
}
