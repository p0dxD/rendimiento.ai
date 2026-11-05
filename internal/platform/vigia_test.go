package platform

import "testing"

// Which heads the watcher handles as pushes: on the first look only an
// unbuilt default branch; then any branch that moved or appeared.
func TestVigiaNuevo(t *testing.T) {
	v := &Vigia{seen: map[string]string{}}
	never := func() bool { return false }
	already := func() bool { return true }
	if !v.nuevo(true, "o/r main", "a1", true, never) {
		t.Error("first look: an unbuilt default branch runs")
	}
	if v.nuevo(true, "o/r vieja", "b1", false, never) {
		t.Error("first look: another branch only gets noted")
	}
	if v.nuevo(true, "o/s main", "z1", true, already) {
		t.Error("first look: a default branch that already ran does not run again")
	}
	if v.nuevo(false, "o/r main", "a1", true, never) {
		t.Error("later: a head that did not move")
	}
	if !v.nuevo(false, "o/r vieja", "b2", false, never) {
		t.Error("later: a branch that moved")
	}
	if !v.nuevo(false, "o/r nueva", "c1", false, never) {
		t.Error("later: a new branch")
	}
}
