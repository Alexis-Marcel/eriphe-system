package main

import (
	"encoding/json"
	"errors"
	"html"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Settings → Skills (skills.go): the user's skills, adding one from a
// folder or a zip, reading it (its SKILL.md, its files, its scripts
// marked), approving it, and a new version's diff before it is approved
// again.

func (w *web) skillRoutes(mux *webMux) {
	mux.Handle("GET /settings/skills", w.auth(w.handleSkills))
	mux.Handle("POST /settings/skills", w.auth(w.handleUploadSkill))
	mux.Handle("GET /settings/skills/{name}", w.auth(w.handleSkill))
	mux.Handle("GET /settings/skills/{name}/runs", w.auth(w.handleSkillRuns))
	mux.Handle("POST /settings/skills/{name}/calls/{id}/{action}", w.auth(w.handleRunCall))
	mux.Handle("POST /settings/skills/{name}/approve", w.auth(w.handleApproveSkill))
	mux.Handle("POST /settings/skills/{name}/reject", w.auth(w.handleRejectSkill))
	mux.Handle("POST /settings/skills/{name}/enable", w.auth(w.handleEnableSkill))
	mux.Handle("POST /settings/skills/{name}/delete", w.auth(w.handleDeleteSkill))
}

// skillRuns are a skill's runs and totals; live while one is open.
func (w *web) skillRuns(name string) ([]runView, skillTotals, bool) {
	runs, tot := w.c.skillReport(name, skillRunsShown, w.c.notifyTime())
	live := len(runs) > 0 && runs[0].State == skillRunOpen
	return runs, tot, live
}

// handleSkillRuns is the runs' part of a skill's page, reloaded while a run
// is going.
func (w *web) handleSkillRuns(rw http.ResponseWriter, r *http.Request, s *session) {
	d := skillPage{Skill: &Skill{Name: r.PathValue("name")}}
	d.Runs, d.Totals, d.Live = w.skillRuns(d.Skill.Name)
	w.render(rw, r, s, "settings", "skill-runs-frag", page{Data: d})
}

// skillsPage is Settings → Skills: the skills, each triggered one's
// trigger in words, and what each uses.
type skillsPage struct {
	Skills   any
	Triggers map[string]string // a triggered skill's trigger, in words
	Uses     map[string]string // what a skill uses, in words (skillUses)
}

func (w *web) handleSkills(rw http.ResponseWriter, r *http.Request, s *session) {
	skills, err := w.c.store.skills()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	triggers, uses := w.skillTexts(w.lang(r))
	w.render(rw, r, s, "settings", "", page{Title: "settings.skills", Nav: "settings", Tab: "skills",
		Data: skillsPage{Skills: skills, Triggers: triggers, Uses: uses}})
}

// skillFileView is a file of the version shown.
type skillFileView struct {
	Path   string
	Size   int
	Script bool
}

type skillPage struct {
	Skill   *Skill
	Shown   *skillVersion // the pending version if any, else the approved one
	SkillMD string
	Files   []skillFileView
	Scripts int
	Diff    []fileDiff // with a pending version: what changed since the approved one, or all of it
	Update  bool       // the pending version replaces an approved one
	Runs    []runView  // a triggered skill's last runs (skill_report.go)
	Totals  skillTotals
	Live    bool // a run is going: its runs reload
}

func (w *web) handleSkill(rw http.ResponseWriter, r *http.Request, s *session) {
	k, err := w.c.store.skill(r.PathValue("name"))
	if err != nil {
		http.Redirect(rw, r, "/settings/skills", http.StatusSeeOther)
		return
	}
	d := skillPage{Skill: k}
	d.Runs, d.Totals, d.Live = w.skillRuns(k.Name)
	// A version that does not read is shown still, its files and why
	// (Skill.Broken).
	var se *skillError
	if d.Shown, err = w.c.store.skillVersion(orStr(k.Pending, k.Approved)); err != nil && (d.Shown == nil || !errors.As(err, &se)) {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, f := range d.Shown.Files {
		v := skillFileView{Path: f.Path, Size: len(f.Data), Script: isScript(f)}
		if v.Script {
			d.Scripts++
		}
		if f.Path == "SKILL.md" {
			d.SkillMD = string(f.Data)
		}
		d.Files = append(d.Files, v)
	}
	if k.Pending != "" {
		var old *skillVersion
		if k.Approved != "" {
			d.Update = true
			if old, err = w.c.store.skillVersion(k.Approved); err != nil && (old == nil || !errors.As(err, &se)) {
				http.Error(rw, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		d.Diff = diffSkills(old, d.Shown)
	}
	w.render(rw, r, s, "settings", "", page{Title: "settings.skills", Nav: "settings", Tab: "skills", View: "skill", Data: d})
}

// handleUploadSkill reads a skill from the form: a zip, or a folder's
// files with their paths (the browser sends them in "paths", in the order
// of the files: a file part's name loses its folders).
func (w *web) handleUploadSkill(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	r.Body = http.MaxBytesReader(rw, r.Body, 2*maxSkillBytes)
	if err := r.ParseMultipartForm(maxSkillBytes); err != nil {
		w.skillError(rw, lang, skillErr("skills.err.toobig", "8"))
		return
	}
	defer r.MultipartForm.RemoveAll()
	var parts []*multipart.FileHeader
	for _, fh := range r.MultipartForm.File["files"] {
		if fh.Filename != "" || fh.Size > 0 { // the input not picked sends an empty part
			parts = append(parts, fh)
		}
	}
	var paths []string
	json.Unmarshal([]byte(r.FormValue("paths")), &paths)
	var files []skillFile
	for i, fh := range parts {
		if fh.Size > maxSkillFile {
			w.skillError(rw, lang, skillErr("skills.err.filesize", fh.Filename))
			return
		}
		f, err := fh.Open()
		if err != nil {
			w.skillError(rw, lang, err)
			return
		}
		data, err := io.ReadAll(io.LimitReader(f, maxSkillFile+1))
		f.Close()
		if err != nil {
			w.skillError(rw, lang, err)
			return
		}
		name := fh.Filename
		if i < len(paths) && paths[i] != "" {
			name = paths[i]
		}
		if len(parts) == 1 && strings.HasSuffix(strings.ToLower(name), ".zip") {
			if files, err = filesFromZip(data); err != nil {
				w.skillError(rw, lang, err)
				return
			}
			break
		}
		// A browser keeps no executable bit: what runs is told by its folder or extension.
		files = append(files, skillFile{Path: name, Data: data})
	}
	if len(files) == 0 {
		w.skillError(rw, lang, skillErr("skills.err.empty", ""))
		return
	}
	k, _, err := w.c.store.addSkillVersion(files, "upload")
	if err != nil {
		w.skillError(rw, lang, err)
		return
	}
	w.c.settleCards(cardSkill) // the brain's version it replaced
	log.Printf("web: skill %s uploaded (%s)", k.Name, orStr(k.Pending, k.Approved))
	rw.Header().Set("HX-Redirect", "/settings/skills/"+url.PathEscape(k.Name))
	rw.WriteHeader(http.StatusOK)
}

// skillError says why a skill was refused, in the upload form.
func (w *web) skillError(rw http.ResponseWriter, lang string, err error) {
	msg := tr(lang, "err.internal")
	var se *skillError
	if errors.As(err, &se) {
		msg = tr(lang, se.Key)
		if strings.Contains(msg, "%s") {
			msg = strings.Replace(msg, "%s", se.Arg, 1)
		}
	} else {
		log.Printf("web: skill upload: %v", err)
	}
	rw.Header().Set("HX-Retarget", "#skill-result")
	rw.Header().Set("HX-Reswap", "innerHTML")
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(rw, `<p class="alert" role="alert">`+html.EscapeString(msg)+`</p>`)
}

func (w *web) handleApproveSkill(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	if err := w.c.approveSkill(name, r.PostFormValue("sha")); err != nil {
		w.toast(rw, tr(w.lang(r), "skills.err.changed"), false)
		return
	}
	log.Printf("web: skill %s approved (%s)", name, r.PostFormValue("sha"))
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}

func (w *web) handleRejectSkill(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	if err := w.c.store.rejectSkill(name); err != nil {
		w.toast(rw, tr(w.lang(r), "err.internal"), false)
		return
	}
	w.c.settleCards(cardSkill)
	log.Printf("web: skill %s: the version waiting is dropped", name)
	if _, err := w.c.store.skill(name); err != nil {
		rw.Header().Set("HX-Redirect", "/settings/skills")
	} else {
		rw.Header().Set("HX-Refresh", "true")
	}
	rw.WriteHeader(http.StatusOK)
}

func (w *web) handleEnableSkill(rw http.ResponseWriter, r *http.Request, s *session) {
	name, on := r.PathValue("name"), r.PostFormValue("on") == "1"
	if err := w.c.store.enableSkill(name, on); err != nil {
		w.toast(rw, tr(w.lang(r), "err.internal"), false)
		return
	}
	log.Printf("web: skill %s on: %v", name, on)
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}

func (w *web) handleDeleteSkill(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	if err := w.c.deleteSkill(name); err != nil {
		w.toast(rw, tr(w.lang(r), "err.internal"), false)
		return
	}
	log.Printf("web: skill %s deleted", name)
	rw.Header().Set("HX-Redirect", "/settings/skills")
	rw.WriteHeader(http.StatusOK)
}

// Human is the file's size, as people read it.
func (v skillFileView) Human() string { return humanSize(int64(v.Size)) }

// Short is the start of a version's hash, as the page shows it.
func (v *skillVersion) Short() string { return v.SHA[:min(12, len(v.SHA))] }

// skillTexts say each approved skill's trigger, and what it uses, in
// words.
func (w *web) skillTexts(lang string) (triggers, uses map[string]string) {
	triggers, uses = map[string]string{}, map[string]string{}
	ks, err := w.c.store.skills()
	if err != nil {
		return
	}
	for _, k := range ks {
		if k.Approved == "" {
			continue
		}
		m, ok := w.c.skillManifestOf(k.Approved)
		if !ok {
			continue
		}
		if m.Trigger != nil {
			triggers[k.Name] = m.Trigger.Text(lang)
		}
		if u := m.Uses.Text(lang); u != "" {
			uses[k.Name] = u
		}
	}
	return
}

// handleRunCall answers one action a run proposed: yes runs it as kept, no
// drops it; the others of its card wait for theirs.
func (w *web) handleRunCall(rw http.ResponseWriter, r *http.Request, s *session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	a, err := w.c.accessAskOf(id)
	if err != nil || a.Run == 0 {
		http.NotFound(rw, r)
		return
	}
	if run := w.c.runByID(a.Run); run == nil || run.Skill != r.PathValue("name") {
		http.NotFound(rw, r)
		return
	}
	approve := r.PathValue("action") == "approve"
	if err := w.c.answerCall(r.Context(), id, approve, "page"); err != nil && err != errCardDecided {
		w.toast(rw, w.formError(r, err), false)
		return
	}
	w.c.bump()
	d := skillPage{Skill: &Skill{Name: r.PathValue("name")}}
	d.Runs, d.Totals, d.Live = w.skillRuns(d.Skill.Name)
	w.render(rw, r, s, "settings", "skill-runs-frag", page{Data: d})
}

// approveSkill approves a skill's pending version, with the monitor it brings.
func (c *core) approveSkill(name, sha string) error {
	undo, err := c.skillMonitorMade(name, sha) // the monitor it brings, approved with it (skill_write.go)
	if err != nil {
		return err
	}
	if err := c.store.approveSkill(name, sha); err != nil {
		undo()
		return err
	}
	c.settleCards(cardSkill) // the card of the version it was, if the brain wrote it
	return nil
}

// deleteSkill deletes a skill and settles the card of a version that waited.
func (c *core) deleteSkill(name string) error {
	if err := c.store.deleteSkill(name); err != nil {
		return err
	}
	c.settleCards(cardSkill)
	return nil
}
