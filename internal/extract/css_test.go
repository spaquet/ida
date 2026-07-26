package extract

import "testing"

func TestCSSApplyExtraction(t *testing.T) {
	content := []byte(`.btn {
  @apply px-4 py-2 bg-primary text-white;
}
`)
	nodes := css("app/assets/stylesheets/buttons.css", content)

	classes := nodeByKind(nodes, "css_class")
	if len(classes) != 1 || classes[0].Name != ".btn" {
		t.Fatalf("css_class = %#v; want .btn", classes)
	}
	uses := nodeByKind(nodes, "class_attr_use")
	if len(uses) != 1 {
		t.Fatalf("class_attr_use = %#v; want one aggregated node", uses)
	}
}

func TestTailwindConfigTokenExtraction(t *testing.T) {
	content := []byte(`module.exports = {
  theme: {
    extend: {
      colors: {
        primary: "#1d4ed8",
        secondary: "#9333ea",
      },
      spacing: {
        18: "4.5rem",
      },
    },
  },
}
`)
	nodes := tailwindConfig("tailwind.config.js", content)
	tokens := nodeByKind(nodes, "tailwind_token")
	want := map[string]string{
		"primary":   "colors.primary",
		"secondary": "colors.secondary",
		"18":        "spacing.18",
	}
	if len(tokens) != len(want) {
		t.Fatalf("tailwind_token = %#v; want %d tokens", tokens, len(want))
	}
	for _, tok := range tokens {
		if want[tok.Name] != tok.QualifiedName {
			t.Errorf("token %q qualified = %q; want %q", tok.Name, tok.QualifiedName, want[tok.Name])
		}
	}
}
