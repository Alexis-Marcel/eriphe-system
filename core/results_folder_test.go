package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// setNodeInfo adds fields to what a node last reported.
func setNodeInfo(t *testing.T, store *Store, name string, fields map[string]any) {
	t.Helper()
	nodes, _ := store.nodes()
	for _, n := range nodes {
		if n.Name == name {
			info := map[string]any{}
			json.Unmarshal(n.Info, &info)
			for k, v := range fields {
				info[k] = v
			}
			raw, _ := json.Marshal(info)
			store.setNodeStatus(name, true, raw)
			return
		}
	}
	t.Fatalf("no node %s", name)
}

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

	// Named Jarvis, on a node too old to name the folder: the worker is
	// told Eriphe/, where that node really mounts it.
	setBrain(c, &BrainConfig{DisplayName: "Jarvis"})
	c.noteResultsName(c.conf())
	f, prompt = folder()
	if !strings.Contains(prompt, "/work/folders/Clients/Eriphe/") || strings.Contains(prompt, "Jarvis/") {
		t.Fatalf("old node: %q", prompt)
	}
	// Its node updated: Jarvis/, the former names, newest first.
	setNodeInfo(t, store, "nas", map[string]any{"results_named": true})
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

	// A release of eriphe-node names it too; a build of main by its flag only.
	for v, want := range map[string]bool{"0.6.4": false, "0.7.0": true, "0.7.1": true, "0.7.0-dev+abc": false, "": false, "sha-abc": false} {
		if got := nodeNamesResults(nodeInfo{Version: v}); got != want {
			t.Errorf("version %q: %v", v, got)
		}
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
