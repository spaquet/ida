package resolve_test

import (
	"database/sql"
	"testing"

	"github.com/spaquet/ida/internal/resolve"
	"github.com/spaquet/ida/internal/store"
)

func open(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertFile(t *testing.T, tx *sql.Tx, path string) int64 {
	t.Helper()
	id, err := store.InsertFile(tx, path, "code", "sum", 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertNode(t *testing.T, tx *sql.Tx, id string, fileID int64, kind, name, qualified string, line int) {
	t.Helper()
	_, err := tx.Exec(`
INSERT INTO nodes(id, file_id, kind, name, qualified_name, start_line, end_line, extractor, confidence, generation)
VALUES (?, ?, ?, ?, ?, ?, ?, 'test', 'exact', 1)`, id, fileID, kind, name, qualified, line, line)
	if err != nil {
		t.Fatal(err)
	}
}

func edgeKinds(t *testing.T, db *store.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT kind FROM edges`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var kinds []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, k)
	}
	return kinds
}

func edgeTarget(t *testing.T, db *store.DB, kind string) string {
	t.Helper()
	var target string
	err := db.QueryRow(`SELECT target_id FROM edges WHERE kind = ?`, kind).Scan(&target)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestResolveStimulusAmbiguousControllerOmitted(t *testing.T) {
	db := open(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	f1 := insertFile(t, tx, "app/javascript/controllers/hello_controller.js")
	f2 := insertFile(t, tx, "app/javascript/controllers/other/hello_controller.js")
	f3 := insertFile(t, tx, "app/views/things/index.html.erb")
	insertNode(t, tx, "c1", f1, "stimulus_controller", "hello", "hello", 1)
	insertNode(t, tx, "c2", f2, "stimulus_controller", "hello", "hello", 1)
	insertNode(t, tx, "use1", f3, "stimulus_controller_use", "hello", "hello", 1)
	if err := resolve.All(tx, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range edgeKinds(t, db) {
		if kind == "stimulus_controller" {
			t.Fatalf("expected no stimulus_controller edge for ambiguous identifier, got one")
		}
	}
}

func TestResolveStimulusControllerAndAction(t *testing.T) {
	db := open(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	f1 := insertFile(t, tx, "app/javascript/controllers/hello_controller.js")
	f2 := insertFile(t, tx, "app/views/things/index.html.erb")
	insertNode(t, tx, "c1", f1, "stimulus_controller", "hello", "hello", 1)
	insertNode(t, tx, "m1", f1, "method", "greet", "hello#greet", 5)
	insertNode(t, tx, "use1", f2, "stimulus_controller_use", "hello", "hello", 1)
	insertNode(t, tx, "use2", f2, "stimulus_action_use", "hello#greet", "hello#greet", 2)
	if err := resolve.All(tx, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := edgeTarget(t, db, "stimulus_controller"); got != "c1" {
		t.Fatalf("stimulus_controller target = %q; want c1", got)
	}
	if got := edgeTarget(t, db, "stimulus_action"); got != "m1" {
		t.Fatalf("stimulus_action target = %q; want m1", got)
	}
}

func TestResolveImportsRelativeAndBare(t *testing.T) {
	db := open(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	from := insertFile(t, tx, "app/javascript/components/App.jsx")
	target := insertFile(t, tx, "app/javascript/components/Greeting.jsx")
	insertNode(t, tx, "imp1", from, "js_import", "./Greeting", "./Greeting", 1)
	insertNode(t, tx, "imp2", from, "js_import", "react", "react", 2)
	insertNode(t, tx, "targetFile", target, "file", "app/javascript/components/Greeting.jsx", "app/javascript/components/Greeting.jsx", 1)
	insertNode(t, tx, "fromFile", from, "file", "app/javascript/components/App.jsx", "app/javascript/components/App.jsx", 1)
	if err := resolve.All(tx, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	kinds := edgeKinds(t, db)
	count := 0
	for _, k := range kinds {
		if k == "imports" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 imports edge (bare specifier left unresolved), got %d: %v", count, kinds)
	}
	if got := edgeTarget(t, db, "imports"); got != "targetFile" {
		t.Fatalf("imports target = %q; want targetFile", got)
	}
}

func TestResolveJSXSameFileAndCrossFile(t *testing.T) {
	db := open(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	f1 := insertFile(t, tx, "app/javascript/components/App.jsx")
	f2 := insertFile(t, tx, "app/javascript/components/Card.jsx")
	insertNode(t, tx, "localComp", f1, "js_component", "Local", "Local", 1)
	insertNode(t, tx, "useLocal", f1, "jsx_use", "Local", "Local", 2)

	insertNode(t, tx, "cardComp", f2, "js_component", "Card", "Card", 1)
	insertNode(t, tx, "importCard", f1, "js_import", "./Card", "./Card", 3)
	insertNode(t, tx, "cardFile", f2, "file", "app/javascript/components/Card.jsx", "app/javascript/components/Card.jsx", 1)
	insertNode(t, tx, "useCard", f1, "jsx_use", "Card", "Card", 4)

	if err := resolve.All(tx, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT source_id, target_id FROM edges WHERE kind = 'jsx_renders' ORDER BY source_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string]string{}
	for rows.Next() {
		var s, tg string
		if err := rows.Scan(&s, &tg); err != nil {
			t.Fatal(err)
		}
		got[s] = tg
	}
	if got["useLocal"] != "localComp" {
		t.Errorf("useLocal -> %q; want localComp", got["useLocal"])
	}
	if got["useCard"] != "cardComp" {
		t.Errorf("useCard -> %q; want cardComp", got["useCard"])
	}
}

func TestResolveReactMountsAmbiguousOmitted(t *testing.T) {
	db := open(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	f1 := insertFile(t, tx, "app/javascript/components/Greeting.jsx")
	f2 := insertFile(t, tx, "app/javascript/components/other/Greeting.jsx")
	f3 := insertFile(t, tx, "app/views/things/index.html.erb")
	insertNode(t, tx, "g1", f1, "js_component", "Greeting", "Greeting", 1)
	insertNode(t, tx, "g2", f2, "js_component", "Greeting", "Greeting", 1)
	insertNode(t, tx, "mount1", f3, "react_mount", "Greeting", "Greeting", 1)
	if err := resolve.All(tx, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range edgeKinds(t, db) {
		if kind == "mounts" {
			t.Fatalf("expected no mounts edge for ambiguous component name, got one")
		}
	}
}

func TestResolveTailwindFansOutToMultipleFiles(t *testing.T) {
	db := open(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	configFile := insertFile(t, tx, "tailwind.config.js")
	viewFile := insertFile(t, tx, "app/views/things/index.html.erb")
	componentFile := insertFile(t, tx, "app/javascript/components/Card.jsx")
	insertNode(t, tx, "token1", configFile, "tailwind_token", "primary", "colors.primary", 4)
	insertNode(t, tx, "viewFileNode", viewFile, "file", "app/views/things/index.html.erb", "app/views/things/index.html.erb", 1)
	insertNode(t, tx, "compFileNode", componentFile, "file", "app/javascript/components/Card.jsx", "app/javascript/components/Card.jsx", 1)
	insertNode(t, tx, "use1", viewFile, "class_attr_use", "text-primary flex", "text-primary flex", 1)
	insertNode(t, tx, "use2", componentFile, "class_attr_use", "bg-primary", "bg-primary", 1)
	if err := resolve.All(tx, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT target_id FROM edges WHERE kind = 'tailwind_uses' ORDER BY target_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var targets []string
	for rows.Next() {
		var tg string
		if err := rows.Scan(&tg); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, tg)
	}
	if len(targets) != 2 || targets[0] != "compFileNode" || targets[1] != "viewFileNode" {
		t.Fatalf("tailwind_uses targets = %v; want [compFileNode viewFileNode]", targets)
	}
}
