package doctor

import (
	"fmt"
	"strings"

	"github.com/spaquet/ida/internal/lsp"
	"github.com/spaquet/ida/internal/project"
	"github.com/spaquet/ida/internal/store"
)

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type Report struct {
	Healthy bool         `json:"healthy"`
	Checks  []Check      `json:"checks"`
	LSP     []lsp.Server `json:"lsp"`
}

func Run(root string) Report {
	report := Report{
		Healthy: true,
		Checks:  []Check{{Name: "rails", Status: "ok", Detail: root}},
	}
	db, err := store.OpenExisting(root)
	if err != nil {
		report.Healthy = false
		report.Checks = append(report.Checks, Check{Name: "database", Status: "error", Detail: err.Error()})
	} else {
		defer func() { _ = db.Close() }()
		status, statusErr := db.Status()
		if statusErr != nil {
			report.Healthy = false
			report.Checks = append(report.Checks, Check{Name: "database", Status: "error", Detail: statusErr.Error()})
		} else {
			indexStatus := "ok"
			if status.State != "complete" {
				report.Healthy = false
				indexStatus = "error"
			}
			report.Checks = append(report.Checks,
				Check{Name: "database", Status: "ok", Detail: fmt.Sprintf("%d files, generation %d", status.Files, status.Generation)},
				Check{Name: "index", Status: indexStatus, Detail: status.State},
			)
			watcherStatus := "ok"
			if status.WatcherState == "degraded" {
				report.Healthy = false
				watcherStatus = "error"
			}
			detail := fmt.Sprintf("%s, %d pending", status.WatcherState, len(status.PendingFiles))
			if status.WatcherError != "" {
				detail += ": " + status.WatcherError
			}
			report.Checks = append(report.Checks, Check{Name: "watcher", Status: watcherStatus, Detail: detail})
		}
	}
	if scope, scopeErr := project.LoadScope(root); scopeErr != nil {
		report.Healthy = false
		report.Checks = append(report.Checks, Check{Name: "engines", Status: "error", Detail: scopeErr.Error()})
	} else if engines := scope.Engines(); len(engines) > 0 {
		report.Checks = append(report.Checks, Check{Name: "engines", Status: "ok", Detail: strings.Join(engines, ", ")})
	} else {
		report.Checks = append(report.Checks, Check{Name: "engines", Status: "ok", Detail: "none found"})
	}
	report.LSP, err = lsp.Detect(root)
	if err != nil {
		report.Healthy = false
		report.Checks = append(report.Checks, Check{Name: "lsp", Status: "error", Detail: err.Error()})
	}
	return report
}
