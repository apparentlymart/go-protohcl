package protohcl

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-ctypb/ctystructpb"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
)

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
func getFieldAttrMessageBuilder(attr FieldAttribute) (attrMessageBuilder, error) {
	desc := attr.TargetField
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
		return structpbAttrMessageBuilder(attr)
	default:
		// TODO: Add a fallback decoder that does the inverse of what
		// ObjectValueForMessage does.
		return nil, schemaErrorf(desc.FullName(), "can't decode attribute into message type %s", elemDesc.FullName())
	}
}

func structpbAttrMessageBuilder(attr FieldAttribute) (attrMessageBuilder, error) {
	desc := attr.TargetField

	wantTy, diags := attr.TypeConstraint()
	if diags.HasErrors() {
		return nil, schemaErrorf(desc.FullName(), "invalid HCL type constraint")
	}

	switch {
	case desc.IsList():
		return func(v cty.Value, path cty.Path, parentMessage protoreflect.Message) (protoreflect.Value, error) {
			return parentMessage.NewField(desc), schemaErrorf(desc.FullName(), "can't decode into list of google.protobuf.Struct")
		}, nil
	case desc.IsMap():
		if !(wantTy == cty.DynamicPseudoType || wantTy.IsObjectType() || wantTy.IsMapType()) {
			return nil, schemaErrorf(desc.FullName(), "map field must have object or map type constraint")
		}
		return func(v cty.Value, path cty.Path, parentMessage protoreflect.Message) (protoreflect.Value, error) {
			ty := v.Type()
			if !(ty.IsObjectType() || ty.IsMapType()) {
				return parentMessage.NewField(desc), attrValueErrorf(path, "an object or map value is required")
			}
			if v.IsNull() {
				return parentMessage.NewField(desc), attrValueErrorf(path, "must not be null")
			}
			if !v.IsKnown() {
				return parentMessage.NewField(desc), attrValueErrorf(path, "value must be known")
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
						return protoreflect.ValueOfMap(m), attrValueErrorf(path, "does not expect an attribute named %q", elemK)
					}
					wantEty = wantTy.AttributeType(elemK)
				default:
					// Shouldn't get here because we already filtered wantTy above
					panic(fmt.Sprintf("unhandled type %#v", wantTy))
				}

				protoElem, err := ctystructpb.ToStructValue(elemV, wantEty)
				if err != nil {
					return protoreflect.ValueOfMap(m), attrValueErrorWrap(path, err)
				}
				m.Set(protoreflect.ValueOfString(elemK).MapKey(), protoreflect.ValueOfMessage(protoElem.ProtoReflect()))
			}
			return protoreflect.ValueOfMap(m), nil
		}, nil
	default:
		return func(v cty.Value, path cty.Path, parentMessage protoreflect.Message) (protoreflect.Value, error) {
			return parentMessage.NewField(desc), schemaErrorf(desc.FullName(), "can't decode into google.protobuf.Struct")
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
