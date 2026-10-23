package plugin

import "testing"

func TestCheckKomariVersion(t *testing.T) {
	tests := []struct {
		constraint string
		wantErr    bool
	}{
		{"", false},
		{"1.6.0", false},
		{"v1.6.0", false},
		{">=0.0.1", false},
		{">1.6.0", true},
		{"<2.0.0", false},
		{"<=1.6.0", false},
		{">=99.0.0", true},
		{"0.1", true}, // Exact constraints normalize to 0.1.0 and 1.0.0.
		{"1", true},
		{"1.2.3.4", true},
		{"abc", true},
		{">", true},
	}
	for _, tt := range tests {
		err := CheckKomariVersion(tt.constraint)
		if (err != nil) != tt.wantErr {
			t.Errorf("CheckKomariVersion(%q) error = %v, wantErr %v", tt.constraint, err, tt.wantErr)
		}
	}
}
