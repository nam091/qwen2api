package topicisolation

import "testing"

func TestExtractEntities(t *testing.T) {
	text := `Please read /home/user/main.go and check https://example.com/api for the camelCase pattern.`
	ents := ExtractEntities(text)
	if _, ok := ents["/home/user/main.go"]; !ok {
		t.Error("expected unix path")
	}
	if _, ok := ents["https://example.com/api"]; !ok {
		t.Error("expected URL")
	}
	if _, ok := ents["camelcase"]; !ok {
		t.Error("expected camelCase identifier")
	}
	if _, ok := ents["main.go"]; !ok {
		t.Error("expected dotted filename")
	}
}

func TestJaccard(t *testing.T) {
	a := map[string]struct{}{"a": {}, "b": {}, "c": {}}
	b := map[string]struct{}{"b": {}, "c": {}, "d": {}}
	j := Jaccard(a, b)
	if j < 0.49 || j > 0.51 {
		t.Errorf("expected ~0.5, got %f", j)
	}
}

func TestDetectorChanged(t *testing.T) {
	d := NewDetector(0.1)
	first := "Read /home/user/project/main.go and fix the bug"
	same := "Now edit /home/user/project/main.go line 42"
	different := "Open https://google.com and register an account with email"

	if d.Changed(first, same) {
		t.Error("same topic should not be detected as changed")
	}
	if !d.Changed(first, different) {
		t.Error("different topic should be detected as changed")
	}
}

func TestDetectorEmptyEntities(t *testing.T) {
	d := NewDetector(0.1)
	if d.Changed("hello world", "goodbye world") {
		t.Error("no entities => should not trigger change")
	}
}
