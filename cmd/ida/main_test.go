package main

import "testing"

func TestHelpAndVersion(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {"--version"}, {"version"}} {
		if err := run(args); err != nil {
			t.Errorf("run(%q) = %v", args, err)
		}
	}
}

func TestInitArgs(t *testing.T) {
	path, install, err := initArgs([]string{"--install-lsp", "."})
	if err != nil || path != "." || !install {
		t.Fatalf("initArgs() = %q, %v, %v", path, install, err)
	}
	if _, _, err := initArgs([]string{".", "other"}); err == nil {
		t.Fatal("initArgs accepted two paths")
	}
}
