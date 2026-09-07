package components

import "testing"

func TestNewSelect_StartsOnCurrent(t *testing.T) {
	m := NewSelect("Pick", []string{"a", "b", "c"}, nil, "b")
	if m.Selection != 1 {
		t.Errorf("Selection = %d, want 1", m.Selection)
	}
	if got := m.Value(); got != "b" {
		t.Errorf("Value() = %q, want %q", got, "b")
	}
}

func TestNewSelect_UnknownCurrentDefaultsToFirst(t *testing.T) {
	m := NewSelect("Pick", []string{"a", "b", "c"}, nil, "z")
	if m.Selection != 0 {
		t.Errorf("Selection = %d, want 0", m.Selection)
	}
}

func TestSelectModel_NextPrevWrap(t *testing.T) {
	m := NewSelect("Pick", []string{"a", "b", "c"}, nil, "c")
	m.SelectNext()
	if got := m.Value(); got != "a" {
		t.Errorf("after SelectNext at last, Value() = %q, want %q (wrap)", got, "a")
	}
	m.SelectPrev()
	if got := m.Value(); got != "c" {
		t.Errorf("after SelectPrev at first, Value() = %q, want %q (wrap)", got, "c")
	}
}

func TestSelectModel_Label(t *testing.T) {
	m := NewSelect("Pick", []string{"DEFAULT", "ERROR"}, []string{"Any (no filter)", ""}, "DEFAULT")
	if got := m.label(0); got != "Any (no filter)" {
		t.Errorf("label(0) = %q, want %q", got, "Any (no filter)")
	}
	if got := m.label(1); got != "ERROR" {
		t.Errorf("label(1) = %q, want %q (falls back to Options)", got, "ERROR")
	}
}
