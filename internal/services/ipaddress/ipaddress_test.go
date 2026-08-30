package ipaddress

import "testing"

func sampleAddresses() []Address {
	return []Address{
		{Name: "web-ip", Address: "34.1.2.3", Region: "us-central1", AddressType: "EXTERNAL", Status: "IN_USE"},
		{Name: "lb-ip", Address: "35.1.2.4", Region: "", AddressType: "EXTERNAL", Status: "RESERVED"},
		{Name: "internal-db", Address: "10.0.0.5", Region: "us-east1", AddressType: "INTERNAL", Status: "RESERVED"},
	}
}

func namesOf(addrs []Address) []string {
	if len(addrs) == 0 {
		return nil
	}
	names := make([]string, len(addrs))
	for i, a := range addrs {
		names[i] = a.Name
	}
	return names
}

func TestAddress_ScopeAndIsGlobal(t *testing.T) {
	regional := Address{Region: "us-central1"}
	if regional.IsGlobal() {
		t.Fatalf("regional address should not be global")
	}
	if got := regional.Scope(); got != "us-central1" {
		t.Fatalf("Scope() = %q, want %q", got, "us-central1")
	}

	global := Address{Region: ""}
	if !global.IsGlobal() {
		t.Fatalf("empty-region address should be global")
	}
	if got := global.Scope(); got != "global" {
		t.Fatalf("Scope() = %q, want %q", got, "global")
	}
}

func TestGetFilteredAddresses(t *testing.T) {
	s := NewService(nil)
	addrs := sampleAddresses()

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"empty query returns all", "", []string{"web-ip", "lb-ip", "internal-db"}},
		{"matches by name substring", "ip", []string{"web-ip", "lb-ip"}},
		{"matches by address", "10.0.0.5", []string{"internal-db"}},
		{"matches by scope (region)", "us-east1", []string{"internal-db"}},
		{"matches by scope (global)", "global", []string{"lb-ip"}},
		{"matches by type", "internal", []string{"internal-db"}},
		{"matches by status", "reserved", []string{"lb-ip", "internal-db"}},
		{"case-insensitive", "WEB-IP", []string{"web-ip"}},
		{"no match", "does-not-exist", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.getFilteredAddresses(addrs, tt.query)
			if len(got) != len(tt.want) {
				t.Fatalf("getFilteredAddresses(%q) = %v, want %v", tt.query, namesOf(got), tt.want)
			}
			for i, a := range got {
				if a.Name != tt.want[i] {
					t.Fatalf("getFilteredAddresses(%q) = %v, want %v", tt.query, namesOf(got), tt.want)
				}
			}
		})
	}
}
