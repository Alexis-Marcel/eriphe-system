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
	"strings"
)

// Settings → Skills (skills.go): the user's skills, adding one from a
// folder or a zip, reading it (its SKILL.md, its files, its scripts
// marked), approving it, and a new version's diff before it is approved
// again.

func (w *web) skillRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/skills", w.auth(w.handleSkills))
	mux.Handle("POST /settings/skills", w.auth(w.handleUploadSkill))
	mux.Handle("GET /settings/skills/{name}", w.auth(w.handleSkill))
	mux.Handle("POST /settings/skills/{name}/approve", w.auth(w.handleApproveSkill))
	mux.Handle("POST /settings/skills/{name}/reject", w.auth(w.handleRejectSkill))
	mux.Handle("POST /settings/skills/{name}/enable", w.auth(w.handleEnableSkill))
	mux.Handle("POST /settings/skills/{name}/delete", w.auth(w.handleDeleteSkill))
}

func (w *web) handleSkills(rw http.ResponseWriter, r *http.Request, s *session) {
	skills, err := w.c.store.skills()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	w.render(rw, r, s, "settings", "", page{Title: "settings.skills", Nav: "settings", Tab: "skills", Data: skills})
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
}

func (w *web) handleSkill(rw http.ResponseWriter, r *http.Request, s *session) {
	k, err := w.c.store.skill(r.PathValue("name"))
	if err != nil {
		http.Redirect(rw, r, "/settings/skills", http.StatusSeeOther)
		return
	}
	d := skillPage{Skill: k}
	if d.Shown, err = w.c.store.skillVersion(orStr(k.Pending, k.Approved)); err != nil {
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
			if old, err = w.c.store.skillVersion(k.Approved); err != nil {
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
	if err := w.c.store.approveSkill(name, r.PostFormValue("sha")); err != nil {
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
	if err := w.c.store.deleteSkill(name); err != nil {
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
