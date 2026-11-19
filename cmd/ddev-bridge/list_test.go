package main

import (
	"strings"
	"testing"

	"ddev-bridge/internal/registry"
)

func TestWriteProjectsSorted(t *testing.T) {
	var b strings.Builder
	writeProjects(&b, []registry.Project{
		{Name: "zeta", Root: "/z"}, {Name: "Alpha", Root: "/b"}, {Name: "beta", Root: "/x"}, {Name: "alpha", Root: "/a"},
	})
	want := "alpha\t/a\nAlpha\t/b\nbeta\t/x\nzeta\t/z\n"
	if b.String() != want {
		t.Fatalf("got\n%s", b.String())
	}
}
