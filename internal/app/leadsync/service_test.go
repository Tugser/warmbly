package leadsync

import "testing"

func TestBuildA1RangeReadsEveryColumn(t *testing.T) {
	tests := []struct {
		name, tab string
		maxRows   int
		want      string
	}{
		{"whole first tab", "", 0, "A:ZZZ"},
		{"whole named tab", "Leads", 0, "'Leads'!A:ZZZ"},
		{"preview of a named tab", " Leads ", 21, "'Leads'!A1:ZZZ21"},
		{"a quote in the tab title is doubled", "Q4 'hot'", 0, "'Q4 ''hot'''!A:ZZZ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildA1Range(tt.tab, tt.maxRows); got != tt.want {
				t.Errorf("buildA1Range(%q, %d) = %q, want %q", tt.tab, tt.maxRows, got, tt.want)
			}
		})
	}
}
