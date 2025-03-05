// Package stdlo provides a minimal version of the lo project.
package stdlo

import (
	"fmt"
	"reflect"
)

// Must0 has the same behavior as Must, but callback returns no variable.
// Play: https://go.dev/play/p/TMoWrRp3DyC
func Must0(err any, messageArgs ...any) {
	must(err, messageArgs...)
}

// Must1 is an alias to Must
// Play: https://go.dev/play/p/TMoWrRp3DyC
func Must1[T any](val T, err any, messageArgs ...any) T {
	must(err, messageArgs...)
	return val
}

// Must2 has the same behavior as Must, but callback returns 2 variables.
// Play: https://go.dev/play/p/TMoWrRp3DyC
func Must2[T1, T2 any](val1 T1, val2 T2, err any, messageArgs ...any) (T1, T2) {
	must(err, messageArgs...)
	return val1, val2
}

// Must3 has the same behavior as Must, but callback returns 3 variables.
// Play: https://go.dev/play/p/TMoWrRp3DyC
func Must3[T1, T2, T3 any](val1 T1, val2 T2, val3 T3, err any, messageArgs ...any) (T1, T2, T3) {
	must(err, messageArgs...)
	return val1, val2, val3
}

// Must4 has the same behavior as Must, but callback returns 4 variables.
// Play: https://go.dev/play/p/TMoWrRp3DyC
func Must4[T1, T2, T3, T4 any](val1 T1, val2 T2, val3 T3, val4 T4, err any, messageArgs ...any) (T1, T2, T3, T4) {
	must(err, messageArgs...)
	return val1, val2, val3, val4
}

// Must5 has the same behavior as Must, but callback returns 5 variables.
// Play: https://go.dev/play/p/TMoWrRp3DyC
//
//nolint:lll
func Must5[T1, T2, T3, T4, T5 any](val1 T1, val2 T2, val3 T3, val4 T4, val5 T5, err any, messageArgs ...any) (T1, T2, T3, T4, T5) {
	must(err, messageArgs...)
	return val1, val2, val3, val4, val5
}

// Must6 has the same behavior as Must, but callback returns 6 variables.
// Play: https://go.dev/play/p/TMoWrRp3DyC
//
//nolint:lll
func Must6[T1, T2, T3, T4, T5, T6 any](val1 T1, val2 T2, val3 T3, val4 T4, val5 T5, val6 T6, err any, messageArgs ...any) (T1, T2, T3, T4, T5, T6) {
	must(err, messageArgs...)
	return val1, val2, val3, val4, val5, val6
}

// must panics if err is error or false.
func must(err any, messageArgs ...any) {
	if err == nil {
		return
	}

	switch errt := err.(type) {
	case bool:
		if !errt {
			message := messageFromMsgAndArgs(messageArgs...)
			if message == "" {
				message = "not ok"
			}

			panic(message)
		}

	case error:
		message := messageFromMsgAndArgs(messageArgs...)
		if message != "" {
			panic(message + ": " + errt.Error())
		}

		panic(errt.Error())
	default:
		panic("must: invalid err type '" + reflect.TypeOf(err).Name() + "', should either be a bool or an error")
	}
}

func messageFromMsgAndArgs(msgAndArgs ...any) string {
	if len(msgAndArgs) == 1 {
		if msgAsStr, ok := msgAndArgs[0].(string); ok {
			return msgAsStr
		}
		return fmt.Sprintf("%+v", msgAndArgs[0])
	}
	if len(msgAndArgs) > 1 {
		return fmt.Sprintf(msgAndArgs[0].(string), msgAndArgs[1:]...) //nolint:forcetypeassert
	}
	return ""
}
