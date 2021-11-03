package protohcl

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-ctypb/ctystructpb"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
)

// nilProtoValue is the zero value of protoreflect.Value, used internally to
// signal the total absense of a value in some context.
var nilProtoValue protoreflect.Value = protoreflect.ValueOf(nil)

// valueForMessageField determines a suitable value to write into a proto field
// that is either a single message, a list of messages, or a map from
// strings to messages.
//
// May return schemaError or attrValueError errors to represent invalid schema
// or invalid user input respectively. In the absense of errors, the returned
// value might be the invalid nilProtoValue to represent that this field should
// just be cleared and not actually populated at all.
func valueForMessageField(v cty.Value, attr FieldAttribute, parentMessage protoreflect.Message) (protoreflect.Value, error) {
	field := attr.TargetField
	wantTy, diags := attr.TypeConstraint()
	if diags.HasErrors() {
		return nilProtoValue, schemaErrorf(field.FullName(), "invalid HCL type constraint")
	}

	builder, err := getFieldAttrMessageBuilder(field, wantTy)
	if err != nil {
		return nilProtoValue, err
	}
	path := make(cty.Path, 0, 4) // some capacity to grow
	return builder(v, path, parentMessage)
}

// isMessageField determines whether the given field is one that ought to
// be handled using valueForMessageField.
func isMessageField(attr FieldAttribute) bool {
	desc := attr.TargetField
	if desc.IsMap() {
		desc = desc.MapValue()
	}
	return desc.Kind() == protoreflect.MessageKind
}

func protoValueIsSet(pv protoreflect.Value) bool {
	return pv.Interface() != nil
}

// attrMessageBuilder represents a particular strategy for decoding an
// HCL attribute value into a protobuf message.
//
// If an attrMessageBuilder returns an error then it should typically be
// an attrValueError with an appropriate path, so that the caller can generate
// a helpful diagnostic message.
type attrMessageBuilder func(v cty.Value, path cty.Path, parentMessage protoreflect.Message) (protoreflect.Value, error)

var _ structpb.Value // Just to make sure we have this at compile time
var structpbValueDesc = structpb.File_google_protobuf_struct_proto.Messages().ByName("Value")

// getFieldAttrMessageBuilder decides on what strategy we'll take to map
// an HCL attribute value onto a field whose element type is a message type.
//
// Some well-known target message types in the protobuf ecosystem have
// special decoding strategies, allowing us to target them even though they
// don't have HCL-specific options of their own. Otherwise, we use a generic
// strategy that tries to conform an HCL object type to an HCL-annotated
// message type in a way that should be the opposite of what
// ObjectValueForMessage does.
func getFieldAttrMessageBuilder(desc protoreflect.FieldDescriptor, wantTy cty.Type) (attrMessageBuilder, error) {
	elemDesc := desc
	if desc.IsMap() {
		if desc.MapKey().Kind() != protoreflect.StringKind {
			return nil, schemaErrorf(desc.FullName(), "HCL can only support maps with string keys")
		}

		elemDesc = desc.MapValue()
	}

	// callers should only send message-typed fields to getFieldAttrMessageBuilder,
	// so if we panic here then it's a bug in the caller.
	elemMsgDesc := elemDesc.Message()
	elemMsgType := elemMsgDesc.FullName()

	switch {
	case elemMsgType == structpbValueDesc.FullName():
		return structpbAttrMessageBuilder(desc, wantTy)
	default:
		// TODO: Add a fallback decoder that does the inverse of what
		// ObjectValueForMessage does.
		return nil, schemaErrorf(desc.FullName(), "can't decode attribute into message type %s", elemMsgType)
	}
}

func structpbAttrMessageBuilder(desc protoreflect.FieldDescriptor, wantTy cty.Type) (attrMessageBuilder, error) {
	switch {
	case desc.IsList():
		if !(wantTy == cty.DynamicPseudoType || wantTy.IsListType() || wantTy.IsSetType()) {
			return nil, schemaErrorf(desc.FullName(), "list field must have tuple, list, or set type constraint")
		}
		return func(v cty.Value, path cty.Path, parentMessage protoreflect.Message) (protoreflect.Value, error) {
			if v.IsNull() {
				return nilProtoValue, attrValueErrorf(path, "must not be null")
			}
			if !v.IsKnown() {
				return nilProtoValue, attrValueErrorf(path, "value must be known")
			}
			ty := v.Type()
			if !(ty.IsListType() || ty.IsSetType() || ty.IsTupleType()) {
				return nilProtoValue, attrValueErrorf(path, "a list, set, or tuple value is required")
			}
			if wantTy.IsTupleType() {
				wantLen := len(wantTy.TupleElementTypes())
				gotLen := v.LengthInt()
				if wantLen != gotLen {
					return nilProtoValue, attrValueErrorf(path, "wrong number of elements (need %d)", wantLen)
				}
			}
			l := parentMessage.NewField(desc).List()
			i := 0
			for it := v.ElementIterator(); it.Next(); i++ {
				_, elemV := it.Element()

				var wantEty cty.Type
				switch {
				case wantTy == cty.DynamicPseudoType:
					wantEty = cty.DynamicPseudoType
				case wantTy.IsCollectionType():
					wantEty = wantTy.ElementType()
				case wantTy.IsTupleType():
					wantEty = wantTy.TupleElementType(i) // we checked that value length matches tuple type length above
				default:
					// Shouldn't get here because we already filtered wantTy above
					panic(fmt.Sprintf("unhandled type %#v", wantTy))
				}

				protoElem, err := ctystructpb.ToStructValue(elemV, wantEty)
				if err != nil {
					return nilProtoValue, attrValueErrorWrap(path, err)
				}
				l.Append(protoreflect.ValueOfMessage(protoElem.ProtoReflect()))
			}
			return protoreflect.ValueOfList(l), nil
		}, nil
	case desc.IsMap():
		if !(wantTy == cty.DynamicPseudoType || wantTy.IsObjectType() || wantTy.IsMapType()) {
			return nil, schemaErrorf(desc.FullName(), "map field must have object or map type constraint")
		}
		return func(v cty.Value, path cty.Path, parentMessage protoreflect.Message) (protoreflect.Value, error) {
			if v.IsNull() {
				return nilProtoValue, attrValueErrorf(path, "must not be null")
			}
			if !v.IsKnown() {
				return nilProtoValue, attrValueErrorf(path, "value must be known")
			}
			ty := v.Type()
			if !(ty.IsObjectType() || ty.IsMapType()) {
				return nilProtoValue, attrValueErrorf(path, "an object or map value is required")
			}
			m := parentMessage.NewField(desc).Map()
			for it := v.ElementIterator(); it.Next(); {
				elemKV, elemV := it.Element()
				elemK := elemKV.AsString()

				var wantEty cty.Type
				switch {
				case wantTy == cty.DynamicPseudoType:
					wantEty = cty.DynamicPseudoType
				case wantTy.IsMapType():
					wantEty = wantTy.ElementType()
				case wantTy.IsObjectType():
					if !wantTy.HasAttribute(elemK) {
						return nilProtoValue, attrValueErrorf(path, "does not expect an attribute named %q", elemK)
					}
					wantEty = wantTy.AttributeType(elemK)
				default:
					// Shouldn't get here because we already filtered wantTy above
					panic(fmt.Sprintf("unhandled type %#v", wantTy))
				}

				protoElem, err := ctystructpb.ToStructValue(elemV, wantEty)
				if err != nil {
					return nilProtoValue, attrValueErrorWrap(path, err)
				}
				m.Set(protoreflect.ValueOfString(elemK).MapKey(), protoreflect.ValueOfMessage(protoElem.ProtoReflect()))
			}
			return protoreflect.ValueOfMap(m), nil
		}, nil
	default:
		return func(v cty.Value, path cty.Path, parentMessage protoreflect.Message) (protoreflect.Value, error) {
			sv, err := ctystructpb.ToStructValue(v, wantTy)
			if err != nil {
				return nilProtoValue, err
			}
			return protoreflect.ValueOfMessage(sv.ProtoReflect()), nil
		}, nil
	}
}

// attrValueError is an error type used for any situation where a given value
// isn't suitable for the context where it's used.
//
// A attrValueError is always a problem with user input, and never a bug in
// the program. (If it _is_ a bug in the program, then it's a bug in protohcl
// that it was misclassified!)
type attrValueError struct {
	Err cty.PathError
}

func attrValueErrorf(path cty.Path, format string, args ...interface{}) attrValueError {
	return attrValueError{
		Err: path.NewErrorf(format, args...).(cty.PathError),
	}
}

func attrValueErrorWrap(path cty.Path, err error) attrValueError {
	switch err := err.(type) {
	case cty.PathError:
		err = path.NewError(err).(cty.PathError)
		return attrValueError{Err: err}
	default:
		return attrValueError{Err: path.NewError(err).(cty.PathError)}
	}
}

func (err attrValueError) Error() string {
	return err.Error()
}

func (err attrValueError) Unwrap() error {
	return err.Err
}

func (err attrValueError) Diagnostic() *hcl.Diagnostic {
	var detail string
	if len(err.Err.Path) == 0 {
		// The top-level attribute value is wrong.
		detail = fmt.Sprintf("Inappropriate value for argument: %s.", err.Err.Error())
	} else {
		detail = fmt.Sprintf("Inappropriate value for %s: %s.", formatCtyPath(err.Err.Path), err.Err.Error())
	}
	return &hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  unsuitableValueSummary,
		Detail:   detail,
	}
}

func attrErrorDiagnostic(err error) *hcl.Diagnostic {
	switch err := err.(type) {
	case schemaError:
		return err.Diagnostic()
	case attrValueError:
		return err.Diagnostic()
	default:
		return &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  unsuitableValueSummary,
			Detail: fmt.Sprintf(
				"Inappropriate value for argument: %s.",
				err.Error(),
			),
		}
	}
}
