package stdlo

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMust1(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	is.Equal("foo", Must1("foo", nil))
	is.PanicsWithValue("something went wrong", func() {
		Must1("", errors.New("something went wrong"))
	})
	is.PanicsWithValue("operation shouldn't fail: something went wrong", func() {
		Must1("", errors.New("something went wrong"), "operation shouldn't fail")
	})
	is.PanicsWithValue("operation shouldn't fail with foo: something went wrong", func() {
		Must1("", errors.New("something went wrong"), "operation shouldn't fail with %s", "foo")
	})

	is.Equal(1, Must1(1, true))
	is.PanicsWithValue("not ok", func() {
		Must1(1, false)
	})
	is.PanicsWithValue("operation shouldn't fail", func() {
		Must1(1, false, "operation shouldn't fail")
	})
	is.PanicsWithValue("operation shouldn't fail with foo", func() {
		Must1(1, false, "operation shouldn't fail with %s", "foo")
	})

	cb := func() error {
		return assert.AnError
	}
	is.PanicsWithValue("operation should fail: assert.AnError general error for testing", func() {
		Must0(cb(), "operation should fail")
	})

	is.PanicsWithValue("must: invalid err type 'int', should either be a bool or an error", func() {
		Must0(0)
	})
	is.PanicsWithValue("must: invalid err type 'string', should either be a bool or an error", func() {
		Must0("error")
	})
}

func TestMustX(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	{
		is.PanicsWithValue("something went wrong", func() {
			Must0(errors.New("something went wrong"))
		})
		is.PanicsWithValue("operation shouldn't fail with foo: something went wrong", func() {
			Must0(errors.New("something went wrong"), "operation shouldn't fail with %s", "foo")
		})
		is.NotPanics(func() {
			Must0(nil)
		})
	}

	{
		val1 := Must1(1, nil)
		is.Equal(1, val1)
		is.PanicsWithValue("something went wrong", func() {
			Must1(1, errors.New("something went wrong"))
		})
		is.PanicsWithValue("operation shouldn't fail with foo: something went wrong", func() {
			Must1(1, errors.New("something went wrong"), "operation shouldn't fail with %s", "foo")
		})
	}

	{
		val1, val2 := Must2(1, 2, nil)
		is.Equal(1, val1)
		is.Equal(2, val2)
		is.PanicsWithValue("something went wrong", func() {
			Must2(1, 2, errors.New("something went wrong"))
		})
		is.PanicsWithValue("operation shouldn't fail with foo: something went wrong", func() {
			Must2(1, 2, errors.New("something went wrong"), "operation shouldn't fail with %s", "foo")
		})
	}

	{
		val1, val2, val3 := Must3(1, 2, 3, nil)
		is.Equal(1, val1)
		is.Equal(2, val2)
		is.Equal(3, val3)
		is.PanicsWithValue("something went wrong", func() {
			Must3(1, 2, 3, errors.New("something went wrong"))
		})
		is.PanicsWithValue("operation shouldn't fail with foo: something went wrong", func() {
			Must3(1, 2, 3, errors.New("something went wrong"), "operation shouldn't fail with %s", "foo")
		})
	}

	{
		val1, val2, val3, val4 := Must4(1, 2, 3, 4, nil)
		is.Equal(1, val1)
		is.Equal(2, val2)
		is.Equal(3, val3)
		is.Equal(4, val4)
		is.PanicsWithValue("something went wrong", func() {
			Must4(1, 2, 3, 4, errors.New("something went wrong"))
		})
		is.PanicsWithValue("operation shouldn't fail with foo: something went wrong", func() {
			Must4(1, 2, 3, 4, errors.New("something went wrong"), "operation shouldn't fail with %s", "foo")
		})
	}

	{
		val1, val2, val3, val4, val5 := Must5(1, 2, 3, 4, 5, nil)
		is.Equal(1, val1)
		is.Equal(2, val2)
		is.Equal(3, val3)
		is.Equal(4, val4)
		is.Equal(5, val5)
		is.PanicsWithValue("something went wrong", func() {
			Must5(1, 2, 3, 4, 5, errors.New("something went wrong"))
		})
		is.PanicsWithValue("operation shouldn't fail with foo: something went wrong", func() {
			Must5(1, 2, 3, 4, 5, errors.New("something went wrong"), "operation shouldn't fail with %s", "foo")
		})
	}

	{
		val1, val2, val3, val4, val5, val6 := Must6(1, 2, 3, 4, 5, 6, nil)
		is.Equal(1, val1)
		is.Equal(2, val2)
		is.Equal(3, val3)
		is.Equal(4, val4)
		is.Equal(5, val5)
		is.Equal(6, val6)
		is.PanicsWithValue("something went wrong", func() {
			Must6(1, 2, 3, 4, 5, 6, errors.New("something went wrong"))
		})
		is.PanicsWithValue("operation shouldn't fail with foo: something went wrong", func() {
			Must6(1, 2, 3, 4, 5, 6, errors.New("something went wrong"), "operation shouldn't fail with %s", "foo")
		})
	}

	{
		is.PanicsWithValue("not ok", func() {
			Must0(false)
		})
		is.PanicsWithValue("operation shouldn't fail with foo", func() {
			Must0(false, "operation shouldn't fail with %s", "foo")
		})
		is.NotPanics(func() {
			Must0(true)
		})
	}

	{
		val1 := Must1(1, true)
		is.Equal(1, val1)
		is.PanicsWithValue("not ok", func() {
			Must1(1, false)
		})
		is.PanicsWithValue("operation shouldn't fail with foo", func() {
			Must1(1, false, "operation shouldn't fail with %s", "foo")
		})
	}

	{
		val1, val2 := Must2(1, 2, true)
		is.Equal(1, val1)
		is.Equal(2, val2)
		is.PanicsWithValue("not ok", func() {
			Must2(1, 2, false)
		})
		is.PanicsWithValue("operation shouldn't fail with foo", func() {
			Must2(1, 2, false, "operation shouldn't fail with %s", "foo")
		})
	}

	{
		val1, val2, val3 := Must3(1, 2, 3, true)
		is.Equal(1, val1)
		is.Equal(2, val2)
		is.Equal(3, val3)
		is.PanicsWithValue("not ok", func() {
			Must3(1, 2, 3, false)
		})
		is.PanicsWithValue("operation shouldn't fail with foo", func() {
			Must3(1, 2, 3, false, "operation shouldn't fail with %s", "foo")
		})
	}

	{
		val1, val2, val3, val4 := Must4(1, 2, 3, 4, true)
		is.Equal(1, val1)
		is.Equal(2, val2)
		is.Equal(3, val3)
		is.Equal(4, val4)
		is.PanicsWithValue("not ok", func() {
			Must4(1, 2, 3, 4, false)
		})
		is.PanicsWithValue("operation shouldn't fail with foo", func() {
			Must4(1, 2, 3, 4, false, "operation shouldn't fail with %s", "foo")
		})
	}

	{
		val1, val2, val3, val4, val5 := Must5(1, 2, 3, 4, 5, true)
		is.Equal(1, val1)
		is.Equal(2, val2)
		is.Equal(3, val3)
		is.Equal(4, val4)
		is.Equal(5, val5)
		is.PanicsWithValue("not ok", func() {
			Must5(1, 2, 3, 4, 5, false)
		})
		is.PanicsWithValue("operation shouldn't fail with foo", func() {
			Must5(1, 2, 3, 4, 5, false, "operation shouldn't fail with %s", "foo")
		})
	}

	{
		val1, val2, val3, val4, val5, val6 := Must6(1, 2, 3, 4, 5, 6, true)
		is.Equal(1, val1)
		is.Equal(2, val2)
		is.Equal(3, val3)
		is.Equal(4, val4)
		is.Equal(5, val5)
		is.Equal(6, val6)
		is.PanicsWithValue("not ok", func() {
			Must6(1, 2, 3, 4, 5, 6, false)
		})
		is.PanicsWithValue("operation shouldn't fail with foo", func() {
			Must6(1, 2, 3, 4, 5, 6, false, "operation shouldn't fail with %s", "foo")
		})
	}
}

// type internalError struct {
// 	foobar string
// }

// func (e *internalError) Error() string {
// 	return "internal error"
// }

// func TestErrorsAs(t *testing.T) {
// 	t.Parallel()
// 	is := assert.New(t)

// 	err, ok := ErrorsAs[*internalError](fmt.Errorf("hello world"))
// 	is.False(ok)
// 	is.Nil(nil, err)

// 	err, ok = ErrorsAs[*internalError](&internalError{foobar: "foobar"})
// 	is.True(ok)
// 	is.Equal(&internalError{foobar: "foobar"}, err)

// 	err, ok = ErrorsAs[*internalError](nil)
// 	is.False(ok)
// 	is.Nil(nil, err)
// }
