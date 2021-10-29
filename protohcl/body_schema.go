package protohcl

import (
	"fmt"

	"github.com/apparentlymart/go-protohcl/protohcl/protohclext"
	"github.com/hashicorp/hcl/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// bodySchema constucts a HCL body schema from the given message descriptor,
// or returns an error explaining why the descriptor is invalid for HCL use.
func bodySchema(desc protoreflect.MessageDescriptor) (*hcl.BodySchema, error) {
	ret := hcl.BodySchema{}
	// We'll also track which names we've used already, so we can detect
	// and report conflicts.
	attrs := map[string]protoreflect.FullName{}
	blockTypes := map[string]protoreflect.FullName{}

	fieldCount := desc.Fields().Len()
	for i := 0; i < fieldCount; i++ {
		field := desc.Fields().Get(i)

		opts, ok := field.Options().(*descriptorpb.FieldOptions)
		if !ok {
			// If missing or totally invalid options then we skip this one.
			// This isn't an error because the schema didn't explicitly opt in
			// to HCL processing yet.
			continue
		}

		// These extensions are all mutually-exclusive with one another,
		// because each proto field must map to zero or one HCL schema
		// constructs.
		attrOpts := proto.GetExtension(opts, protohclext.E_Attr).(*protohclext.Attribute)
		blockOpts := proto.GetExtension(opts, protohclext.E_Block).(*protohclext.NestedBlock)
		flatten := proto.GetExtension(opts, protohclext.E_Flatten).(bool)
		labelOpts := proto.GetExtension(opts, protohclext.E_Label).(*protohclext.BlockLabel)

		switch {
		case attrOpts != nil && attrOpts.Name != "":
			if blockOpts != nil && blockOpts.TypeName != "" {
				return nil, schemaErrorf(field.FullName(), "cannot be both attribute %q and nested block type %q", attrOpts.Name, blockOpts.TypeName)
			}
			if flatten {
				return nil, schemaErrorf(field.FullName(), "cannot be attribute %q and also flatten into the current body", attrOpts.Name)
			}
			if labelOpts != nil && labelOpts.Name != "" {
				return nil, schemaErrorf(field.FullName(), "cannot be both attribute %q and block label %q", attrOpts.Name, labelOpts.Name)
			}

			attrS, err := attributeSchema(field, attrOpts)
			if err != nil {
				return nil, err // err should already be a schemaError
			}
			if existingName, exists := attrs[attrS.Name]; exists {
				return nil, schemaErrorf(field.FullName(), "declaration of attribute %q conflicts with %s", attrS.Name, existingName)
			}
			if existingName, exists := blockTypes[attrS.Name]; exists {
				return nil, schemaErrorf(field.FullName(), "declaration of attribute %q conflicts with block type declared by %s", attrS.Name, existingName)
			}
			ret.Attributes = append(ret.Attributes, attrS)
			attrs[attrS.Name] = field.FullName()

		case blockOpts != nil && blockOpts.TypeName != "":
			if flatten {
				return nil, schemaErrorf(field.FullName(), "cannot be nested block type %q and also flatten into the current body", attrOpts.Name)
			}
			if labelOpts != nil && labelOpts.Name != "" {
				return nil, schemaErrorf(field.FullName(), "cannot be both nested block type %q and block label %q", attrOpts.Name, labelOpts.Name)
			}

			blockS, err := blockTypeSchema(field, blockOpts)
			if err != nil {
				return nil, err // err should already be a schemaError
			}
			if existingName, exists := attrs[blockS.Type]; exists {
				return nil, schemaErrorf(field.FullName(), "declaration of block type %q conflicts with attribute declared by %s", blockS.Type, existingName)
			}
			if existingName, exists := blockTypes[blockS.Type]; exists {
				return nil, schemaErrorf(field.FullName(), "declaration of block type %q conflicts with %s", blockS.Type, existingName)
			}
			ret.Blocks = append(ret.Blocks, blockS)
			blockTypes[blockS.Type] = field.FullName()

		case flatten:
			if labelOpts.Name != "" {
				return nil, schemaErrorf(field.FullName(), "cannot be block label %q and also flatten into the current body", labelOpts.Name)
			}

			return nil, schemaErrorf(field.FullName(), "flatten mode not implemented yet")

		case labelOpts != nil && labelOpts.Name != "":
			// We don't care about labels when we're dealing with bodies. That's
			// relevent only for nested message types in blockTypeSchema.
			continue

		default:
			// Otherwise this field isn't relevant to HCL at all, and we'll
			// totally ignore it.
			continue
		}

	}

	return &ret, nil
}

func attributeSchema(desc protoreflect.FieldDescriptor, opts *protohclext.Attribute) (hcl.AttributeSchema, error) {
	return hcl.AttributeSchema{
		Name:     opts.Name,
		Required: opts.Required,

		// At the HCL raw schema level we don't actually care about the type
		// or encoding mode yet. That'll be for the decoder to deal with once
		// it's holding the value of a concrete hcl.Expression. However,
		// that does mean that some schemaError results will be deferred until
		// decoding time.
	}, nil
}

func blockTypeSchema(desc protoreflect.FieldDescriptor, opts *protohclext.NestedBlock) (hcl.BlockHeaderSchema, error) {
	if desc.Kind() != protoreflect.MessageKind {
		return hcl.BlockHeaderSchema{}, schemaErrorf(desc.FullName(), "field representing nested block must have message type, not %s", desc.Kind())
	}

	// We need to search in the nested message for any label-annotated fields,
	// which will each in turn define one block label.
	var labelNames []string
	msg := desc.Message()
	fieldCount := msg.Fields().Len()

	for i := 0; i < fieldCount; i++ {
		field := msg.Fields().Get(i)

		opts, ok := field.Options().(*descriptorpb.FieldOptions)
		if !ok {
			continue
		}

		if !proto.HasExtension(opts, protohclext.E_Label) {
			continue
		}
		labelOpts := proto.GetExtension(opts, protohclext.E_Label).(*protohclext.BlockLabel)
		labelNames = append(labelNames, labelOpts.Name)
	}

	return hcl.BlockHeaderSchema{
		Type:       opts.TypeName,
		LabelNames: labelNames,
	}, nil
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
