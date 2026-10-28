package tmplfuncs

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"text/template"
)

var (
	errCallRefused = errors.New("call is not supported")
	errPrintfForm  = errors.New("printf widths and argument indexes must be literal")
)

// argSize is the printed length of a value, read without printing a string.
func argSize(v any) int {
	if v == nil {
		return 0
	}
	if s, ok := v.([]string); ok {
		n := 2
		for _, x := range s {
			n += len(x) + 1
		}
		return n
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.String {
		return rv.Len()
	}
	return len(fmt.Sprint(v))
}

func argsSize(args []any) int {
	n := 0
	for _, a := range args {
		n += argSize(a) + 1
	}
	return n
}

// bounded refuses a result that would pass MaxOutput, before building it.
func bounded(size int, build func() string) (string, error) {
	if size > MaxOutput {
		return "", ErrOutputLimit
	}
	out := build()
	if len(out) > MaxOutput {
		return "", ErrOutputLimit
	}
	return out, nil
}

// literalNumber reads the digits at format[i:], capped past MaxOutput.
func literalNumber(format string, i int) (int, int) {
	n := 0
	for i < len(format) && format[i] >= '0' && format[i] <= '9' {
		n = min(n*10+int(format[i]-'0'), MaxOutput+1)
		i++
	}
	return n, i
}

// printfSize is an upper bound on what fmt.Sprintf(format, args...) prints. It
// refuses * widths and explicit argument indexes, whose cost it cannot see.
func printfSize(format string, args []any) (int, error) {
	widest := 0
	for _, a := range args {
		widest = max(widest, argSize(a))
	}
	total := len(format) + argsSize(args)
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		for i < len(format) && strings.IndexByte("+-# 0", format[i]) >= 0 {
			i++
		}
		width, precision := 0, 0
		width, i = literalNumber(format, i)
		if i < len(format) && format[i] == '.' {
			precision, i = literalNumber(format, i+1)
		}
		if i < len(format) && (format[i] == '*' || format[i] == '[') {
			return 0, errPrintfForm
		}
		total += width + precision + widest + 32
		if total > MaxOutput {
			return 0, ErrOutputLimit
		}
	}
	return total, nil
}

// boundedBuiltins replace the text/template builtins that can call into the
// data or build a string of any size; each refuses a result past MaxOutput.
var boundedBuiltins = template.FuncMap{
	"call": func(...any) (string, error) { return "", errCallRefused },
	"printf": func(format string, args ...any) (string, error) {
		size, err := printfSize(format, args)
		if err != nil {
			return "", err
		}
		return bounded(size, func() string { return fmt.Sprintf(format, args...) })
	},
	"print": func(args ...any) (string, error) {
		return bounded(argsSize(args), func() string { return fmt.Sprint(args...) })
	},
	"println": func(args ...any) (string, error) {
		return bounded(argsSize(args)+1, func() string { return fmt.Sprintln(args...) })
	},
	"html": func(args ...any) (string, error) {
		return bounded(argsSize(args), func() string { return template.HTMLEscaper(args...) })
	},
	"js": func(args ...any) (string, error) {
		return bounded(argsSize(args), func() string { return template.JSEscaper(args...) })
	},
	"urlquery": func(args ...any) (string, error) {
		return bounded(argsSize(args), func() string { return template.URLQueryEscaper(args...) })
	},
}

// boundedJoin is strings.Join over a list, refused past MaxOutput.
func boundedJoin(list, sep any) (string, error) {
	items, _ := ListStrings(list)
	s := valueString(sep)
	size := len(s) * len(items)
	for _, x := range items {
		size += len(x)
	}
	return bounded(size, func() string { return strings.Join(items, s) })
}
