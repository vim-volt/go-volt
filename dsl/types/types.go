package types

// Type is a type of a value
type Type interface {
	// String returns a string like "<type %s>"
	String() string

	// InstanceOf checks has-a relation with its argument type
	InstanceOf(Type) bool
}

// ===================== VoidType ===================== //

// VoidType is void type
type VoidType struct{}

func (*VoidType) String() string {
	return "Void"
}

// InstanceOf returns true if t is VoidType
func (*VoidType) InstanceOf(t Type) bool {
	if _, ok := t.(*VoidType); ok {
		return true
	}
	return false
}

// ===================== NullType ===================== //

// NullType is JSON null type
type NullType struct{}

func (*NullType) String() string {
	return "Null"
}

// InstanceOf returns true if t is NullType
func (*NullType) InstanceOf(t Type) bool {
	if _, ok := t.(*NullType); ok {
		return true
	}
	return false
}

// ===================== BoolType ===================== //

// BoolType is JSON boolean type
type BoolType struct{}

func (*BoolType) String() string {
	return "Bool"
}

// InstanceOf returns true if t is BoolType
func (*BoolType) InstanceOf(t Type) bool {
	if _, ok := t.(*BoolType); ok {
		return true
	}
	return false
}

// ===================== NumberType ===================== //

// NumberType is JSON number type
type NumberType struct{}

func (*NumberType) String() string {
	return "Number"
}

// InstanceOf returns true if t is NumberType
func (*NumberType) InstanceOf(t Type) bool {
	if _, ok := t.(*NumberType); ok {
		return true
	}
	return false
}

// ===================== StringType ===================== //

// StringType is JSON string type
type StringType struct{}

func (*StringType) String() string {
	return "String"
}

// InstanceOf returns true if t is StringType
func (*StringType) InstanceOf(t Type) bool {
	if _, ok := t.(*StringType); ok {
		return true
	}
	return false
}

// ===================== ArrayType ===================== //

// ArrayType is JSON array type
type ArrayType struct {
	Arg Type
}

func (t *ArrayType) String() string {
	return "Array[" + t.Arg.String() + "]"
}

// InstanceOf returns true if t is instance of t2
func (t *ArrayType) InstanceOf(t2 Type) bool {
	if array, ok := t2.(*ArrayType); ok {
		return t.Arg.InstanceOf(array.Arg)
	}
	return false
}

// ===================== ObjectType ===================== //

// ObjectType is JSON object type
type ObjectType struct {
	Arg Type
}

func (t *ObjectType) String() string {
	return "Object[" + t.Arg.String() + "]"
}

// InstanceOf returns true if t is instance of t2
func (t *ObjectType) InstanceOf(t2 Type) bool {
	if array, ok := t2.(*ObjectType); ok {
		return t.Arg.InstanceOf(array.Arg)
	}
	return false
}

// ===================== AnyType ===================== //

// AnyType allows any type
type AnyType struct{}

func (*AnyType) String() string {
	return "Any"
}

// InstanceOf always returns true
func (*AnyType) InstanceOf(_ Type) bool {
	return true
}
