package i18n

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kaptinlin/messageformat-go/pkg/datamodel"
)

const formattedValueOverhead = 1024

type templateWork struct {
	patterns     []patternWork
	requirements formatRequirements
}

type patternWork struct {
	staticBytes int
	staticParts int
	expansions  []workExpansion
}

type workExpansion struct {
	argument     string
	literalBytes int
	function     string
	options      map[string]any
	offset       int64
}

func compileTemplateWork(model datamodel.Message, arguments []ArgumentSpec) templateWork {
	bindings := make(map[string]workExpansion, len(arguments)+len(model.Declarations()))
	specs := make(map[string]ArgumentSpec, len(arguments))
	for _, argument := range arguments {
		bindings[argument.Name] = workExpansion{argument: argument.Name}
		specs[argument.Name] = argument
	}
	for _, declaration := range model.Declarations() {
		var name string
		var expression *datamodel.Expression
		switch declaration := declaration.(type) {
		case *datamodel.InputDeclaration:
			name = declaration.Name()
			expression = declaration.Value()
		case *datamodel.LocalDeclaration:
			name = declaration.Name()
			expression = declaration.Value()
		}
		if expression != nil {
			bindings[name] = expressionWork(expression, bindings)
		}
	}
	patterns := make([]datamodel.Pattern, 0, 1)
	switch message := model.(type) {
	case *datamodel.PatternMessage:
		patterns = append(patterns, message.Pattern())
	case *datamodel.SelectMessage:
		patterns = make([]datamodel.Pattern, 0, len(message.Variants()))
		for _, variant := range message.Variants() {
			patterns = append(patterns, variant.Value())
		}
	}
	work := templateWork{patterns: make([]patternWork, 0, len(patterns))}
	for _, pattern := range patterns {
		profile := patternWork{}
		for _, element := range pattern.Elements() {
			switch element := element.(type) {
			case *datamodel.TextElement:
				profile.staticBytes += len(element.Value())
				profile.staticParts++
			case *datamodel.Markup:
				profile.staticParts++
			case *datamodel.Expression:
				profile.expansions = append(profile.expansions, expressionWork(element, bindings))
			}
		}
		work.patterns = append(work.patterns, profile)
	}
	for _, pattern := range work.patterns {
		for _, expansion := range pattern.expansions {
			work.requirements |= expansionFormatRequirements(expansion, specs)
		}
	}
	if selected, ok := model.(*datamodel.SelectMessage); ok {
		profiles := selectorProfiles(selected, specs)
		for _, selector := range selected.Selectors() {
			profile, exists := profiles[selector.Name()]
			if !exists {
				continue
			}
			work.requirements |= argumentFormatRequirements(profile.argument.Type)
			switch profile.function {
			case "number", "integer", "percent", "offset":
				work.requirements |= formatNumber
				selectMode, _ := optionString(profile.options, "select")
				if selectMode != "exact" {
					work.requirements |= formatPlural
				}
			case "currency", "unit":
				work.requirements |= formatNumber
			}
		}
	}
	return work
}

func expansionFormatRequirements(expansion workExpansion, specs map[string]ArgumentSpec) formatRequirements {
	var requirements formatRequirements
	switch expansion.function {
	case "number", "integer", "currency", "percent", "unit", "offset":
		requirements |= formatNumber
	case "date", "time", "datetime":
		requirements |= formatDate
	}
	if spec, exists := specs[expansion.argument]; exists {
		requirements |= argumentFormatRequirements(spec.Type)
	}
	return requirements
}

func argumentFormatRequirements(argumentType ArgumentType) formatRequirements {
	switch argumentType {
	case TypeInteger, TypeUnsignedInteger, TypeBigInteger, TypeDecimal, TypeMoney:
		return formatNumber
	case TypeDate, TypeInstant:
		return formatDate
	default:
		return 0
	}
}

func expressionWork(expression *datamodel.Expression, bindings map[string]workExpansion) workExpansion {
	var expansion workExpansion
	switch argument := expression.Arg().(type) {
	case *datamodel.VariableRef:
		expansion = bindings[argument.Name()]
	case *datamodel.Literal:
		expansion.literalBytes = len(argument.Value())
	}
	if function := expression.FunctionRef(); function != nil {
		name := function.Name()
		options := literalOptions(function.Options())
		if name == "offset" {
			delta, _ := offsetDelta(options)
			expansion.offset += delta
			if expansion.function == "" {
				expansion.function = "number"
			}
		} else {
			expansion.function = name
			if style, numeric := functionNumericStyle(name); numeric {
				expansion.options = resolvedNumberOptions(style, expansion.options, options)
			} else {
				expansion.options = options
			}
		}
	}
	return expansion
}

func (w templateWork) check(ctx context.Context, specs []ArgumentSpec, arguments []Argument, limits Limits, presentation Presentation, budget *renderBudget) error {
	byName := make(map[string]Argument, len(arguments))
	for _, argument := range arguments {
		byName[argument.name] = argument
	}
	bySpec := make(map[string]ArgumentSpec, len(specs))
	for _, spec := range specs {
		bySpec[spec.Name] = spec
	}
	maximumBytes := 0
	maximumParts := 0
	checks := 0
	for _, pattern := range w.patterns {
		bytes := pattern.staticBytes
		parts := pattern.staticParts
		isolationBytes := 6
		isolationParts := 2
		if presentation == PresentationNoIsolation {
			isolationBytes = 0
			isolationParts = 0
		}
		for _, expansion := range pattern.expansions {
			if checks&31 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			checks++
			valueBytes, err := expansion.outputBytes(bySpec, byName)
			if err != nil {
				return err
			}
			valueParts, err := expansion.outputParts(bySpec, byName)
			if err != nil {
				return err
			}
			if valueBytes > limits.MaxOutputBytes-bytes-isolationBytes || valueParts > limits.MaxOutputParts-parts-isolationParts {
				budget.exhausted = true
				return ErrLimitExceeded
			}
			bytes += valueBytes + isolationBytes
			parts += valueParts + isolationParts
		}
		if bytes > maximumBytes {
			maximumBytes = bytes
		}
		if parts > maximumParts {
			maximumParts = parts
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return budget.require(maximumBytes, maximumParts)
}

func (e workExpansion) outputParts(specs map[string]ArgumentSpec, arguments map[string]Argument) (int, error) {
	if e.argument == "" {
		return 1, nil
	}
	spec, exists := specs[e.argument]
	if !exists {
		return 0, fmt.Errorf("%w: unknown expansion argument", ErrInvalidMessage)
	}
	argument, present := arguments[e.argument]
	if !present || argument.null {
		return 1, nil
	}
	switch spec.Type {
	case TypeInteger:
		return e.numericOutputParts(numericInput{kind: numericSigned, signed: argument.signed})
	case TypeUnsignedInteger:
		return e.numericOutputParts(numericInput{kind: numericUnsigned, unsigned: argument.unsigned})
	case TypeBigInteger:
		return e.numericOutputParts(numericInput{kind: numericBig, big: argument.big})
	case TypeDecimal:
		return e.numericOutputParts(numericInput{kind: numericDecimal, decimal: argument.text})
	case TypeMoney:
		amount, _, ok := strings.Cut(argument.text, "\x00")
		if !ok {
			return 0, fmt.Errorf("%w: malformed Money argument", ErrInvalidMessage)
		}
		return numericOutputParts(numericInput{kind: numericDecimal, decimal: amount}, "currency", e.options)
	case TypeDate, TypeInstant:
		return maximumDateValueParts, nil
	default:
		return 1, nil
	}
}

func (e workExpansion) numericOutputParts(value numericInput) (int, error) {
	if e.offset != 0 {
		adjusted, err := value.offset(e.offset)
		if err != nil {
			return 0, err
		}
		value = adjusted
	}
	return numericOutputParts(value, e.function, e.options)
}

func (e workExpansion) outputBytes(specs map[string]ArgumentSpec, arguments map[string]Argument) (int, error) {
	if e.argument == "" {
		return e.literalBytes, nil
	}
	spec, exists := specs[e.argument]
	if !exists {
		return 0, fmt.Errorf("%w: unknown expansion argument", ErrInvalidMessage)
	}
	argument, present := arguments[e.argument]
	if !present || argument.null {
		return 0, nil
	}
	switch spec.Type {
	case TypeText, TypeEnum:
		return len(argument.text), nil
	case TypeBool:
		return len(strconv.FormatBool(argument.boolean)), nil
	case TypeDate, TypeInstant:
		return formattedValueOverhead, nil
	case TypeInteger:
		return e.numericOutputBytes(numericInput{kind: numericSigned, signed: argument.signed})
	case TypeUnsignedInteger:
		return e.numericOutputBytes(numericInput{kind: numericUnsigned, unsigned: argument.unsigned})
	case TypeBigInteger:
		return e.numericOutputBytes(numericInput{kind: numericBig, big: argument.big})
	case TypeDecimal:
		return e.numericOutputBytes(numericInput{kind: numericDecimal, decimal: argument.text})
	case TypeMoney:
		amount, _, ok := strings.Cut(argument.text, "\x00")
		if !ok {
			return 0, fmt.Errorf("%w: malformed Money argument", ErrInvalidMessage)
		}
		return numericOutputBytes(numericInput{kind: numericDecimal, decimal: amount}, "currency", e.options)
	default:
		return 0, fmt.Errorf("%w: unsupported expansion argument", ErrInvalidMessage)
	}
}

func (e workExpansion) numericOutputBytes(value numericInput) (int, error) {
	if e.offset != 0 {
		adjusted, err := value.offset(e.offset)
		if err != nil {
			return 0, err
		}
		value = adjusted
	}
	return numericOutputBytes(value, e.function, e.options)
}

func numericOutputBytes(value numericInput, function string, options map[string]any) (int, error) {
	digits := 0
	switch value.kind {
	case numericSigned:
		digits = 20
	case numericUnsigned:
		digits = 20
	case numericBig:
		if value.big == nil {
			return 0, fmt.Errorf("%w: nil big integer", ErrInvalidMessage)
		}
		digits = decimalIntegerBytes(value.big)
	case numericDecimal:
		parts, err := parseDecimal(value.decimal)
		if err != nil {
			return 0, err
		}
		point := len(parts.digits) - parts.fraction + parts.exponent
		switch {
		case point <= 0:
			digits = 1 - point + len(parts.digits)
		case point >= len(parts.digits):
			digits = point
		default:
			digits = len(parts.digits)
		}
	default:
		return 0, fmt.Errorf("%w: unknown numeric argument", ErrInvalidMessage)
	}
	if function == "percent" {
		digits += 2
	}
	for _, name := range []string{"minimumIntegerDigits", "minimumFractionDigits", "maximumFractionDigits", "minimumSignificantDigits", "maximumSignificantDigits"} {
		if value, ok := optionInt(options[name]); ok && value > digits {
			digits = value
		}
	}
	if digits > (int(^uint(0)>>1)-formattedValueOverhead)/8 {
		return int(^uint(0) >> 1), nil
	}
	return digits*8 + formattedValueOverhead, nil
}
