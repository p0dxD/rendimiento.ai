package store

import (
	"context"
	"errors"
	"testing"
)

// ALLOWED_USERS seeds the first administrators once; members are then
// managed, and an organization never loses its last administrator.
func TestMembers(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if n, err := s.SeedAdmins(ctx, []string{"p0dxD", " "}); err != nil || n != 1 {
		t.Fatalf("seed: %d %v", n, err)
	}
	if n, _ := s.SeedAdmins(ctx, []string{"otra"}); n != 0 {
		t.Fatalf("seeding again added %d", n)
	}
	if role, err := s.MemberRole(ctx, DefaultOrg, "P0DXD"); err != nil || role != MemberAdmin {
		t.Fatalf("role = %q %v", role, err)
	}
	if err := s.SetMember(ctx, DefaultOrg, "ana", MemberGuest, "p0dxD"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.IsMemberAnywhere(ctx, "Ana"); !ok {
		t.Fatal("ana may sign in")
	}
	if err := s.SetMember(ctx, DefaultOrg, "p0dxD", MemberGuest, "p0dxD"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demoting the last admin: %v", err)
	}
	if err := s.RemoveMember(ctx, DefaultOrg, "p0dxD"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("removing the last admin: %v", err)
	}
	if err := s.SetMember(ctx, DefaultOrg, "ana", MemberAdmin, "p0dxD"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMember(ctx, DefaultOrg, "p0dxD", MemberCollaborator, "ana"); err != nil {
		t.Fatalf("with two admins one may step down: %v", err)
	}
	ms, err := s.Members(ctx, DefaultOrg)
	if err != nil || len(ms) != 2 || ms[0].Login != "ana" || ms[1].Role != MemberCollaborator {
		t.Fatalf("members = %+v %v", ms, err)
	}
	if err := s.RemoveMember(ctx, DefaultOrg, "nadie"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removing nobody: %v", err)
	}
	if ok, _ := s.IsMemberAnywhere(ctx, "nadie"); ok {
		t.Fatal("a stranger may not sign in")
	}
}
