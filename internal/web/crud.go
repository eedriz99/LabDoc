package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

// queryer is satisfied by both *sql.DB and *sql.Tx. The pool holds a single
// connection, so code inside a transaction must never touch s.db.
type queryer interface {
	Query(string, ...any) (*sql.Rows, error)
}

type base struct {
	Title string
	Nav   []*Entity
}

// listTag is one highlighted badge (e.g. SSD, HDD) inside a list cell.
type listTag struct{ Text, Class string }

type listCell struct {
	Text string
	Tags []listTag
}

type listRow struct {
	ID    int64
	Cells []listCell
}

type listPage struct {
	base
	Entity *Entity
	Cols   []Field
	Rows   []listRow
}

type choice struct {
	Value, Label string
	Selected     bool
}

type formPage struct {
	base
	Entity  *Entity
	ID      int64
	Action  string
	Error   string
	Values  map[string]string
	Choices map[string][]choice
}

type histItem struct {
	At, Note string
	Changes  []string
}

type historyPage struct {
	base
	Entity *Entity
	ID     int64
	Items  []histItem
}

type indexEntry struct {
	Entity *Entity
	Count  int
}

type indexPage struct {
	base
	Entries []indexEntry
}

var errNotFound = errors.New("not found")

func (s *Server) newBase(title string) base { return base{Title: title, Nav: entities} }

func (s *Server) fail(w http.ResponseWriter, what string, err error) {
	log.Printf("%s: %v", what, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}

// queryAll reads every row into memory so the connection is free again
// before the caller issues another query.
func queryAll(q queryer, query string, args ...any) ([][]any, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

// snapshot returns the raw stored state of one row, including many-to-many
// links, as used for revisions and for pre-filling the edit form.
func (e *Entity) snapshot(q queryer, id int64) (map[string]any, error) {
	names := e.columnNames()
	rows, err := queryAll(q, "SELECT "+strings.Join(names, ", ")+" FROM "+e.Table+" WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errNotFound
	}
	snap := map[string]any{"id": id}
	for i, n := range names {
		snap[n] = rows[0][i]
	}
	for _, f := range e.Fields {
		if f.Kind != kMulti {
			continue
		}
		rs, err := queryAll(q, "SELECT "+f.Join.Other+" FROM "+f.Join.Table+" WHERE "+f.Join.Owner+" = ? ORDER BY 1", id)
		if err != nil {
			return nil, err
		}
		ids := []any{}
		for _, r := range rs {
			ids = append(ids, r[0])
		}
		snap[f.Name] = ids
	}
	return snap, nil
}

func addRevision(tx *sql.Tx, e *Entity, id int64, snap map[string]any, note string) error {
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO revisions (entity_type, entity_id, snapshot_json, note) VALUES (?, ?, ?, ?)",
		e.Key, id, string(b), note)
	return err
}

// parse validates the posted form and returns column values in the order of
// scalarFields.
func (e *Entity) parse(r *http.Request) ([]any, error) {
	var args []any
	for _, f := range e.scalarFields() {
		if f.Kind == kChecks {
			picked := map[string]bool{}
			for _, v := range r.PostForm[f.Name] {
				if !slices.Contains(f.Options, v) {
					return nil, fmt.Errorf("%s has an invalid value", f.Label)
				}
				picked[v] = true
			}
			// Store in option order so the same selection is always the same string.
			var canon []string
			for _, o := range f.Options {
				if picked[o] {
					canon = append(canon, o)
				}
			}
			if len(canon) == 0 && f.Required {
				return nil, fmt.Errorf("%s is required", f.Label)
			}
			args = append(args, strings.Join(canon, ","))
			continue
		}
		v := strings.TrimSpace(r.PostFormValue(f.Name))
		if f.Kind == kBool {
			if v == "1" {
				args = append(args, 1)
			} else {
				args = append(args, 0)
			}
			continue
		}
		if v == "" {
			if f.Required {
				return nil, fmt.Errorf("%s is required", f.Label)
			}
			switch f.Kind {
			case kNumber, kRef:
				args = append(args, nil)
			default:
				args = append(args, "")
			}
			continue
		}
		switch f.Kind {
		case kNumber, kRef:
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%s must be a number", f.Label)
			}
			args = append(args, n)
		case kSelect:
			ok := false
			for _, o := range f.Options {
				ok = ok || o == v
			}
			if !ok {
				return nil, fmt.Errorf("%s has an invalid value", f.Label)
			}
			args = append(args, v)
		default:
			args = append(args, v)
		}
	}
	return args, nil
}

func friendlyErr(e *Entity, err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE constraint failed: connections."):
		return "That port is already used by another connection."
	case strings.Contains(msg, "UNIQUE constraint"):
		return "That value already exists; it must be unique."
	case strings.Contains(msg, "CHECK constraint") && e.Key == "ips":
		return "Invalid combination of values (an IP assignment needs a device or a service)."
	case strings.Contains(msg, "CHECK constraint"):
		return "Invalid combination of values."
	}
	// Validation messages are written lowercase (Go convention); capitalize for display.
	r, n := utf8.DecodeRuneInString(msg)
	return string(unicode.ToUpper(r)) + msg[n:]
}

// write creates (id == 0) or updates a row, its links and its revision in one
// transaction. It returns the row id.
func (s *Server) write(e *Entity, id int64, args []any, multi map[string][]string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	cols := e.columnNames()
	note := "updated"
	var before map[string]any
	if id == 0 {
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ")
		res, err := tx.Exec("INSERT INTO "+e.Table+" ("+strings.Join(cols, ", ")+") VALUES ("+marks+")", args...)
		if err != nil {
			return 0, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return 0, err
		}
		note = "created"
	} else {
		if before, err = e.snapshot(tx, id); err != nil {
			return 0, err
		}
		sets := strings.Join(cols, " = ?, ") + " = ?"
		if _, err := tx.Exec("UPDATE "+e.Table+" SET "+sets+" WHERE id = ?", append(args, id)...); err != nil {
			return 0, err
		}
	}

	for _, f := range e.Fields {
		if f.Kind != kMulti {
			continue
		}
		j := f.Join
		if _, err := tx.Exec("DELETE FROM "+j.Table+" WHERE "+j.Owner+" = ?", id); err != nil {
			return 0, err
		}
		for _, v := range multi[f.Name] {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("%s has an invalid value", f.Label)
			}
			if _, err := tx.Exec("INSERT OR IGNORE INTO "+j.Table+" ("+j.Owner+", "+j.Other+") VALUES (?, ?)", id, n); err != nil {
				return 0, err
			}
		}
	}

	after, err := e.snapshot(tx, id)
	if err != nil {
		return 0, err
	}
	// Skip the revision when nothing actually changed.
	if before != nil {
		b1, _ := json.Marshal(before)
		b2, _ := json.Marshal(after)
		if string(b1) == string(b2) {
			return id, tx.Commit()
		}
	}
	if err := addRevision(tx, e, id, after, note); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Server) choices(e *Entity, values map[string]string, multi map[string][]string) (map[string][]choice, error) {
	out := map[string][]choice{}
	for _, f := range e.Fields {
		switch f.Kind {
		case kSelect:
			for _, o := range f.Options {
				out[f.Name] = append(out[f.Name], choice{Value: o, Label: o, Selected: values[f.Name] == o})
			}
		case kChecks:
			for _, o := range f.Options {
				sel := slices.Contains(strings.Split(values[f.Name], ","), o)
				out[f.Name] = append(out[f.Name], choice{Value: o, Label: o, Selected: sel})
			}
		case kRef, kMulti:
			r := byKey[f.Ref]
			rows, err := queryAll(s.db, "SELECT id, "+r.LabelSQL+" FROM "+r.Table+" ORDER BY "+r.OrderBy+", id")
			if err != nil {
				return nil, err
			}
			sel := map[string]bool{}
			if f.Kind == kMulti {
				for _, v := range multi[f.Name] {
					sel[v] = true
				}
			} else if v := values[f.Name]; v != "" {
				sel[v] = true
			}
			for _, row := range rows {
				v := str(row[0])
				out[f.Name] = append(out[f.Name], choice{Value: v, Label: str(row[1]), Selected: sel[v]})
			}
		}
	}
	return out, nil
}

func (s *Server) renderForm(w http.ResponseWriter, e *Entity, id int64, values map[string]string, multi map[string][]string, errMsg string) {
	ch, err := s.choices(e, values, multi)
	if err != nil {
		s.fail(w, "choices", err)
		return
	}
	title, action := "New "+e.Singular, "/"+e.Key
	if id != 0 {
		title, action = "Edit "+e.Singular, "/"+e.Key+"/"+strconv.FormatInt(id, 10)
	}
	s.render(w, "form.html", formPage{
		base: s.newBase(title), Entity: e, ID: id, Action: action,
		Error: errMsg, Values: values, Choices: ch,
	})
}

func idParam(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

func (s *Server) mountCRUD(r chi.Router, e *Entity) {
	base := "/" + e.Key

	r.Get(base, func(w http.ResponseWriter, _ *http.Request) {
		rows, err := queryAll(s.db, e.listSQL())
		if err != nil {
			s.fail(w, "list "+e.Key, err)
			return
		}
		page := listPage{base: s.newBase(e.Plural), Entity: e, Cols: e.listFields()}
		for _, row := range rows {
			id, _ := strconv.ParseInt(str(row[0]), 10, 64)
			lr := listRow{ID: id}
			for i, c := range row[1:] {
				cell := listCell{Text: str(c)}
				if (page.Cols[i].Kind == kChecks || page.Cols[i].Badge) && cell.Text != "" {
					for _, t := range strings.Split(cell.Text, ",") {
						cell.Tags = append(cell.Tags, listTag{Text: t, Class: strings.ToLower(t)})
					}
				}
				lr.Cells = append(lr.Cells, cell)
			}
			page.Rows = append(page.Rows, lr)
		}
		s.render(w, "list.html", page)
	})

	r.Get(base+"/new", func(w http.ResponseWriter, r *http.Request) {
		values := map[string]string{}
		for _, f := range e.Fields {
			if f.Default != "" {
				values[f.Name] = f.Default
			}
			// Allow prefilling single-value fields from the query string, e.g.
			// /connections/new?device_id=1&switch_id=2&mode=Trunk from the topology editor.
			if v := r.URL.Query().Get(f.Name); v != "" && f.Kind != kMulti && f.Kind != kChecks {
				values[f.Name] = v
			}
		}
		s.renderForm(w, e, 0, values, nil, "")
	})

	r.Get(base+"/{id}/edit", func(w http.ResponseWriter, r *http.Request) {
		id, ok := idParam(r)
		if !ok {
			http.NotFound(w, r)
			return
		}
		snap, err := e.snapshot(s.db, id)
		if errors.Is(err, errNotFound) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			s.fail(w, "edit "+e.Key, err)
			return
		}
		values := map[string]string{}
		multi := map[string][]string{}
		for _, f := range e.Fields {
			if f.Kind == kMulti {
				for _, v := range snap[f.Name].([]any) {
					multi[f.Name] = append(multi[f.Name], str(v))
				}
			} else {
				values[f.Name] = str(snap[f.Name])
			}
		}
		s.renderForm(w, e, id, values, multi, "")
	})

	save := func(w http.ResponseWriter, r *http.Request) {
		var id int64
		if chi.URLParam(r, "id") != "" {
			var ok bool
			if id, ok = idParam(r); !ok {
				http.NotFound(w, r)
				return
			}
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		values := map[string]string{}
		multi := map[string][]string{}
		for _, f := range e.Fields {
			switch f.Kind {
			case kMulti:
				multi[f.Name] = r.PostForm[f.Name]
			case kChecks:
				values[f.Name] = strings.Join(r.PostForm[f.Name], ",")
			default:
				values[f.Name] = r.PostFormValue(f.Name)
			}
		}
		args, err := e.parse(r)
		if err == nil && e.Validate != nil {
			err = e.Validate(r)
		}
		if err == nil {
			_, err = s.write(e, id, args, multi)
		}
		if errors.Is(err, errNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			// 200, not 4xx: htmx does not swap error responses by default.
			s.renderForm(w, e, id, values, multi, friendlyErr(e, err))
			return
		}
		http.Redirect(w, r, base, http.StatusSeeOther)
	}
	r.Post(base, save)
	r.Post(base+"/{id}", save)

	r.Post(base+"/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		id, ok := idParam(r)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if err := s.remove(e, id); errors.Is(err, errNotFound) {
			http.NotFound(w, r)
		} else if err != nil {
			s.fail(w, "delete "+e.Key, err)
		}
		// Empty 200: htmx swaps the table row out.
	})

	r.Get(base+"/{id}/history", func(w http.ResponseWriter, r *http.Request) {
		id, ok := idParam(r)
		if !ok {
			http.NotFound(w, r)
			return
		}
		items, err := s.history(e, id)
		if err != nil {
			s.fail(w, "history "+e.Key, err)
			return
		}
		s.render(w, "history.html", historyPage{
			base: s.newBase("History"), Entity: e, ID: id, Items: items,
		})
	})
}

func (s *Server) remove(e *Entity, id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	snap, err := e.snapshot(tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM "+e.Table+" WHERE id = ?", id); err != nil {
		return err
	}
	if err := addRevision(tx, e, id, snap, "deleted"); err != nil {
		return err
	}
	return tx.Commit()
}

// history renders revisions newest first, each as a diff against the
// previous snapshot.
func (s *Server) history(e *Entity, id int64) ([]histItem, error) {
	rows, err := queryAll(s.db,
		"SELECT changed_at, note, snapshot_json FROM revisions WHERE entity_type = ? AND entity_id = ? ORDER BY id",
		e.Key, id)
	if err != nil {
		return nil, err
	}
	var items []histItem
	prev := map[string]any{}
	for _, row := range rows {
		var cur map[string]any
		if err := json.Unmarshal([]byte(str(row[2])), &cur); err != nil {
			return nil, err
		}
		item := histItem{At: str(row[0]), Note: str(row[1])}
		keys := make([]string, 0, len(cur))
		for k := range cur {
			if k != "id" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			o, n := fmt.Sprint(prev[k]), fmt.Sprint(cur[k])
			if _, had := prev[k]; !had {
				item.Changes = append(item.Changes, fmt.Sprintf("%s: %s", k, n))
			} else if o != n {
				item.Changes = append(item.Changes, fmt.Sprintf("%s: %s → %s", k, o, n))
			}
		}
		prev = cur
		items = append([]histItem{item}, items...)
	}
	return items, nil
}
