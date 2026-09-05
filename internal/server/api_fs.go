package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PaulWeber-co/FileBrowserTest/internal/config"
	"github.com/PaulWeber-co/FileBrowserTest/internal/thumb"
	"github.com/PaulWeber-co/FileBrowserTest/internal/vfs"
)

type locationDTO struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	ReadOnly bool     `json:"readOnly"`
	Icon     string   `json:"icon,omitempty"`
	Color    string   `json:"color,omitempty"`
	Caps     vfs.Caps `json:"caps"`
	Detail   string   `json:"detail,omitempty"`
}

func (a *App) handleLocations(w http.ResponseWriter, r *http.Request, id Identity) {
	locs := a.cfg.Snapshot()
	out := make([]locationDTO, 0, len(locs))
	for _, l := range locs {
		d := locationDTO{
			ID: l.ID, Label: l.Label, Type: l.Type,
			ReadOnly: l.ReadOnly || id.ReadOnly, Icon: l.Icon, Color: l.Color,
			Detail: locationDetail(l),
		}
		// Fähigkeiten stehen ohne Verbindungsaufbau fest, weil sie am
		// Protokoll hängen. So bleibt die Seitenleiste sofort da.
		switch l.Type {
		case "smb":
			if l.SMB != nil && vfs.IsSMB1Dialect(l.SMB.Dialect) {
				// SMB1 kennt kein rekursives Löschen und kein Setzen der
				// Zeitstempel; beides blendet die Oberfläche dann aus.
				d.Caps = vfs.Caps{RandomRead: true, Rename: true, SpaceInfo: true}
			} else {
				d.Caps = vfs.Caps{RandomRead: true, Rename: true, Recursive: true, SetModTime: true, SpaceInfo: true}
			}
		case "sftp", "local":
			d.Caps = vfs.Caps{RandomRead: true, Rename: true, Recursive: true, SetModTime: true, SpaceInfo: true}
		case "webdav":
			d.Caps = vfs.Caps{RandomRead: true, Rename: true, ServerCopy: true, Recursive: true}
		case "ftp":
			d.Caps = vfs.Caps{Rename: true, Recursive: true, SetModTime: true}
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"locations": out})
}

// locationDetail beschreibt das Ziel in einer Zeile für die Seitenleiste.
func locationDetail(l config.Location) string {
	switch l.Type {
	case "smb":
		if l.SMB != nil {
			return fmt.Sprintf("smb://%s/%s", l.SMB.Host, l.SMB.Share)
		}
	case "ftp":
		if l.FTP != nil {
			return fmt.Sprintf("ftp://%s", l.FTP.Host)
		}
	case "sftp":
		if l.SFTP != nil {
			return fmt.Sprintf("sftp://%s@%s", l.SFTP.User, l.SFTP.Host)
		}
	case "webdav":
		if l.WebDAV != nil {
			return l.WebDAV.URL
		}
	case "local":
		if l.Local != nil {
			return l.Local.Path
		}
	}
	return l.Type
}

type listResponse struct {
	Location string     `json:"location"`
	Path     string     `json:"path"`
	Entries  []entryDTO `json:"entries"`
	Parent   string     `json:"parent"`
	ReadOnly bool       `json:"readOnly"`
	Space    *vfs.Space `json:"space,omitempty"`
	Took     int64      `json:"tookMs"`
	Crumbs   []crumb    `json:"crumbs"`
}

type crumb struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type entryDTO struct {
	Name    string     `json:"name"`
	Size    int64      `json:"size"`
	IsDir   bool       `json:"dir"`
	ModTime time.Time  `json:"mtime"`
	Kind    thumb.Kind `json:"kind"`
	Thumb   bool       `json:"thumb,omitempty"`
	Link    bool       `json:"link,omitempty"`
}

func (a *App) toDTO(entries []vfs.Entry, showHidden bool) []entryDTO {
	hasFF := a.thumbs.FFmpegAvailable()
	out := make([]entryDTO, 0, len(entries))
	for _, e := range entries {
		if !showHidden && isHidden(e.Name) {
			continue
		}
		d := entryDTO{Name: e.Name, Size: e.Size, IsDir: e.IsDir, ModTime: e.ModTime, Link: e.Symlink}
		if e.IsDir {
			d.Kind = "folder"
		} else {
			d.Kind = thumb.KindOf(e.Name)
			d.Thumb = thumb.CanThumb(e.Name, hasFF)
		}
		out = append(out, d)
	}
	return out
}

func isHidden(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch strings.ToLower(name) {
	case "system volume information", "$recycle.bin", "thumbs.db", "desktop.ini", ".ds_store", "found.000":
		return true
	}
	return false
}

func (a *App) handleList(w http.ResponseWriter, r *http.Request, id Identity) {
	locID := r.URL.Query().Get("loc")
	p := vfs.Clean(r.URL.Query().Get("path"))
	c, loc, ok := a.clientFor(w, locID)
	if !ok {
		return
	}
	if queryBool(r, "refresh") {
		c.Invalidate(p)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	start := time.Now()
	entries, err := c.List(ctx, p)
	if err != nil {
		fail(w, err)
		return
	}
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "name"
	}
	vfs.SortEntries(entries, sortBy, queryBool(r, "desc"))

	resp := listResponse{
		Location: locID,
		Path:     p,
		Entries:  a.toDTO(entries, queryBool(r, "hidden")),
		Parent:   vfs.Dir(p),
		ReadOnly: loc.ReadOnly || id.ReadOnly,
		Took:     time.Since(start).Milliseconds(),
		Crumbs:   crumbsFor(p),
	}
	if queryBool(r, "space") {
		if sp, err := c.Space(ctx, p); err == nil {
			resp.Space = &sp
		}
	}
	if !id.Guest {
		a.state.PushRecent(id.Name, Favorite{LocationID: locID, Path: p, Name: displayName(loc.Label, p)})
	}
	writeJSON(w, http.StatusOK, resp)
}

func displayName(label, p string) string {
	if p == "" {
		return label
	}
	return vfs.Base(p)
}

func crumbsFor(p string) []crumb {
	out := []crumb{}
	if p == "" {
		return out
	}
	parts := strings.Split(p, "/")
	cur := ""
	for _, part := range parts {
		if cur == "" {
			cur = part
		} else {
			cur = cur + "/" + part
		}
		out = append(out, crumb{Name: part, Path: cur})
	}
	return out
}

func (a *App) handleStat(w http.ResponseWriter, r *http.Request, id Identity) {
	c, _, ok := a.clientFor(w, r.URL.Query().Get("loc"))
	if !ok {
		return
	}
	p := vfs.Clean(r.URL.Query().Get("path"))
	e, err := c.Stat(r.Context(), p)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.toDTO([]vfs.Entry{e}, true)[0])
}

func (a *App) handleSpace(w http.ResponseWriter, r *http.Request, id Identity) {
	c, _, ok := a.clientFor(w, r.URL.Query().Get("loc"))
	if !ok {
		return
	}
	sp, err := c.Space(r.Context(), vfs.Clean(r.URL.Query().Get("path")))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sp)
}

type pathRequest struct {
	Loc  string `json:"loc"`
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

func (a *App) handleMkdir(w http.ResponseWriter, r *http.Request, id Identity) {
	var req pathRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	if !vfs.ValidName(req.Name) {
		failWith(w, http.StatusBadRequest, "Ungültiger Ordnername.")
		return
	}
	c, loc, ok := a.clientFor(w, req.Loc)
	if !ok {
		return
	}
	if loc.ReadOnly {
		failWith(w, http.StatusForbidden, "Dieser Speicherort ist schreibgeschützt.")
		return
	}
	target := vfs.Join(req.Path, req.Name)
	if err := c.Mkdir(r.Context(), target); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": target})
}

type renameRequest struct {
	Loc  string `json:"loc"`
	Path string `json:"path"`
	Name string `json:"name"`
}

func (a *App) handleRename(w http.ResponseWriter, r *http.Request, id Identity) {
	var req renameRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	if !vfs.ValidName(req.Name) {
		failWith(w, http.StatusBadRequest, "Ungültiger Name.")
		return
	}
	c, loc, ok := a.clientFor(w, req.Loc)
	if !ok {
		return
	}
	if loc.ReadOnly {
		failWith(w, http.StatusForbidden, "Dieser Speicherort ist schreibgeschützt.")
		return
	}
	src := vfs.Clean(req.Path)
	if src == "" {
		failWith(w, http.StatusBadRequest, "Die Wurzel kann nicht umbenannt werden.")
		return
	}
	dst := vfs.Join(vfs.Dir(src), req.Name)
	if err := c.Rename(r.Context(), src, dst); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": dst})
}

type deleteRequest struct {
	Loc   string   `json:"loc"`
	Items []string `json:"items"`
}

func (a *App) handleDelete(w http.ResponseWriter, r *http.Request, id Identity) {
	var req deleteRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	c, loc, ok := a.clientFor(w, req.Loc)
	if !ok {
		return
	}
	if loc.ReadOnly {
		failWith(w, http.StatusForbidden, "Dieser Speicherort ist schreibgeschützt.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	var failed []string
	for _, item := range req.Items {
		p := vfs.Clean(item)
		if p == "" {
			continue
		}
		if err := c.Remove(ctx, p, true); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %s", vfs.Base(p), friendly(err)))
		}
	}
	if len(failed) > 0 {
		writeJSON(w, http.StatusMultiStatus, map[string]any{"ok": false, "failed": failed})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type transferRequest struct {
	Op        string   `json:"op"` // move | copy
	SrcLoc    string   `json:"srcLoc"`
	Items     []string `json:"items"`
	DstLoc    string   `json:"dstLoc"`
	DstPath   string   `json:"dstPath"`
	Overwrite bool     `json:"overwrite"`
}

func (a *App) handleTransfer(w http.ResponseWriter, r *http.Request, id Identity) {
	var req transferRequest
	if err := decodeBody(w, r, &req); err != nil {
		failWith(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Op != "move" && req.Op != "copy" {
		failWith(w, http.StatusBadRequest, "op muss move oder copy sein.")
		return
	}
	src, srcLoc, ok := a.clientFor(w, req.SrcLoc)
	if !ok {
		return
	}
	dst, dstLoc, ok := a.clientFor(w, req.DstLoc)
	if !ok {
		return
	}
	if dstLoc.ReadOnly {
		failWith(w, http.StatusForbidden, "Das Ziel ist schreibgeschützt.")
		return
	}
	if req.Op == "move" && srcLoc.ReadOnly {
		failWith(w, http.StatusForbidden, "Die Quelle ist schreibgeschützt.")
		return
	}
	dstBase := vfs.Clean(req.DstPath)
	items := make([]string, 0, len(req.Items))
	for _, it := range req.Items {
		p := vfs.Clean(it)
		if p == "" {
			continue
		}
		// Ein Ordner darf nicht in sich selbst verschoben werden.
		if req.SrcLoc == req.DstLoc && (dstBase == p || strings.HasPrefix(dstBase+"/", p+"/")) {
			failWith(w, http.StatusBadRequest, fmt.Sprintf("%q kann nicht in sich selbst verschoben werden.", vfs.Base(p)))
			return
		}
		items = append(items, p)
	}
	if len(items) == 0 {
		failWith(w, http.StatusBadRequest, "Keine Objekte ausgewählt.")
		return
	}

	label := fmt.Sprintf("%d Objekt(e) nach %s", len(items), dstLoc.Label)
	job := a.jobs.Start(id.Name, req.Op, label, func(ctx context.Context, j *Job) error {
		return a.runTransfer(ctx, j, req, src, dst)
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": job.ID})
}

// runTransfer führt Kopieren/Verschieben aus - möglichst serverseitig,
// sonst Byte für Byte durch SpeedNAS hindurch.
func (a *App) runTransfer(ctx context.Context, j *Job, req transferRequest, src, dst *vfs.Client) error {
	sameLoc := req.SrcLoc == req.DstLoc
	dstBase := vfs.Clean(req.DstPath)

	// Schneller Weg: Verschieben innerhalb desselben Ortes ist ein Rename.
	if sameLoc && req.Op == "move" && src.Caps().Rename {
		for _, item := range vfsClean(req.Items) {
			if err := ctx.Err(); err != nil {
				return err
			}
			target := vfs.Join(dstBase, vfs.Base(item))
			j.setCurrent(vfs.Base(item))
			if !req.Overwrite {
				if _, err := dst.Stat(ctx, target); err == nil {
					var e error
					target, e = uniqueName(ctx, dst, target)
					if e != nil {
						return e
					}
				}
			}
			if err := src.Rename(ctx, item, target); err != nil {
				return err
			}
			j.fileDone()
		}
		return nil
	}

	// Sonst: erst planen (Anzahl und Größe), damit der Fortschritt stimmt.
	plan, err := planTransfer(ctx, src, req.Items)
	if err != nil {
		return err
	}
	j.setPlan(len(plan.files), plan.bytes)

	for _, item := range vfsClean(req.Items) {
		if err := ctx.Err(); err != nil {
			return err
		}
		target := vfs.Join(dstBase, vfs.Base(item))
		if !req.Overwrite {
			if _, err := dst.Stat(ctx, target); err == nil {
				if target, err = uniqueName(ctx, dst, target); err != nil {
					return err
				}
			}
		}
		if err := a.copyTree(ctx, j, src, dst, item, target, sameLoc); err != nil {
			return err
		}
		if req.Op == "move" {
			if err := src.Remove(ctx, item, true); err != nil {
				return fmt.Errorf("kopiert, aber Quelle nicht löschbar: %w", err)
			}
		}
	}
	return nil
}

type transferPlan struct {
	files []string
	bytes int64
}

func planTransfer(ctx context.Context, src *vfs.Client, items []string) (transferPlan, error) {
	var p transferPlan
	var walk func(string) error
	walk = func(path string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		e, err := src.Stat(ctx, path)
		if err != nil {
			return err
		}
		if !e.IsDir {
			p.files = append(p.files, path)
			p.bytes += e.Size
			return nil
		}
		entries, err := src.List(ctx, path)
		if err != nil {
			return err
		}
		for _, c := range entries {
			if err := walk(vfs.Join(path, c.Name)); err != nil {
				return err
			}
		}
		return nil
	}
	for _, it := range vfsClean(items) {
		if err := walk(it); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (a *App) copyTree(ctx context.Context, j *Job, src, dst *vfs.Client, from, to string, sameLoc bool) error {
	e, err := src.Stat(ctx, from)
	if err != nil {
		return err
	}
	if e.IsDir {
		if err := dst.Mkdir(ctx, to); err != nil && !errors.Is(err, vfs.ErrExists) {
			return err
		}
		entries, err := src.List(ctx, from)
		if err != nil {
			return err
		}
		for _, c := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := a.copyTree(ctx, j, src, dst, vfs.Join(from, c.Name), vfs.Join(to, c.Name), sameLoc); err != nil {
				return err
			}
		}
		return nil
	}

	j.setCurrent(e.Name)
	// Serverseitiges Kopieren spart den kompletten Umweg über diesen Rechner.
	if sameLoc && dst.Caps().ServerCopy {
		if err := dst.ServerCopy(ctx, from, to); err == nil {
			j.addBytes(e.Size)
			j.fileDone()
			return nil
		}
	}
	rc, _, err := src.StreamAt(ctx, from, 0, a.prefetchOpts())
	if err != nil {
		return err
	}
	defer rc.Close()

	pr := &progressReader{r: rc, job: j}
	if _, err := dst.Write(ctx, to, pr, e.Size); err != nil {
		return err
	}
	j.fileDone()
	return nil
}

type progressReader struct {
	r   io.Reader
	job *Job
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.job.addBytes(int64(n))
	}
	return n, err
}

// uniqueName hängt " (2)", " (3)" ... an, bis der Name frei ist.
func uniqueName(ctx context.Context, c *vfs.Client, target string) (string, error) {
	dir := vfs.Dir(target)
	base := vfs.Base(target)
	stem, ext := base, ""
	if i := strings.LastIndex(base, "."); i > 0 {
		stem, ext = base[:i], base[i:]
	}
	for i := 2; i < 500; i++ {
		cand := vfs.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, err := c.Stat(ctx, cand); errors.Is(err, vfs.ErrNotFound) {
			return cand, nil
		} else if err != nil && !errors.Is(err, vfs.ErrNotFound) {
			return "", err
		}
	}
	return "", fmt.Errorf("kein freier Name für %q gefunden", base)
}

func vfsClean(items []string) []string {
	out := make([]string, 0, len(items))
	for _, i := range items {
		if p := vfs.Clean(i); p != "" {
			out = append(out, p)
		}
	}
	return out
}
