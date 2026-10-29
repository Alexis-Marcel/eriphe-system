package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// The results subfolder: where a task lent a machine's folder with drop
// writes, named after the assistant (Eriphe/ until it has another name,
// Jarvis/ once it is called Jarvis). eriphe-core tells the node the current
// name and the former ones; the node renames a former results folder to
// the current name on next use, or, when a folder of the new name is
// already there, keeps the former one and says so (a results_conflict
// event, in the device's access log). Google Drive folders keep Eriphe/.

const (
	defaultResults = "Eriphe"
	resultsNameKey = "results_name"   // the name the results folder had at the last start or change
	resultsFormer  = "results_former" // its former names, newest first (JSON)
	maxFormer      = 8
)

// resultsFolderName is a display name as a folder name: what is not a
// letter, a digit, a space, an apostrophe or a dash becomes a dash (a
// display name has none, but a file may have been written by hand), and
// nothing usable leaves Eriphe.
func resultsFolderName(display string) string {
	s := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || r == ' ' || r == '-' || r == '\'' || r == '’' {
			return r
		}
		return '-'
	}, normalizeDisplayName(display))
	s = strings.Trim(s, " -'’")
	if s == "" || len(s) > 96 {
		return defaultResults
	}
	return s
}

// resultsName is the results folder's current name.
func (c *core) resultsName() string { return resultsFolderName(c.conf().assistantName()) }

// formerResults are the names the results folder had before, newest first:
// Eriphe always among them, for the folders older versions made.
func (c *core) formerResults() []string {
	var former []string
	json.Unmarshal([]byte(c.store.get(resultsFormer)), &former)
	cur := c.resultsName()
	var out []string
	for _, f := range append(former, defaultResults) {
		if f != cur && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// noteResultsName keeps the results folder's former names when the
// assistant is renamed: called at start and on each catalog change.
func (c *core) noteResultsName(m *Config) {
	if c.store == nil {
		return
	}
	cur := resultsFolderName(m.assistantName())
	last := c.store.get(resultsNameKey)
	if last == cur {
		return
	}
	if last != "" {
		var former []string
		json.Unmarshal([]byte(c.store.get(resultsFormer)), &former)
		former = slices.DeleteFunc(former, func(f string) bool { return f == last || f == cur })
		former = append([]string{last}, former...)
		if len(former) > maxFormer {
			former = former[:maxFormer]
		}
		raw, _ := json.Marshal(former)
		c.store.set(resultsFormer, string(raw))
	}
	c.store.set(resultsNameKey, cur)
}

// resultsPath is where a worker writes its results in a folder.
func resultsPath(f *Folder, results string) string {
	return fmt.Sprintf("%s/%s/", f.mountPath(), results)
}
