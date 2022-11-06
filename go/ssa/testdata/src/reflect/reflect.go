package reflect

type Type interface {
	Elem() Type
	Kind() Kind
	String() string
}

type Value struct{}

func (Value) String() string
func (Value) Elem() Value
func (Value) Field(int) Value
func (Value) Index(i int) Value
func (Value) Int() int64
func (Value) Interface() any
func (Value) IsNil() bool
func (Value) IsValid() bool
func (Value) Kind() Kind
func (Value) Len() int
func (Value) MapIndex(Value) Value
func (Value) MapKeys() []Value
func (Value) NumField() int
func (Value) Pointer() uintptr
func (Value) SetInt(int64)
func (Value) Type() Type

func SliceOf(Type) Type
func TypeOf(any) Type
func ValueOf(any) Value

type Kind uint

const (
	Invalid Kind = iota
	Int
	Pointer
)

func DeepEqual(x, y any) bool
