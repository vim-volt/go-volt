package dsl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/vim-volt/volt/dsl/ops"
	"github.com/vim-volt/volt/dsl/types"
)

// Parse parses expr JSON. And if an array literal value is found:
// 1. Split to operation and its arguments
// 2. Do semantic analysis recursively for its arguments
// 3. Convert to *Expr
func Parse(content []byte) (types.Expr, error) {
	var value interface{}
	if err := json.Unmarshal(content, value); err != nil {
		return nil, err
	}
	array, ok := value.([]interface{})
	if !ok {
		return nil, errors.New("top-level must be an array")
	}
	arrayValue, err := parseArray(array)
	if err != nil {
		return nil, err
	}
	// If expression's operator is a macro, return value may not be an array
	// (e.g. ["macro", 1, 2])
	expr, ok := arrayValue.(types.Expr)
	if !ok {
		return nil, errors.New("the result must be an expression")
	}
	return expr, nil
}

func parseArray(array []interface{}) (types.Value, error) {
	if len(array) == 0 {
		return nil, errors.New("expected operation but got an empty array")
	}
	opName, ok := array[0].(string)
	if !ok {
		return nil, fmt.Errorf("expected operator (string) but got '%+v'", array[0])
	}
	args := make([]types.Value, 0, len(array)-1)
	for i := 1; i < len(array); i++ {
		v, err := parse(array[i])
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}
	op, exists := ops.Lookup(opName)
	if !exists {
		return nil, fmt.Errorf("no such operation '%s'", opName)
	}
	// Expand macro's expression at parsing time
	if op.IsMacro() {
		val, _, err := op.EvalExpr(context.Background(), args)
		return val, err
	}
	return op.Bind(args...)
}

func parse(value interface{}) (types.Value, error) {
	switch val := value.(type) {
	case nil:
		return types.NullValue, nil
	case bool:
		return types.NewBool(val), nil
	case string:
		return types.NewString(val), nil
	case float64:
		return types.NewNumber(val), nil
	case map[string]interface{}:
		m := make(map[string]types.Value, len(val))
		for k, o := range m {
			v, err := parse(o)
			if err != nil {
				return nil, err
			}
			m[k] = v
		}
		return types.NewObject(m, types.AnyValue), nil
	case []interface{}:
		return parseArray(val)
	default:
		return nil, fmt.Errorf("unknown value was given '%+v'", val)
	}
}
