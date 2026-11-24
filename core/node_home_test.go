package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A node is maintained only while a skill approved and on names it as its
// home: computed, never in its file. The graph's row carries it (its
// satellite), node_find and node_read say it, and the brain's line says
// « entretenu par <skill> ».
func TestMaintainedIsComputed(t *testing.T) {
	c := graphCore(t)
	d, err := c.nodes.Create(nodestore.New{Title: "Mes mails", Author: "user"})
	if err != nil {
		t.Fatal(err)
	}
	id := d.Node.Meta.ID
	other := mustNode(t, c, nodestore.New{Title: "Ailleurs"})
	said := func() (row []string, line string, found []string) {
		sum, _ := c.nodes.Get(id)
		row = c.graphRow(sum).Maintained
		line = c.nodeLine(statusNode{sum, c.nodeStatus(sum)})
		out, _ := c.brainNodeFind(nodeFindIn{Query: "Mes mails"})
		raw, _ := json.Marshal(out)
		var got struct {
			Nodes []struct {
				ID         string   `json:"id"`
				Maintained []string `json:"maintained"`
			} `json:"nodes"`
		}
		json.Unmarshal(raw, &got)
		for _, n := range got.Nodes {
			if n.ID == id {
				found = n.Maintained
			}
		}
		return
	}
	if row, line, found := said(); row != nil || strings.Contains(line, "entretenu") || found != nil {
		t.Fatalf("maintained before any skill: %v %q %v", row, line, found)
	}

	// Written, waiting for its yes: not yet.
	md := "---\nname: tri\ndescription: Les mails.\nwhen: time daily 08:00\nnode: " + id + "\n---\nTiens la page.\n"
	k, _, err := c.store.addSkillVersion([]skillFile{{Path: "SKILL.md", Data: []byte(md)}}, skillFromEriphe)
	if err != nil {
		t.Fatal(err)
	}
	if row, _, _ := said(); row != nil {
		t.Fatalf("maintained by a skill waiting: %v", row)
	}
	// Approved: maintained, by it alone.
	if err := c.store.approveSkill(k.Name, k.Pending); err != nil {
		t.Fatal(err)
	}
	row, line, found := said()
	if !slices.Equal(row, []string{"tri"}) || !strings.Contains(line, ", entretenu par tri") || !slices.Equal(found, []string{"tri"}) {
		t.Fatalf("approved: %v %q %v", row, line, found)
	}
	if sum, _ := c.nodes.Get(other); c.graphRow(sum).Maintained != nil {
		t.Fatal("another node is maintained")
	}
	if raw, _ := c.nodes.Read(id, 0); strings.Contains(string(raw.Raw), "tri") {
		t.Fatalf("its file says who maintains it:\n%s", raw.Raw)
	}
	out, err := c.brainNodeRead(brainNodeReadIn{ID: id})
	if err != nil || !slices.Equal(out["maintained"].([]string), []string{"tri"}) {
		t.Fatalf("node_read: %v %v", out["maintained"], err)
	}
	// Off: a node like any other again.
	c.store.enableSkill("tri", false)
	if row, line, _ := said(); row != nil || strings.Contains(line, "entretenu") {
		t.Fatalf("maintained by a skill off: %v %q", row, line)
	}
}
