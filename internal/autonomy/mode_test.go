package autonomy

import "testing"

func TestModesHaveExplicitContractSemantics(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  Mode
	}{
		{"", Review}, {"review", Review}, {"REVIEW", Review}, {"quick", Quick},
	} {
		got, err := Parse(tc.input)
		if err != nil || got != tc.want {
			t.Fatalf("Parse(%q) = %q, %v; want %q", tc.input, got, err, tc.want)
		}
	}
	if Quick.UsesContract() || !Review.UsesContract() {
		t.Fatal("mode contract semantics changed")
	}
	if _, err := Parse("loop"); err == nil {
		t.Fatal("unimplemented loop mode was accepted")
	}
}
