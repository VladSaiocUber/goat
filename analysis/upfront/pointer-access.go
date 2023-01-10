package upfront

import (
	"go/types"
	"log"
	"regexp"
	"strings"

	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
)

type (
	// accessPath is a baseline from which access actions used by points-to labels may be derived.
	accessPath struct{}

	// FieldAccess is an access action that models reading the field of a struct value. The "Field"
	// field encodes the name of the field.
	FieldAccess struct {
		accessPath
		Field string
	}
	// ArrayAccess is an access action that models reading an index in an array or slice value.
	ArrayAccess struct{ accessPath }

	// Access is implemented by all access actions.
	Access interface{ accessTag() }
)

// accessTag is implemented by any access action
func (accessPath) accessTag() {}

// pathRegexp encodes access paths, which can be of the form: x.y.[*].
// We specify that field names can contain anything but a dot and an open square bracket
var pathRegexp = regexp.MustCompile(`\.[^.[]+|\[\*\]`)

// SplitLabel takes a points-to analysis label and splits it into the root SSA value,
// and a sequence of access actions composing an access path.
func SplitLabel(label *pointer.Label) (ssa.Value, []Access) {
	v := label.Value()
	if path := label.Path(); path == "" {
		// If the label does not contain an access path, return the SSA value
		// and an empty set of access actions.
		return v, nil
	} else {
		components := pathRegexp.FindAllString(path, -1)
		if strings.Join(components, "") != path {
			log.Fatalln("Path match was not full", components, path, label)
		}

		accesses := make([]Access, len(components))
		for i, f := range components {
			// Check whether the access action is an array access, [*], or field access.
			if f == "[*]" {
				accesses[i] = ArrayAccess{}
			} else {
				// The dot before the field name is discarded
				accesses[i] = FieldAccess{Field: f[1:]}
			}
		}
		return v, accesses
	}
}

// getExtendedQueries returns a set of extended queries for accessing
// communication primitives in aggregate data structures. It constructs the
// extended query based on the type's value.
func getExtendedQueries(v ssa.Value) []string {
	t, prefix := v.Type().Underlying(), "x"
	if pt, ok := t.(*types.Pointer); ok {
		t = pt.Elem()
	}

	labels := []string{}

	var collectLabels func(types.Type, string)
	collectLabels = func(t types.Type, prefix string) {
		switch t := t.Underlying().(type) {
		case *types.Struct:
			for i := 0; i < t.NumFields(); i++ {
				ftv := t.Field(i)
				collectLabels(ftv.Type(), prefix+"."+ftv.Name())
			}
		case *types.Chan:
			labels = append(labels, prefix)
		}
	}

	collectLabels(t, prefix)

	return labels
}
