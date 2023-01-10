package gotopo

import (
	"go/types"
	"testing"

	"github.com/cs-au-dk/goat/testutil"
	"github.com/cs-au-dk/goat/utils"
	"github.com/cs-au-dk/goat/utils/slices"
	"github.com/stretchr/testify/require"

	"golang.org/x/tools/go/ssa"
)

// psetPredicate is a predicate over P-sets (and their index in the P-set collection).
// Any P-set that is meant to satisfy the intended semantics of the predicate must return true.
type psetPredicate func(int, utils.SSAValueSet) bool

// getPsets performs all the necessary steps to retrieve P-sets,
// basedo n the GCatch heuristic.
func getPsets(loadRes testutil.LoadResult, entry string) PSets {
	mainPkg := loadRes.Mains[0]

	entryFun := mainPkg.Func(entry)

	G := loadRes.PrunedCallDAG.Original

	_, primsToUses := GetPrimitives(entryFun, loadRes.Pointer, G, false)

	computeDominator := G.DominatorTree(loadRes.Pointer.CallGraph.Root.Func)

	return GetGCatchPSets(
		loadRes.Cfg,
		entryFun,
		loadRes.Pointer,
		G,
		computeDominator,
		loadRes.PrunedCallDAG,
		primsToUses,
	)
}

// verifyPsets takes a test object, a P-set collection and a set of predicates.
// It checks for strictly one-to-one correspondence between the P-set collection
// and the set of supplied predicates.
//
// It is the caller's responsibility to supply an exhaustive and sound list of P-set predicates.
func verifyPsets(t *testing.T, psets PSets, psetPredicates ...psetPredicate) {
	require.Equal(t, len(psetPredicates), len(psets),
		"The number of Psets is not the same as the number of predicates.\n"+
			"Found %d P-sets and %d predicates", len(psets), len(psetPredicates))

	psetVerified := make([]int, len(psets))
	psetAppliedPredicates := make([]int, len(psetPredicates))

	for i, pset := range psets {
		for _, pred := range psetPredicates {
			if pred(i, pset) {
				psetAppliedPredicates[i]++
				psetVerified[i]++
			}
		}
	}
	// Every PSet must have been verified only once
	for i, matches := range psetVerified {
		require.Equal(t, 1, matches, "PSet %s was matched by %d verification predicates.", psets[i], matches)
	}
	// Every PSet must have been verified only once
	for i, matches := range psetAppliedPredicates {
		require.Equal(t, 1, matches, "Verification predicate %s matched %d psets.", i, matches)
	}
}

func TestGCatchPSets(t *testing.T) {
	t.Run("SingleMutexPSet", func(t *testing.T) {
		loadRes := testutil.LoadPackageFromSource(t, "test", primTestProg)
		psets := getPsets(loadRes, "main")

		mainFun := loadRes.Mains[0].Func("main")

		insn, ok := utils.FindSSAInstruction(mainFun, func(i ssa.Instruction) bool {
			alloc, ok := i.(*ssa.Alloc)
			return ok && alloc.Heap &&
				alloc.Type().(*types.Pointer).Elem().(*types.Named).Obj().Name() == "ProtectedInt"
		})
		if !ok {
			t.Fatal("Unable to find ProtectedInt alloc in main")
		}

		mkStruct := insn.(*ssa.Alloc)

		if psets.Get(mkStruct).Empty() {
			t.Errorf("No pset contains %v: %v", mkStruct, psets)
		}
	})

	t.Run("SelectChanDep", func(t *testing.T) {
		loadRes := testutil.LoadPackageFromSource(t, "test", `
			package main
			func main() {
				ch1, ch2 := make(chan int), make(chan int)
				select {
				case <-ch1:
				case <-ch2:
				default:
				}
			}`)

		psets := getPsets(loadRes, "main")
		if len(psets) != 1 {
			t.Errorf("Expected exactly one pset, got: %v", psets)
		} else if pset := psets[0]; pset.Size() != 2 {
			t.Errorf("Expected pset to contain both channels, was: %v", pset)
		}
	})

	t.Run("ChanChanDep", func(t *testing.T) {
		loadRes := testutil.LoadPackageFromSource(t, "test", `
			package main
			func ubool() bool
			func main() {
				ch1, ch2 := make(chan int), make(chan int)
				if ubool() {
					ch1 <- 0
					<-ch2
				} else {
					ch2 <- 0
					<-ch1
				}
			}`)

		psets := getPsets(loadRes, "main")
		if len(psets) != 1 {
			t.Errorf("Expected exactly one pset, got: %v", psets)
		} else if pset := psets[0]; pset.Size() != 2 {
			t.Errorf("Expected pset to contain both channels, was: %v", pset)
		}
	})

	t.Run("MutMutDep", func(t *testing.T) {
		loadRes := testutil.LoadPackageFromSource(t, "test", `
			package main
			import "sync"
			func ubool() bool
			func main() {
				var mu1, mu2 sync.Mutex
				if ubool() {
					mu1.Lock()
					mu2.Lock()
				} else {
					mu2.Lock()
					mu1.Lock()
				}
			}`)

		psets := getPsets(loadRes, "main")
		if len(psets) != 1 {
			t.Errorf("Expected exactly one pset, got: %v", psets)
		} else if pset := psets[0]; pset.Size() != 2 {
			t.Errorf("Expected pset to contain both mutexes, was: %v", pset)
		}
	})

	t.Run("ChanMutDep", func(t *testing.T) {
		loadRes := testutil.LoadPackageFromSource(t, "test", `
			package main
			import "sync"
			func ubool() bool
			func main() {
				var mu sync.Mutex
				ch := make(chan int)
				if ubool() {
					ch <- 10
					mu.Lock()
				} else {
					mu.Lock()
					<-ch
				}
			}`)

		psets := getPsets(loadRes, "main")
		if len(psets) != 1 {
			t.Errorf("Expected exactly one pset, got: %v", psets)
		} else if pset := psets[0]; pset.Size() != 2 {
			t.Errorf("Expected pset to contain both primitives, was: %v", pset)
		}
	})

	t.Run("ScopedChanDep", func(t *testing.T) {
		loadRes := testutil.LoadPackageFromSource(t, "test", `
			package main
			func f(ch chan int) {
				newch := make(chan int)
				select {
				case <-ch:
				case <-newch:
				}
			}
			func main() {
				ch := make(chan int)
				f(ch)
			}`)

		psets := getPsets(loadRes, "main")
		if len(psets) != 2 {
			t.Errorf("Expected two psets, got: %v", psets)
		} else {
			if _, found := slices.Find(psets, func(set utils.SSAValueSet) bool {
				return set.Size() == 2
			}); !found {
				t.Errorf("Expected to find a pset containing both primitives, got: %v", psets)
			}

			if _, found := slices.Find(psets, func(set utils.SSAValueSet) bool {
				return set.Size() == 1
			}); !found {
				t.Errorf("Expected to find a pset containing only newch due to smaller scope, got: %v", psets)
			}
		}
	})
}

// TestPayloadPSets tests the P-set inclusion mechanism for different configuration.
// The test naming convention is:
//
//	<Carrier kind>{-Property}*-carries-<Payload kind>{-Propeties}*
func TestPayloadPSets(t *testing.T) {
	t.Run("chan-carries-chan", func(t *testing.T) {
		loadRes := testutil.LoadPackageFromSource(t, "test", `
			package main

			func main() {
				ch1 := make(chan chan int, 1)
				ch2 := make(chan int)
				ch1 <- ch2
				<-<-ch1
			}`)

		psets := getPsets(loadRes, "main")

		verifyPsets(t, psets,
			// This predicate must be satisfied only by {ch1, ch2}
			func(i int, pset utils.SSAValueSet) bool {
				if pset.Size() != 2 {
					return false
				}

				foundLines := make(map[int]struct{})

				pset.ForEach(func(v ssa.Value) {
					line := v.Parent().Pkg.Prog.Fset.PositionFor(v.Pos(), false).Line
					foundLines[line] = struct{}{}
				})

				for _, expected := range []int{5, 6} {
					if _, ok := foundLines[expected]; !ok {
						return false
					}
				}

				return true
			},
			// This predicate must be satisfied only by {ch1}
			func(i int, pset utils.SSAValueSet) bool {
				var foundLine bool
				if pset.Size() == 1 {
					pset.ForEach(func(v ssa.Value) {
						line := v.Parent().Pkg.Prog.Fset.PositionFor(v.Pos(), false).Line
						foundLine = foundLine || line == 5
					})
				}

				return foundLine
			})
	})
	t.Run("chan-carries-struct", func(t *testing.T) {
		loadRes := testutil.LoadPackageFromSource(t, "test", `
			package main

			type Object struct {
				ch chan int
			}

			func main() {
				ch := make(chan Object, 1)
				ch <- Object{
					ch : make(chan int),
				}
				<-((<-ch).ch)
			}`)

		psets := getPsets(loadRes, "main")

		verifyPsets(t, psets,
			// This predicate must be satisfied only by {ch, obj.ch}
			func(i int, pset utils.SSAValueSet) bool {
				if pset.Size() != 2 {
					return false
				}
				foundLines := make(map[int]struct{})

				pset.ForEach(func(v ssa.Value) {
					line := v.Parent().Pkg.Prog.Fset.PositionFor(v.Pos(), false).Line
					foundLines[line] = struct{}{}
				})

				for _, expected := range []int{9, 11} {
					if _, ok := foundLines[expected]; !ok {
						return false
					}
				}

				return true
			},
			// This predicate must be satisfied only by {ch}
			func(i int, pset utils.SSAValueSet) bool {
				var foundLine bool
				if pset.Size() == 1 {
					pset.ForEach(func(v ssa.Value) {
						line := v.Parent().Pkg.Prog.Fset.PositionFor(v.Pos(), false).Line
						foundLine = foundLine || line == 9
					})
				}

				return foundLine
			})
	})

	t.Run("chan-carries-obj", func(t *testing.T) {
		loadRes := testutil.LoadPackageFromSource(t, "test", `
			package main

			type Object struct {
				ch chan int
			}

			func main() {
				ch := make(chan *Object, 1)
				ch <- &Object{
					ch : make(chan int),
				}
				<-((<-ch).ch)
			}`)

		psets := getPsets(loadRes, "main")

		verifyPsets(t, psets,
			// This predicate must be satisfied only by {ch, obj.ch}
			func(i int, pset utils.SSAValueSet) bool {
				if pset.Size() != 2 {
					return false
				}
				foundLines := make(map[int]struct{})

				pset.ForEach(func(v ssa.Value) {
					line := v.Parent().Pkg.Prog.Fset.PositionFor(v.Pos(), false).Line
					foundLines[line] = struct{}{}
				})

				for _, expected := range []int{9, 11} {
					if _, ok := foundLines[expected]; !ok {
						return false
					}
				}

				return true
			},
			// This predicate must be satisfied only by {ch}
			func(i int, pset utils.SSAValueSet) bool {
				var foundLine bool
				if pset.Size() == 1 {
					pset.ForEach(func(v ssa.Value) {
						line := v.Parent().Pkg.Prog.Fset.PositionFor(v.Pos(), false).Line
						foundLine = foundLine || line == 9
					})
				}

				return foundLine
			})
	})

	t.Run("chan-uncertain-carries-obj", func(t *testing.T) {
		loadRes := testutil.LoadPackageFromSource(t, "test", `
			package main
			var ubool bool

			type Object struct {
				ch chan int
			}

			func main() {
				var ch chan *Object
				if ubool {
					ch = make(chan *Object, 1)
				} else {
					ch = make(chan *Object, 1)
				}
				ch <- &Object{
					ch : make(chan int),
				}
				<-((<-ch).ch)
			}`)

		psets := getPsets(loadRes, "main")

		verifyPsets(t, psets,
			// This predicate must be satisfied only by {ch₁, ch₂, obj.ch}
			func(i int, pset utils.SSAValueSet) bool {
				if pset.Size() != 3 {
					return false
				}
				foundLines := make(map[int]struct{})

				pset.ForEach(func(v ssa.Value) {
					line := v.Parent().Pkg.Prog.Fset.PositionFor(v.Pos(), false).Line
					foundLines[line] = struct{}{}
				})

				for _, expected := range []int{12, 14, 17} {
					if _, ok := foundLines[expected]; !ok {
						return false
					}
				}

				return true
			},
			// This predicate must be satisfied only by {ch₁, ch₂}
			func(i int, pset utils.SSAValueSet) bool {
				if pset.Size() != 2 {
					return false
				}
				foundLines := make(map[int]struct{})

				pset.ForEach(func(v ssa.Value) {
					line := v.Parent().Pkg.Prog.Fset.PositionFor(v.Pos(), false).Line
					foundLines[line] = struct{}{}
				})

				for _, expected := range []int{12, 14} {
					if _, ok := foundLines[expected]; !ok {
						return false
					}
				}

				return true
			})
	})

	t.Run("chan-carries-obj-uncertain", func(t *testing.T) {
		loadRes := testutil.LoadPackageFromSource(t, "test", `
			package main
			var ubool bool

			type Object struct {
				ch chan int
			}

			func main() {
				ch := make(chan *Object, 1)
				var obj *Object
				if ubool {
					obj = &Object{
						ch: make(chan int),
					}
				} else {
					obj = &Object{
						ch: make(chan int),
					}
				}
				ch <- obj
				<-((<-ch).ch)
			}`)

		psets := getPsets(loadRes, "main")

		verifyPsets(t, psets,
			// This predicate must be satisfied only by {obj.ch₁, ch}
			func(i int, pset utils.SSAValueSet) bool {
				if pset.Size() != 2 {
					return false
				}
				foundLines := make(map[int]struct{})

				pset.ForEach(func(v ssa.Value) {
					line := v.Parent().Pkg.Prog.Fset.PositionFor(v.Pos(), false).Line
					foundLines[line] = struct{}{}
				})

				for _, expected := range []int{10, 14} {
					if _, ok := foundLines[expected]; !ok {
						return false
					}
				}

				return true
			},
			// This predicate must be satisfied only by {obj.ch₂,  ch}
			func(i int, pset utils.SSAValueSet) bool {
				if pset.Size() != 2 {
					return false
				}
				foundLines := make(map[int]struct{})

				pset.ForEach(func(v ssa.Value) {
					line := v.Parent().Pkg.Prog.Fset.PositionFor(v.Pos(), false).Line
					foundLines[line] = struct{}{}
				})

				for _, expected := range []int{10, 18} {
					if _, ok := foundLines[expected]; !ok {
						return false
					}
				}

				return true
			},
			// This predicate must be satisfied only by {ch}
			func(i int, pset utils.SSAValueSet) bool {
				var foundLine bool
				if pset.Size() == 1 {
					pset.ForEach(func(v ssa.Value) {
						line := v.Parent().Pkg.Prog.Fset.PositionFor(v.Pos(), false).Line
						foundLine = foundLine || line == 10
					})
				}

				return foundLine
			})
	})

	t.Run("chan--carries-embedded-struct", func(t *testing.T) {
		loadRes := testutil.LoadPackageFromSource(t, "test", `
			package main

			type Object struct { // Object wrapping channel
				ch chan int
			}

			type Wrapper struct { // Wrapper around Object
				Object
			}

			func main() {
				ch := make(chan Wrapper, 1)
				ch <- Wrapper{
					Object: Object{
						ch : make(chan int),
					},
				}
				<-((<-ch).ch)
			}`)

		psets := getPsets(loadRes, "main")

		verifyPsets(t, psets,
			// This predicate must be satisfied only by {ch, wrapper.Object.ch}
			func(i int, pset utils.SSAValueSet) bool {
				if pset.Size() != 2 {
					return false
				}
				foundLines := make(map[int]struct{})

				pset.ForEach(func(v ssa.Value) {
					line := v.Parent().Pkg.Prog.Fset.PositionFor(v.Pos(), false).Line
					foundLines[line] = struct{}{}
				})

				for _, expected := range []int{13, 16} {
					if _, ok := foundLines[expected]; !ok {
						return false
					}
				}

				return true
			},
			// This predicate must be satisfied only by {ch}
			func(i int, pset utils.SSAValueSet) bool {
				var foundLine bool
				if pset.Size() == 1 {
					pset.ForEach(func(v ssa.Value) {
						line := v.Parent().Pkg.Prog.Fset.PositionFor(v.Pos(), false).Line
						foundLine = foundLine || line == 13
					})
				}

				return foundLine
			})
	})

	t.Run("chan--carries-embedded-obj", func(t *testing.T) {
		loadRes := testutil.LoadPackageFromSource(t, "test", `
			package main

			type Object struct { // Object wrapping channel
				ch chan int
			}

			type Wrapper struct { // Wrapper around Object
				Object
			}

			func main() {
				ch := make(chan *Wrapper, 1)
				ch <- &Wrapper{
					Object: Object{
						ch : make(chan int),
					},
				}
				<-((<-ch).ch)
			}`)

		psets := getPsets(loadRes, "main")

		verifyPsets(t, psets,
			// This predicate must be satisfied only by {ch, wrapper.Object.ch}
			func(i int, pset utils.SSAValueSet) bool {
				if pset.Size() != 2 {
					return false
				}
				foundLines := make(map[int]struct{})

				pset.ForEach(func(v ssa.Value) {
					line := v.Parent().Pkg.Prog.Fset.PositionFor(v.Pos(), false).Line
					foundLines[line] = struct{}{}
				})

				for _, expected := range []int{13, 16} {
					if _, ok := foundLines[expected]; !ok {
						return false
					}
				}

				return true
			},
			// This predicate must be satisfied only by {ch}
			func(i int, pset utils.SSAValueSet) bool {
				var foundLine bool
				if pset.Size() == 1 {
					pset.ForEach(func(v ssa.Value) {
						line := v.Parent().Pkg.Prog.Fset.PositionFor(v.Pos(), false).Line
						foundLine = foundLine || line == 13
					})
				}

				return foundLine
			})
	})
}
