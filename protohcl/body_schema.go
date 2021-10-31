package protohcl

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// bodySchema constucts a HCL body schema from the given message descriptor,
// or returns an error explaining why the descriptor is invalid for HCL use.
func bodySchema(desc protoreflect.MessageDescriptor) (*hcl.BodySchema, error) {
	// For the moment we don't allow "oneofs" at all, except for the synthetic
	// ones used to represent nullable fields, because we don't yet have the
	// logic to return an error if the input configuration tries to populate
	// more than one oneof field at a time.
	// TODO: Implement that extra validation logic in the body decoder, and
	// then we can remove this restriction. When we do, we may wish to allow
	// annotating oneofs with an HCL-specific "required", because proto oneofs
	// are really "zero or one of" but in HCL we commonly want to require
	// exactly one of a set of possibilities.
	for i := 0; i < desc.Oneofs().Len(); i++ {
		oneOf := desc.Oneofs().Get(i)
		if !oneOf.IsSynthetic() {
			return nil, schemaErrorf(oneOf.FullName(), "oneof declarations are not yet supported in messages used for HCL decoding")
		}
	}

	ret := hcl.BodySchema{}
	// We'll also track which names we've used already, so we can detect
	// and report conflicts.
	attrs := map[string]protoreflect.FullName{}
	blockTypes := map[string]protoreflect.FullName{}
	blockLabels := map[string]protoreflect.FullName{}

	fieldCount := desc.Fields().Len()
	for i := 0; i < fieldCount; i++ {
		field := desc.Fields().Get(i)

		elem, err := GetFieldElem(field)
		if err != nil {
			return nil, err // should already be a schemaError
		}

		switch elem := elem.(type) {
		case FieldAttribute:
			attrS := attributeSchema(elem)
			if existingName, exists := attrs[attrS.Name]; exists {
				return nil, schemaErrorf(field.FullName(), "declaration of attribute %q conflicts with %s", attrS.Name, existingName)
			}
			if existingName, exists := blockTypes[attrS.Name]; exists {
				return nil, schemaErrorf(field.FullName(), "declaration of attribute %q conflicts with block type declared by %s", attrS.Name, existingName)
			}
			if existingName, exists := blockLabels[attrS.Name]; exists {
				return nil, schemaErrorf(field.FullName(), "declaration of attribute %q conflicts with block label name declared by %s", attrS.Name, existingName)
			}
			ret.Attributes = append(ret.Attributes, attrS)
			attrs[attrS.Name] = field.FullName()

		case FieldNestedBlockType:
			blockS := blockTypeSchema(elem)
			if existingName, exists := attrs[blockS.Type]; exists {
				return nil, schemaErrorf(field.FullName(), "declaration of block type %q conflicts with attribute declared by %s", blockS.Type, existingName)
			}
			if existingName, exists := blockTypes[blockS.Type]; exists {
				return nil, schemaErrorf(field.FullName(), "declaration of block type %q conflicts with %s", blockS.Type, existingName)
			}
			if existingName, exists := blockLabels[blockS.Type]; exists {
				return nil, schemaErrorf(field.FullName(), "declaration of block type %q conflicts with block label name declared by %s", blockS.Type, existingName)
			}
			ret.Blocks = append(ret.Blocks, blockS)
			blockTypes[blockS.Type] = field.FullName()

		case FieldFlattened:
			// For our schema-building purposes we'll deal with "flatten" by
			// just constructing a schema for the child message and then
			// merging it into the one we're currently working on.
			nestSchema, err := bodySchema(elem.Nested)
			if err != nil {
				return nil, schemaErrorf(desc.FullName(), "invalid message to flatten: %w", err)
			}
			for _, attrS := range nestSchema.Attributes {
				if existingName, exists := attrs[attrS.Name]; exists {
					return nil, schemaErrorf(field.FullName(), "flattened-in attribute %q conflicts with %s", attrS.Name, existingName)
				}
				if existingName, exists := blockTypes[attrS.Name]; exists {
					return nil, schemaErrorf(field.FullName(), "flattened-in attribute %q conflicts with block type declared by %s", attrS.Name, existingName)
				}
				if existingName, exists := blockLabels[attrS.Name]; exists {
					return nil, schemaErrorf(field.FullName(), "flattened-in attribute %q conflicts with block label name declared by %s", attrS.Name, existingName)
				}
				ret.Attributes = append(ret.Attributes, attrS)
				attrs[attrS.Name] = field.FullName()
			}
			for _, blockS := range nestSchema.Blocks {
				if existingName, exists := attrs[blockS.Type]; exists {
					return nil, schemaErrorf(field.FullName(), "flattened-in block type %q conflicts with attribute declared by %s", blockS.Type, existingName)
				}
				if existingName, exists := blockTypes[blockS.Type]; exists {
					return nil, schemaErrorf(field.FullName(), "flattened-in block type %q conflicts with %s", blockS.Type, existingName)
				}
				if existingName, exists := blockLabels[blockS.Type]; exists {
					return nil, schemaErrorf(field.FullName(), "flattened-in block type %q conflicts with block label name declared by %s", blockS.Type, existingName)
				}
				ret.Blocks = append(ret.Blocks, blockS)
				blockTypes[blockS.Type] = field.FullName()
			}

		case FieldBlockLabel:
			// While we're dealing with bodies we only care that the label
			// names don't collide with other declarations. We actually handle
			// the labels only in blockTypeSchema, for nested message types.
			if existingName, exists := attrs[elem.Name]; exists {
				return nil, schemaErrorf(field.FullName(), "block label name %q conflicts with attribute declared by %s", elem.Name, existingName)
			}
			if existingName, exists := blockTypes[elem.Name]; exists {
				return nil, schemaErrorf(field.FullName(), "block label name %q conflicts with %s", elem.Name, existingName)
			}
			if existingName, exists := blockLabels[elem.Name]; exists {
				return nil, schemaErrorf(field.FullName(), "block label name %q conflicts %s", elem.Name, existingName)
			}
			blockLabels[elem.Name] = field.FullName()

		default:
			// Otherwise this field isn't relevant to HCL at all, and we'll
			// totally ignore it.
			continue
		}

	}

	return &ret, nil
}

func attributeSchema(elem FieldAttribute) hcl.AttributeSchema {
	return hcl.AttributeSchema{
		Name:     elem.Name,
		Required: elem.Required,

		// At the HCL raw schema level we don't actually care about the type
		// or encoding mode yet. That'll be for the decoder to deal with once
		// it's holding the value of a concrete hcl.Expression. However,
		// that does mean that some schemaError results will be deferred until
		// decoding time.
	}
}

func blockTypeSchema(elem FieldNestedBlockType) hcl.BlockHeaderSchema {
	// We need to search in the nested message for any label-annotated fields,
	// which will each in turn define one block label.
	var labelNames []string
	msg := elem.Nested
	fieldCount := msg.Fields().Len()

	for i := 0; i < fieldCount; i++ {
		field := msg.Fields().Get(i)

		elem, err := GetFieldElem(field)
		if err != nil {
			// We'll catch this error in the caller anyway, so we'll just
			// ignore it.
			continue
		}

		switch elem := elem.(type) {
		case FieldBlockLabel:
			labelNames = append(labelNames, elem.Name)
		default:
			// Everything else is irrelevant for our purposes here.
		}
	}

	return hcl.BlockHeaderSchema{
		Type:       elem.TypeName,
		LabelNames: labelNames,
	}
}

// schemaError is an error type used for any situation where the given message
// descriptor has inconsistencies that make it unsuitable for whatever HCL
// operation was requested.
//
// A schemaError is always a bug in whatever software provided the schema, and
// not a user error.
type schemaError struct {
	// Decl is the fully-qualified name of the closest declaration that
	// reflects the problem being described.
	Decl protoreflect.FullName

	// Err is the underlying error.
	Err error
}

func schemaErrorf(decl protoreflect.FullName, format string, args ...interface{}) schemaError {
	return schemaError{
		Decl: decl,
		Err:  fmt.Errorf(format, args...),
	}
}

func (err schemaError) Error() string {
	if err.Decl != "" && err.Decl.IsValid() {
		return fmt.Sprintf("unsupported protobuf schema: %s", err.Err.Error())
	}
	return fmt.Sprintf("unsupported protobuf schema in %s: %s", err.Decl, err.Err.Error())
}

func (err schemaError) Unwrap() error {
	return err.Err
}

func (err schemaError) Diagnostic() *hcl.Diagnostic {
	return &hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  "Invalid configuration schema",
		Detail: fmt.Sprintf(
			"Invalid HCL annotations in protobuf schema for %s: %s.\n\nThis is a bug in the component that defined this schema, and not an error in the given configuration.",
			err.Decl, err.Err.Error(),
		),
	}
}

func schemaErrorDiagnostic(err error) *hcl.Diagnostic {
	switch err := err.(type) {
	case schemaError:
		return err.Diagnostic()
	default:
		return &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Invalid configuration schema",
			Detail: fmt.Sprintf(
				"Failed to construct configuration schema from protobuf schema: %s.\n\nThis is a bug in the component that defined this schema, and not an error in the given configuration.",
				err.Error(),
			),
		}
	}
}
