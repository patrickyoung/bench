package filterexec

import "testing"

func TestOverlayEnvReplacesRatherThanDuplicates(t *testing.T) {
	got := overlayEnv([]string{"A=old", "B=keep", "A=stale"}, []string{"A=new", "INVALID", "C=added"})
	want := []string{"B=keep", "A=new", "C=added"}
	if len(got) != len(want) {
		t.Fatalf("env = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("env = %#v, want %#v", got, want)
		}
	}
}
