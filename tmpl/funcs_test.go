package tmpl

import "testing"

func TestStripExt(t *testing.T) {
	var files = map[string]string{
		"hello.txt":      "hello",
		"no_ext":         "no_ext",
		"long.extension": "long",
	}
	for f, correct := range files {
		got := trimExt(f)
		if got != correct {
			t.Fatalf("got %q expected %q", got, correct)
		}
	}
}
