package models

import (
	"errors"
	"fmt"

	"github.com/go-playground/validator/v10"
)

// structValidator is the shared validator instance; validator.Struct is safe for
// concurrent use, so one instance serves every request.
var structValidator = validator.New()

// ValidateStruct runs the declarative field validators of a request DTO and
// returns a human-readable detail for the first failing field.
func ValidateStruct(value any) error {
	if err := structValidator.Struct(value); err != nil {
		var fieldErrors validator.ValidationErrors
		if errors.As(err, &fieldErrors) && len(fieldErrors) > 0 {
			return describeFieldError(fieldErrors[0])
		}
		return err
	}
	return nil
}

func describeFieldError(fieldError validator.FieldError) error {
	field := fieldError.Field()
	switch fieldError.Tag() {
	case "required":
		return fmt.Errorf("%s is required", field)
	case "oneof":
		return fmt.Errorf("%s must be one of %s", field, fieldError.Param())
	case "min":
		return fmt.Errorf("%s must be at least %s", field, fieldError.Param())
	case "max":
		return fmt.Errorf("%s must be at most %s", field, fieldError.Param())
	default:
		return fmt.Errorf("%s is invalid", field)
	}
}
