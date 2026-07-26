package extract

import "testing"

func TestRubyDeclarations(t *testing.T) {
	content := []byte("module Admin\n  class User\n    def active?\n    end\n  end\nend\n")
	nodes := File("app/models/admin/user.rb", content)
	got := make(map[string]bool)
	for _, node := range nodes {
		got[node.QualifiedName] = true
	}
	for _, want := range []string{"Admin", "User", "active?"} {
		if !got[want] {
			t.Errorf("missing %q in %#v", want, nodes)
		}
	}
}
