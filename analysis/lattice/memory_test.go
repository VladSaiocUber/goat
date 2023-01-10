package lattice

import (
	"testing"

	loc "github.com/cs-au-dk/goat/analysis/location"
)

func TestMemory(t *testing.T) {
	al := loc.AllocationSiteLocation{
		Goro:    &fakeGoro{"a"},
		Context: nil,
		Site:    &fakeValue{},
	}

	tl, ok := representative(al)
	if !ok {
		t.Fatal("???")
	}
	_ = tl

	t.Run("Multialloc", func(t *testing.T) {
		check := func(m Memory, l loc.AddressableLocation, expected bool) {
			if actual := m.IsMultialloc(l); actual != expected {
				t.Errorf("(%v).IsMultialloc(%v) = %v, expected %v", m, l, actual, expected)
			}
		}

		// Normal (non-top) location
		check(
			Elements().Memory().Allocate(al, Consts().BasicTopValue(), false),
			al, false)

		check(
			Elements().Memory().
				Allocate(al, Consts().BasicTopValue(), false).
				Allocate(al, Consts().BasicTopValue(), false),
			al, true)

		check(
			Elements().Memory().Allocate(al, Consts().BasicTopValue(), true),
			al, true)

		// Top location should be multiallocated no matter what
		check(
			Elements().Memory().Allocate(tl, Consts().BasicTopValue(), false),
			tl, true)

		check(
			Elements().Memory().
				Allocate(tl, Consts().BasicTopValue(), false).
				Allocate(tl, Consts().BasicTopValue(), false),
			tl, true)

		check(
			Elements().Memory().Allocate(tl, Consts().BasicTopValue(), true),
			tl, true)
	})

}
