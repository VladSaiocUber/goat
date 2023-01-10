package utils

import (
	"fmt"
	"go/token"

	"golang.org/x/tools/go/ssa"
)

func PrintSSAFun(fun *ssa.Function) {
	fmt.Println(fun.Name())
	for bi, b := range fun.Blocks {
		fmt.Println(bi, ":")
		for _, i := range b.Instrs {
			switch v := i.(type) {
			case *ssa.DebugRef:
				// skip
			case ssa.Value:
				fmt.Println(v.Name(), "=", v)
			default:
				fmt.Println(i)
			}
		}
	}
}

func PrintSSAFunWithPos(fset *token.FileSet, fun *ssa.Function) {
	fmt.Println(fun.Name())
	for bi, b := range fun.Blocks {
		fmt.Println(bi, ":")
		for _, i := range b.Instrs {
			switch v := i.(type) {
			case *ssa.DebugRef:
				// skip
			case ssa.Value:
				fmt.Println(v.Name(), "=", v, "at position:", fset.Position(v.Pos()))
			default:
				fmt.Println(i, "at position:", fset.Position(i.Pos()))
			}
		}
	}
}
