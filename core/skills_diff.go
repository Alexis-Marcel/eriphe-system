package main

import (
	"bytes"
	"strings"
)

// What changed between two versions of a skill, file by file, shown to the
// user before they approve the new one.

// fileDiff is one file's change: added, removed or changed, and its lines
// around the changes (Lines empty for a binary file, or one too long to
// compare: the user then reads the new version whole).
type fileDiff struct {
	Path   string
	Status string // added, removed, changed
	Script bool
	Lines  []diffLine
	Binary bool
}

// diffLine is a line of a diff: Op is " " (kept), "+" or "-"; a gap
// between hunks has Op "…".
type diffLine struct {
	Op, Text string
}

const (
	diffContext  = 3
	diffMaxLines = 3000 // per side, beyond which a file is not compared line by line
)

// diffSkills compares the files of two versions; old may be nil (a new skill).
func diffSkills(old, cur *skillVersion) []fileDiff {
	before := map[string]skillFile{}
	if old != nil {
		for _, f := range old.Files {
			before[f.Path] = f
		}
	}
	var out []fileDiff
	seen := map[string]bool{}
	for _, f := range cur.Files {
		seen[f.Path] = true
		o, had := before[f.Path]
		switch {
		case !had:
			d := fileDiff{Path: f.Path, Status: "added", Script: isScript(f), Binary: !isText(f.Data)}
			if !d.Binary {
				d.Lines = diffLines(nil, splitLines(f.Data))
			}
			out = append(out, d)
		case !bytes.Equal(o.Data, f.Data) || o.Exec != f.Exec:
			d := fileDiff{Path: f.Path, Status: "changed", Script: isScript(f), Binary: !isText(f.Data) || !isText(o.Data)}
			if !d.Binary {
				d.Lines = diffLines(splitLines(o.Data), splitLines(f.Data))
			}
			out = append(out, d)
		}
	}
	if old != nil {
		for _, f := range old.Files {
			if !seen[f.Path] {
				out = append(out, fileDiff{Path: f.Path, Status: "removed", Script: isScript(f)})
			}
		}
	}
	return out
}

func splitLines(b []byte) []string {
	s := strings.TrimSuffix(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// diffLines is a line diff (longest common subsequence), with
// diffContext lines kept around each change.
func diffLines(a, b []string) []diffLine {
	if len(a) > diffMaxLines || len(b) > diffMaxLines {
		var out []diffLine
		for _, l := range a {
			out = append(out, diffLine{"-", l})
		}
		for _, l := range b {
			out = append(out, diffLine{"+", l})
		}
		return out
	}
	// lcs[i][j]: the longest common subsequence of a[i:] and b[j:].
	lcs := make([][]int32, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var all []diffLine
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			all = append(all, diffLine{" ", a[i]})
			i, j = i+1, j+1
		case i < len(a) && (j == len(b) || lcs[i+1][j] >= lcs[i][j+1]):
			all = append(all, diffLine{"-", a[i]}) // the removed line before the added one
			i++
		default:
			all = append(all, diffLine{"+", b[j]})
			j++
		}
	}
	// Only the changes and their context.
	keep := make([]bool, len(all))
	for k, l := range all {
		if l.Op != " " {
			for m := max(0, k-diffContext); m <= min(len(all)-1, k+diffContext); m++ {
				keep[m] = true
			}
		}
	}
	var out []diffLine
	gap := false
	for k, l := range all {
		if !keep[k] {
			gap = true
			continue
		}
		if gap && len(out) > 0 {
			out = append(out, diffLine{"…", ""})
		}
		gap = false
		out = append(out, l)
	}
	return out
}
