package query_test

import (
	"database/sql"
	"testing"

	"github.com/spaquet/ida/internal/query"
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

func insertEdge(t *testing.T, tx *sql.Tx, id, source, target, kind string, fileID int64) {
	t.Helper()
	_, err := tx.Exec(`
INSERT INTO edges(id, source_id, target_id, kind, confidence, file_id, start_line, evidence, generation)
VALUES (?, ?, ?, ?, 'convention', ?, 1, '', 1)`, id, source, target, kind, fileID)
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnusedPartials(t *testing.T) {
	db := open(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	formFile := insertFile(t, tx, "app/views/articles/_form.html.erb")
	sidebarFile := insertFile(t, tx, "app/views/articles/_sidebar.html.erb")
	viewFile := insertFile(t, tx, "app/views/articles/index.html.erb")
	insertNode(t, tx, "form", formFile, "partial", "form", "articles/form", 1)
	insertNode(t, tx, "sidebar", sidebarFile, "partial", "sidebar", "articles/sidebar", 1)
	insertNode(t, tx, "use1", viewFile, "partial_use", "form", "form", 1)
	insertEdge(t, tx, "e1", "use1", "form", "renders_partial", viewFile)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	results, err := query.Unused(db, "partial")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "sidebar" {
		t.Fatalf("Unused(partial) = %#v; want only sidebar", results)
	}
}

func TestUnusedUnsupportedKind(t *testing.T) {
	db := open(t)
	if _, err := query.Unused(db, "bogus"); err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}

func TestDuplicatesMethodFlagsRealRiskNotExpectedConfig(t *testing.T) {
	db := open(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	svcFile1 := insertFile(t, tx, "app/services/notify_service.rb")
	svcFile2 := insertFile(t, tx, "app/services/notify_service_v2.rb")
	insertNode(t, tx, "m1", svcFile1, "method", "call", "NotifyService#call", 2)
	insertNode(t, tx, "m2", svcFile2, "method", "call", "NotifyService#call", 3)

	devFile := insertFile(t, tx, "config/environments/development.rb")
	prodFile := insertFile(t, tx, "config/environments/production.rb")
	insertNode(t, tx, "e1", devFile, "method", "configure", "Rails::Application#configure", 1)
	insertNode(t, tx, "e2", prodFile, "method", "configure", "Rails::Application#configure", 1)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	groups, err := query.Duplicates(db, "method")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("Duplicates(method) = %#v; want 2 groups", groups)
	}
	byName := make(map[string]query.DuplicateGroup)
	for _, g := range groups {
		byName[g.QualifiedName] = g
	}
	svc := byName["NotifyService#call"]
	if svc.Expected || len(svc.Locations) != 2 {
		t.Fatalf("NotifyService#call group = %#v; want Expected=false, 2 locations", svc)
	}
	env := byName["Rails::Application#configure"]
	if !env.Expected || len(env.Locations) != 2 {
		t.Fatalf("Rails::Application#configure group = %#v; want Expected=true, 2 locations", env)
	}
}

func TestDuplicatesStimulusController(t *testing.T) {
	db := open(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	f1 := insertFile(t, tx, "app/javascript/controllers/hello_controller.js")
	f2 := insertFile(t, tx, "app/javascript/controllers/other/hello_controller.js")
	insertNode(t, tx, "c1", f1, "stimulus_controller", "hello", "hello", 1)
	insertNode(t, tx, "c2", f2, "stimulus_controller", "hello", "hello", 1)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	groups, err := query.Duplicates(db, "stimulus_controller")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].QualifiedName != "hello" || len(groups[0].Locations) != 2 {
		t.Fatalf("Duplicates(stimulus_controller) = %#v; want one hello group with 2 locations", groups)
	}
}

func TestEnvVarsGroupedByNameWithCategory(t *testing.T) {
	db := open(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	dbYaml := insertFile(t, tx, "config/database.yml")
	initFile := insertFile(t, tx, "config/initializers/stripe.rb")
	appFile := insertFile(t, tx, "app/services/notify_service.rb")
	insertNode(t, tx, "u1", dbYaml, "env_var_use", "DB_HOST", "DB_HOST", 2)
	insertNode(t, tx, "u2", initFile, "env_var_use", "STRIPE_KEY", "STRIPE_KEY", 1)
	insertNode(t, tx, "u3", appFile, "env_var_use", "STRIPE_KEY", "STRIPE_KEY", 4)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	groups, err := query.EnvVars(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("EnvVars() = %#v; want 2 groups", groups)
	}
	byName := make(map[string]query.EnvVarGroup)
	for _, g := range groups {
		byName[g.Name] = g
	}
	if len(byName["DB_HOST"].Uses) != 1 || byName["DB_HOST"].Uses[0].Category != "database" {
		t.Fatalf("DB_HOST = %#v; want 1 use categorized database", byName["DB_HOST"])
	}
	stripe := byName["STRIPE_KEY"]
	if len(stripe.Uses) != 2 {
		t.Fatalf("STRIPE_KEY = %#v; want 2 uses", stripe)
	}
	cats := map[string]bool{stripe.Uses[0].Category: true, stripe.Uses[1].Category: true}
	if !cats["initializer"] || !cats["app"] {
		t.Fatalf("STRIPE_KEY categories = %#v; want initializer and app", cats)
	}
}
