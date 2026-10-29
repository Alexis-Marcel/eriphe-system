package main

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// A display name as a folder name.
func TestResultsFolderName(t *testing.T) {
	for in, want := range map[string]string{
		"": "Eriphe", "Eriphe": "Eriphe", "Jarvis": "Jarvis", " Mme  Irma ": "Mme Irma", "O'Neil": "O'Neil",
		"Élise": "Élise", "a/b": "a-b", `..\x`: "x", "-": "Eriphe", "Jar:vis*": "Jar-vis",
	} {
		if got := resultsFolderName(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}

// A task lent a folder with drop gets the results folder's name, and its
// former ones for its node to rename; the worker is told where it is; a
// results folder its node could not rename is in the device's log.
func TestResultsFolderFollowsTheName(t *testing.T) {
	keepName(t)
	c, store := foldersCore(t)
	ctx := context.Background()
	c.addFolder(ctx, Folder{Name: "Clients", Node: "nas", Share: "data", Path: "Clients", Access: folderDrop})
	folder := func() (nodeFolder, string) {
		t.Helper()
		task, err := c.runTask(ctx, runRequest{Prompt: "sum", Folders: []string{"Clients"}})
		if err != nil {
			t.Fatal(err)
		}
		spec := assignedSpec(t, c, "nas", task.ID)
		return spec.Folders[0], spec.AppendSystemPrompt
	}

	// Never named: Eriphe/, nothing former.
	f, prompt := folder()
	if f.Results != "Eriphe" || len(f.Former) != 0 || !strings.Contains(prompt, "/work/folders/Clients/Eriphe/") {
		t.Fatalf("default: %+v %q", f, prompt)
	}

	// Named Jarvis, then Alfred: the former names, newest first.
	setBrain(c, &BrainConfig{DisplayName: "Jarvis"})
	c.noteResultsName(c.conf())
	f, prompt = folder()
	if f.Results != "Jarvis" || !slices.Equal(f.Former, []string{"Eriphe"}) || !strings.Contains(prompt, "/work/folders/Clients/Jarvis/") {
		t.Fatalf("Jarvis: %+v %q", f, prompt)
	}
	setBrain(c, &BrainConfig{DisplayName: "Alfred"})
	c.noteResultsName(c.conf())
	f, _ = folder()
	if f.Results != "Alfred" || !slices.Equal(f.Former, []string{"Jarvis", "Eriphe"}) {
		t.Fatalf("Alfred: %+v", f)
	}
	// Back to Jarvis: it is no former name of itself.
	setBrain(c, &BrainConfig{DisplayName: "Jarvis"})
	c.noteResultsName(c.conf())
	if f, _ = folder(); f.Results != "Jarvis" || !slices.Equal(f.Former, []string{"Alfred", "Eriphe"}) {
		t.Fatalf("back: %+v", f)
	}

	// Its node kept the former one: said in the device's log.
	task, _ := c.runTask(ctx, runRequest{Prompt: "sum", Folders: []string{"Clients"}})
	c.logResults(task, []EventRow{{Type: "results_conflict", Data: map[string]any{"folder": "Clients", "kept": "Alfred", "wanted": "Jarvis"}},
		{Type: "tool_use", Data: map[string]any{"name": "Write", "input": map[string]any{"file_path": "/work/folders/Clients/Jarvis/devis.txt"}}}})
	log, _ := store.nodeLog("nas", 10, 0)
	var kinds []string
	for _, e := range log {
		kinds = append(kinds, e.Kind+" "+e.Detail)
	}
	if !slices.Contains(kinds, "results_conflict Alfred/ → Jarvis/") || !slices.Contains(kinds, "result Jarvis/devis.txt") {
		t.Fatalf("log %v", kinds)
	}
	setAssistantName("Jarvis")
	if got := tr("fr", "devices.log.results_conflict"); !strings.Contains(got, "ancien dossier") {
		t.Fatalf("text %q", got)
	}
}
