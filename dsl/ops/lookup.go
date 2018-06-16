package ops

import "github.com/vim-volt/volt/dsl/types"

// opsMap holds all operation structs.
// All operations in dsl/op/*.go sets its struct to this in init()
var opsMap = make(map[string]types.Op)

// Lookup looks up operator name
func Lookup(name string) (types.Op, bool) {
	op, exists := opsMap[name]
	return op, exists
}
