package main

import "testing"

func TestHelpAndVersion(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {"--version"}, {"version"}} {
		if err := run(args); err != nil {
			t.Errorf("run(%q) = %v", args, err)
		}
	}
}
